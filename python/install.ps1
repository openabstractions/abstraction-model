#Requires -Version 5.1
[CmdletBinding()]
param(
    [string]$Python,
    [int]$Port = 11800,
    [switch]$NoStart
)

$ErrorActionPreference = 'Stop'

$TaskName = 'abstraction-resident-broker'
$Script = Join-Path $PSScriptRoot 'resident.py'
$LogDir = Join-Path $env:USERPROFILE '.abstraction\logs'

function Find-Interpreter {
    param([string]$Preferred)
    $candidates = @()
    if ($Preferred) { $candidates += $Preferred }
    if ($env:RESIDENT_PYTHON) { $candidates += $env:RESIDENT_PYTHON }
    $launcher = (Get-Command py.exe -ErrorAction SilentlyContinue).Source
    if ($launcher) {
        $found = & $launcher -3 -c "import sys; print(sys.executable)" 2>$null
        if ($LASTEXITCODE -eq 0 -and $found) { $candidates += $found }
    }
    $candidates += (Get-Command python.exe -ErrorAction SilentlyContinue | ForEach-Object Source)
    $candidates += Get-ChildItem "$env:LOCALAPPDATA\Programs\Python\Python3*\python.exe",
                                "C:\Python3*\python.exe" -ErrorAction SilentlyContinue |
                   ForEach-Object FullName

    foreach ($c in $candidates) {
        if (-not $c -or -not (Test-Path $c)) { continue }
        if ($c -like '*\WindowsApps\*') { continue }
        $windowless = Join-Path (Split-Path $c) 'pythonw.exe'
        if (-not (Test-Path $windowless)) { continue }
        & $c -c "import sys, http.server, urllib.request, ctypes; sys.path.insert(0, r'$PSScriptRoot'); import abstraction_model" 2>$null | Out-Null
        if ($LASTEXITCODE -eq 0) { return $windowless }
    }
    throw ("No usable Python 3 found. The 'python' on PATH is the Microsoft Store alias stub, " +
           "which cannot run a scheduled task. Pass -Python <path to python.exe>.")
}

function Stop-StrayBrokers {
    $stray = Get-CimInstance Win32_Process -Filter "Name='pythonw.exe' OR Name='python.exe'" |
             Where-Object { $_.CommandLine -like '*resident.py*' }
    foreach ($p in $stray) {
        Write-Host "  stopping stray broker, pid $($p.ProcessId)"
        Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
    }
    if ($stray) { Start-Sleep -Milliseconds 700 }
}

if (-not (Test-Path $Script)) { throw "resident.py not found beside this script" }
if (([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
    ).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Warning "Running elevated. The task will be registered for the elevated account, which may not be the account you log in as."
}

$interpreter = Find-Interpreter $Python
Write-Host "interpreter   $interpreter"
Write-Host "script        $Script"
Write-Host "logs          $LogDir\resident.log"

New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

$action = New-ScheduledTaskAction -Execute $interpreter -Argument "`"$Script`" $Port" -WorkingDirectory $PSScriptRoot
$atLogon = New-ScheduledTaskTrigger -AtLogOn -User "$env:USERDOMAIN\$env:USERNAME"
$everyMinute = New-ScheduledTaskTrigger -Once -At (Get-Date).AddMinutes(-1) `
    -RepetitionInterval (New-TimeSpan -Minutes 1)
$everyMinute.Repetition.Duration = $null
$everyMinute.Repetition.StopAtDurationEnd = $false
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType Interactive -RunLevel Limited
$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -Hidden -MultipleInstances IgnoreNew -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -StartWhenAvailable

Stop-StrayBrokers
Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger @($atLogon, $everyMinute) `
    -Principal $principal -Settings $settings `
    -Description "Routes applications to whichever local host already holds the model. abstraction-model/python/resident.py." | Out-Null

Write-Host "registered    $TaskName (at logon, re-checked every minute, so a broker that dies is back within 60s)"

if ($NoStart) { return }

Start-ScheduledTask -TaskName $TaskName
for ($i = 0; $i -lt 30; $i++) {
    Start-Sleep -Milliseconds 400
    try {
        $r = Invoke-WebRequest "http://127.0.0.1:$Port/findings" -UseBasicParsing -TimeoutSec 3
        Write-Host "running       http://127.0.0.1:$Port/  ($($r.StatusCode))"
        return
    } catch { }
}
Write-Warning "Task started but nothing answered on 127.0.0.1:$Port. See $LogDir\resident.log"
