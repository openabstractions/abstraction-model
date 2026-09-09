#Requires -Version 5.1
[CmdletBinding()]
param(
    [int]$Port = 11800,
    [switch]$KeepLogs
)

$ErrorActionPreference = 'Stop'

$TaskName = 'abstraction-resident-broker'
$LogDir = Join-Path $env:USERPROFILE '.abstraction\logs'

$task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($task) {
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    Write-Host "removed       scheduled task $TaskName"
} else {
    Write-Host "absent        scheduled task $TaskName"
}

$stray = Get-CimInstance Win32_Process -Filter "Name='pythonw.exe' OR Name='python.exe'" |
         Where-Object { $_.CommandLine -like '*resident.py*' }
foreach ($p in $stray) {
    Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
    Write-Host "stopped       broker process $($p.ProcessId)"
}
if (-not $stray) { Write-Host "absent        no broker process was running" }

$logs = Get-ChildItem $LogDir -Filter 'resident.log*' -ErrorAction SilentlyContinue
if ($logs -and -not $KeepLogs) {
    $logs | Remove-Item -Force
    Write-Host "removed       $($logs.Count) log file(s) from $LogDir"
    if (-not (Get-ChildItem $LogDir -ErrorAction SilentlyContinue)) {
        Remove-Item $LogDir -Force
        Write-Host "removed       empty $LogDir"
    }
} elseif ($logs) {
    Write-Host "kept          $($logs.Count) log file(s) in $LogDir"
} else {
    Write-Host "absent        no log files in $LogDir"
}

Start-Sleep -Milliseconds 500
$left = @()
if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) { $left += 'scheduled task' }
if (Get-CimInstance Win32_Process -Filter "Name='pythonw.exe' OR Name='python.exe'" |
    Where-Object { $_.CommandLine -like '*resident.py*' }) { $left += 'broker process' }
if (Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue) {
    $left += "something still listening on $Port"
}

if ($left) {
    Write-Warning ("left behind: " + ($left -join '; '))
    exit 1
}
Write-Host "clean         nothing of the broker is left on this machine"
