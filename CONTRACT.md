# abstraction.model contract

[`model.thrift`](model.thrift) defines `abstraction.model/resolver@1`,
same-account identity-bound model lookup through explicitly configured
registry providers, and `abstraction.model/manifest@1`, the same lookup
published as a storage descriptor. This file states the obligations a
provider meets; [README.md](README.md) holds the walkthrough.

Binds: `model.thrift`

## Reading this page

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in this
document are to be interpreted as described in BCP 14 [RFC2119] [RFC8174]
when, and only when, they appear in all capitals, as shown here.

The rule ids below, such as `MODEL-C1`, are what tests and refusals cite. A
retired id is never reused; [HISTORY.md](HISTORY.md) keeps it with the
release it left.

| Letter | Meaning |
| --- | --- |
| C | resolution and descriptor rules |

This release renames no field of `resolver@1` or `manifest@1`; Reading this
page carries no prose-to-wire table.

## Resolution

`ModelResolver.Resolve(Ref)` uses providers configured by the service host.
The service accepts no provider configuration, local backing-file path or
download destination from a caller. `Ref.File` identifies a file inside a
model repository; it does not grant a local filesystem path. The service
performs lookup only and submits no work.

`LookupResult.request` is present exactly when `outcome` is `resolved`. The
request preserves the complete artifact identity, size and ordered source
list. Its nonempty digest must be SHA-256 with 32 hexadecimal bytes; size
zero remains unknown and negative size refuses. Sources must satisfy the
portable HTTP(S) request validator.

A source carrying attributes, headers or nonzero priority refuses as
unsupported, except the credential name of the Ref itself. A local/file
source, private Sink or existing handoff Request identity also refuses. A
service never silently removes such a source because another source is
portable. Explicit native adapters retain those uses. HTTP URL user
information refuses through the existing execution validator.

**[MODEL-C1] Credential application.** The service MUST apply a `Ref`'s
named credential to its registry metadata requests, through
`abstraction.credentials/applier@1` with consumer
`abstraction.model/resolver@1` and the Program-proven caller as subject.
Every source of the resolved request MUST carry the same `credential` name.
Every source MUST NOT carry a header or a secret. The service MUST read an
applier refusal of `unknown`, `expired`, `revoked`, `lost`, `not_permitted`,
`target_refused`, `consumer_refused`, `unsupported_kind`, `invalid` or
`forbidden` as `unsupported_mapping`. The service MUST read an applier that
cannot answer as `unavailable`. A host configured without an applier MUST
refuse such a `Ref` as `unsupported_mapping` before any registry request. A
redirect to another host MUST NOT carry the applied headers.

The six Ref fields are passed directly to the selected resolver. HF lookup
rejects simultaneous File and Quant and revision path/query delimiters;
Ollama rejects File and Quant because that resolver does not use them.
Custom registry providers receive every field without a string round trip.

`modelget` and the local-store adapters `local.go` uses run the deprecated
in-process resolver (`model.NewRegistry`, `model.Default`) directly, without
a runtime; see [README.md](README.md#start).
`model.NewServiceRegistry(providers...)`, which backs `ModelResolver`,
performs no local-store discovery. `service.Listen(endpoint, registry)`
requires a nonnil configured registry. Configure Add/SetLocal before serving
concurrent calls. Selecting a native registry with local augmentation
explicitly can yield unsupported mappings. Providers must honor their
context; provider configuration and implementation are trusted service-side
code.

The receiver obtains shared `listen.Program` evidence and rechecks the peer
user against the host account before invoking a provider. Caller-supplied
fields do not supply identity. Native process/path proof remains unavailable
on macOS; this service does not weaken that requirement. Authenticating the
server to the caller remains the shared transport's separate obligation.

## The descriptor is the model identity

**[MODEL-C2] The descriptor's keys.** A manifest of kind
`abstraction.model/descriptor@1` MUST carry a descriptor whose keys are
`family`, `quant`, `format`, `architecture`, `size_label`, `finetune`,
`base`, `source` and `role`. `family` is what two names from two programs
must agree on for one model to answer for both. `quant` MUST be spelled as
this layer's identity rules spell it, in lower case. Every key is optional;
an absent key states that the publisher could not say it. A publisher MUST
NOT write a key it did not read.

**[MODEL-C3] Component roles.** A component is part of a family. `role`
MUST name it: `weights` for the servable artifact a family is built from, or
a storage inventory source's own closed vocabulary for a part beside it —
`projector` and `vae` are the seeded values, read from the same classifier
that names an entry's `role` (`abstraction.storage/object@1`'s roles). A
descriptor with no `role` key states `weights`; the key exists so a reader
does not have to reclassify the file itself. `base` carries a second meaning
for a component: the family it belongs to, read from the component's own
name when that name derives one (`mmproj-<family>`, `<family>-vae`), beside
`base`'s other meaning, the model a weights file was fine-tuned from. A
router that folds a component's identity into its base family's detail MUST
read `base` for the target and `role` for the word it lists the component
under. A component whose `base` cannot be derived keeps its own `family`,
listed under it and distinguished by `role`.

That descriptor is the identity of the weights, wherever they are named:
`ModelManifest.Resolve` publishes one for a registry reference, and a
storage inventory source publishes one for a file a store holds. A reader
that has a descriptor for a name uses the `family` it declares. A reader
with no descriptor for a name may fall back to parsing that name by this
layer's identity rules, and says which of the two it did.

## Outcomes

| Outcome | Meaning |
| --- | --- |
| resolved | A complete portable request is present. |
| invalid | The supplied reference is missing required identity or contains unsupported/ambiguous fields. |
| unavailable | No configured resolver could supply a mapping, or the lookup failed. This does not classify the failure as transient or permanent. |
| unsupported_mapping | The existing native mapping cannot be represented completely by the portable request. No partial projection is returned. |
| forbidden | Bound caller identity does not authorize this service account's lookup. |

## Bounds

The service owns no bytes and walks no directories. Stores, holders and what
a machine already holds are `abstraction.storage/inventory@1`'s, composed by
the runtime over its declared sources. The Go package's `Inventory()` was
removed the same way; see [HISTORY.md](HISTORY.md).

Go clients accept a fresh context per call and preserve default reuse. Calls
use a five-second default wait and a 1 MiB frame limit. The host bounds each
accepted connection to five seconds and admits at most 32 concurrent
connections. A caller waiting error is neither a permanent lookup refusal
nor authorization to submit, retry or cancel a job. A resolved request still
needs separate job admission and its own caller-owned identity and
requirements.

## Divergences

None declared for this release.

## Not built

- **Python service transport.** This repository does not yet package one;
  the generated module imports the installed download request namespace
  only.

## Generated dependency packaging

Generate download/request.thrift with `--named-codecs`. Model generation
uses
`--go-import=request=github.com/openabstractions/abstraction-download/go/abstraction/download/request`;
these flags are recorded in generate.targets. Go module publication must pin
a download revision containing those exported codecs.

C++ consumers install the generated `abstraction_download_request` package,
then this repository's cpp package. `find_package(abstraction_model_api
CONFIG REQUIRED)` provides `abstraction::model_api`, which links
`abstraction::download_request`. No store, native download provider or C++
server is included.
