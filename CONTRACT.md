# Model lookup service

`ModelResolver.Resolve(Ref)` uses providers configured by the service host.
The service accepts no provider configuration, local backing-file path or
download destination from a caller. `Ref.File` identifies a file inside a model repository; it does not grant
a local filesystem path. The service performs lookup only and submits no work.

`LookupResult.request` is present exactly when `outcome` is `resolved`. The request
preserves the complete artifact identity, size and ordered source list. Its
nonempty digest must be SHA-256 with 32 hexadecimal bytes; size zero remains
unknown and negative size refuses. Sources must satisfy the existing portable
anonymous HTTP(S) request validator.

| Outcome | Meaning |
| --- | --- |
| resolved | A complete portable request is present. |
| invalid | The supplied reference is missing required identity or contains unsupported/ambiguous fields. |
| unavailable | No configured resolver could supply a mapping, or the lookup failed. This does not classify the failure as transient or permanent. |
| unsupported_mapping | The existing native mapping cannot be represented completely by the portable request. No partial projection is returned. |
| forbidden | Bound caller identity does not authorize this service account's lookup. |

A source carrying attributes, credential references, headers or nonzero priority
refuses as unsupported. A local/file source, private Sink or existing handoff
Request identity also refuses. A service never silently removes such a source
because another source is portable. Explicit native adapters retain those uses.
HTTP URL user information refuses through the existing execution validator.

The five Ref fields are passed directly to the selected resolver. HF lookup
rejects simultaneous File and Quant and revision path/query delimiters; Ollama
rejects File and Quant because that resolver does not use them. Custom registry
providers receive every field without a string round trip.

`model.NewServiceRegistry(providers...)` performs no local-store discovery.
`service.Listen(endpoint, registry)` requires a nonnil configured registry.
Configure Add/SetLocal before serving concurrent calls. Selecting a native
registry with local augmentation explicitly can yield unsupported mappings.
Providers must honor their context; provider configuration and implementation
are trusted service-side code.

The receiver obtains shared `listen.Program` evidence and rechecks the peer user
against the host account before invoking a provider. Caller-supplied fields do
not supply identity. Native process/path proof remains unavailable on macOS;
this service does not weaken that requirement. Authenticating the server to the
caller remains the shared transport's separate obligation.

Go clients accept a fresh context per call and preserve default reuse. Calls use
a five-second default wait and a 1 MiB frame limit. The host bounds each accepted
connection to five seconds and admits at most 32 concurrent connections. A caller
waiting error is neither a permanent lookup refusal nor authorization to submit,
retry or cancel a job. A resolved request still needs separate job admission and
its own caller-owned identity and requirements.

## Generated dependency packaging

Generate download/request.thrift with `--named-codecs`. Model generation uses
`--go-import=request=github.com/openabstractions/abstraction-download/go/abstraction/download/request`;
these flags are recorded in generate.targets. Go module publication must pin a
download revision containing those exported codecs.

C++ consumers install the generated `abstraction_download_request` package, then
this repository's cpp package. `find_package(abstraction_model_api CONFIG REQUIRED)`
provides `abstraction::model_api`, which links `abstraction::download_request`.
No store, native download provider or C++ server is included. Python's generated
module likewise imports the installed download request namespace; this repository
does not yet package a Python service transport binding.
