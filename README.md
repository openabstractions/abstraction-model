# abstraction-model

*For applications that name a model and need its exact files.*

Resolve a model name to the exact files it means, each with a digest a
download can verify. Turn a model name from Hugging Face, Ollama, LM Studio or another registry
into one exact set of verifiable bytes before downloading it, and tell
whether two differently-named models are the same one. A `family` is what
two names for the same weights must agree on before one of them can answer
for the other. The resolver understands revisions, published digests,
quantization and family aliases.

**What's new here:** A model name resolves to exact, verifiable files, and two
names for the same model resolve to one descriptor (family, quantisation,
format), so the copy Ollama, LM Studio or the Hugging Face cache already holds
is recognised as the same model.

## Start

Install the runtime first: https://openabstractions.org/adopt.html

    go get github.com/openabstractions/abstraction-model/go

Applications select `abstraction.model/resolver@1` through the facade and
call the typed resolver:

```go
import (
	facade "github.com/openabstractions/abstraction-facade/go/client"
	model "github.com/openabstractions/abstraction-model/go/client"
)

machine := facade.Discover()
resolver, err := machine.ResolveModel(ctx, facade.Requirements{Scope: facade.ScopeLocal})
// Handle err before using resolver.
result, err := resolver.Resolve(model.Ref{Registry: "hf", Repo: "bartowski/Qwen2.5-0.5B-Instruct-GGUF", Quant: "IQ2_M"})
```

`result.Outcome` is `resolved` exactly when `result.Request` carries a
complete, verifiable download request; every other outcome explains why not.
[CONTRACT.md](CONTRACT.md) describes waiting, authorization and unsupported
native mappings. Whether to adopt this at all, and what it costs:
[Adopting](CONTRIBUTING.md#adopting).

**Without the runtime**, `ResolveModel` fails with a typed resolution error;
nothing here falls back to a local scan on its own. `modelget`, below, needs
no runtime: it runs its own deprecated in-process resolver directly
(`model.NewRegistry`, `model.Default`), one explicitly selected native
workflow among several (`modelget`, `Registry`, and the local-store adapters
`local.go` uses).

```
$ modelget where ollama://qwen2.5:0.5b
digest  sha256:c5396e06af294bd101b30dce59131a76d2b773e76950acc870eda801d3ab0515
size    379.4 MiB
local   %USERPROFILE%\.ollama\models\blobs\sha256-c5396e06...  (ollama)

these exact bytes are already on this machine 1 time(s).
fetching this would cost nothing but a copy.
```

This is the AI-specific layer. Below it,
[`download`](https://github.com/openabstractions/abstraction-download) moves
bytes and refuses anything whose digest is wrong; below that,
[`job`](https://github.com/openabstractions/abstraction-job) makes the work
outlive the process that asked for it. This layer knows the few things that
are specific to model weights.

## Is that the same model as this one?

```
$ modelget identify Qwen3.6-35B-A3B-MTP-GGUF qwen/qwen3.6-35b-a3b
qwen3.6-35b-a3b   Qwen3.6-35B-A3B-MTP-GGUF
qwen3.6-35b-a3b   qwen/qwen3.6-35b-a3b

same model. one of these can answer for both.
```

Four hosts on one machine name one model four ways, and none of them can tell
that the other three are holding it. Two applications each pointed at their
own host loaded the same weights twice and cost 18.3 GB of GPU memory.
Answering that question is what `identity` is for. It lives in its own
module, `github.com/openabstractions/abstraction-model/identity`, with no
imports outside the standard library, so a caller such as a router or a
storage inventory source can take it without taking the download stack.

Four callers import it: this module's own `modelget`, `abstraction-router`,
`abstraction-storage-over-local-stores` and `abstraction-inference`. None of
the three outside this repository import anything else from
`abstraction-model` — not the descriptor types, not the resolver.

```go
identity.Family("unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf")
// "qwen3.6-35b-a3b"
identity.Same("Qwen3.6-35B-A3B-MTP-GGUF", "qwen/qwen3.6-35b-a3b")  // true
```

**Every token that is not known packaging is kept.** The rule everyone
reaches for first is a list of noise to strip, and it is wrong in the
direction that matters: it collapses
`gemma-4-26b-a4b-it-ultra-uncensored-heretic` onto `Gemma-4-26B-A4B-it`, and
then a request for Google's release is answered by an abliterated fine-tune.
Over-splitting costs a missed saving; over-merging answers with the wrong
model.

**Quantisation is not identity, and it is not nothing either.** The same
weights at `Q4_K_M` and at `UD-Q4_K_XL` are one model for routing and two
files for storage — one `Family`, two `Quant`s, and 275.8 GB across four
silos on the machine this was written against contains zero byte-identical
duplicates.

The 42 identifiers in [`testdata/identifiers.tsv`](testdata/identifiers.tsv)
were read from the four running services, and Go and Python are held to them
byte-for-byte by `scripts/identity-conformance.sh`. Python is in
[`python/`](python/README.md) because a consumer is a ComfyUI node: what to
install, what to import and one example that runs are on that page.

## What is already here, by family

Not this layer's question. This layer owns naming: a reference, a digest, a
portable request and a descriptor that says what the weights are. Stores,
holders and what a machine already holds belong to the storage inventory,
`abstraction.storage/inventory@1`, composed by the runtime over its declared
sources. `abstraction-storage-over-local-stores` is one such source: it
reads Ollama, the Hugging Face cache, LM Studio, ComfyUI, FastFlowLM, Jan,
Stability Matrix, Pinokio, SwarmUI and operator-named directories —
`ABSTRACTION_MODEL_ROOTS` is where those are named — and publishes an
`abstraction.model/descriptor@1` manifest for each model it can name.

**Known limit of the naming rules.** A speculative-decoding draft head
shipped as its own file (`mtp-gemma-4-26B-A4B-it.gguf`) lands in its
parent's family, because `mtp` has to be dropped for
`Qwen3.6-35B-A3B-MTP-GGUF` to meet `qwen/qwen3.6-35b-a3b` and this parser
does not care where in a name a token sits. `mmproj` and `vae` are handled,
as roles that never become the base.

## The three things it knows

**The digest is published, not supplied.** Nobody asking for a model knows
its hash. HuggingFace publishes `lfs.sha256`; Ollama names its blobs by
digest. This layer's job is to obtain that number *before a byte moves*, so
the layer below has something to refuse against. When a registry will not
say, this refuses:

```
hf: model.gguf in org/repo@main has no lfs.sha256
    — refusing to fetch bytes nobody can check
```

**A model reference is not a URL.** `hf://org/repo@revision#quant` selects
one file from a repo that may hold twenty quantisations of the same weights.
It refuses to guess between them, because picking "the first" would make one
reference mean different bytes depending on what order an API returned. It
also pins to the **resolved commit** rather than the branch you named — a
branch moves, and a job that resumes next week must fetch the bytes it
started on.

**You probably already have it.** Four stores on this machine hold 116 GB
between them and none of them know about each other.

## The dedup, and why it costs nothing

Two of those four stores already name files by content:

| store | layout | matchable? |
|---|---|---|
| Ollama | `blobs/sha256-<hex>` | **yes** — named by digest |
| HuggingFace | `blobs/<etag>` | **yes** — the etag is the sha256 for LFS |
| Lemonade | reads the HuggingFace layout | yes |
| LM Studio | by filename only | **no** |

So once a registry has told us the digest, finding out whether those exact
bytes are already on disk is a **stat, not a scan** — no hashing of 116 GB.
A hit becomes a `file:` source at priority 0, and because `download.Runner`
already tries sources in priority order, cross-store dedup falls out with
**no special case anywhere**. A model Ollama pulled does not get downloaded
again for something else; it gets copied and verified.

LM Studio's absence from that table is the argument in miniature: it is the
largest store here at 44 GB and it publishes no digest, so nothing can tell
whether its bytes are anyone else's.

**A local hit is a candidate, not an answer.** Ollama never checks its own
digests — G3 corrupted a blob in place and Ollama listed it, loaded it and
served confident nonsense while the filename still claimed the original
hash. So the layer below verifies whatever it copies, and there is a test
for exactly that.

## Use it

```bash
modelget get hf://bartowski/Qwen2.5-0.5B-Instruct-GGUF#IQ2_M -o D:/models/
modelget get <ref> --here         # do it in this process, whatever else exists
modelget list                     # what is in flight, and what finished
modelget resume                   # pick up everything interrupted
modelget where <ref>              # digest, size, and where it can be had
```

**Where the download happens is not an argument.** Somebody asking for a
model wants the model, and which tier fetches it is a property of the
machine. `modelget` registers whatever is configured and reachable, best
first, and hands the job to the first thing that will take it — a NAS if
`ABSTRACTION_NAS_STORE` points at a store something is watching, else the OS
transfer service, else this process.

```
$ export ABSTRACTION_NAS_STORE='//nas.invalid/home/abstraction-store'
$ modelget get hf://bartowski/Qwen2.5-0.5B-Instruct-GGUF#IQ2_M -o D:/models/
handed to the NAS. It continues if this process exits,
or the machine sleeps. Check on it with: modelget list
```

Nothing in that command mentions a NAS. Unset the variable and it uses BITS;
the command does not change. The NAS side is
[`addon-synology`](https://github.com/openabstractions/addon-synology).

## The complaint this answers, tested

> you start a 40 GB download, you close the laptop, and it starts again from zero

```bash
bash scripts/kill-and-resume.sh
```

A **real** 313 MB model from HuggingFace, killed with `SIGKILL` partway
through — no graceful shutdown, which is what closing a lid looks like to a
process. A separate invocation finds the job, resumes from the byte the
dead one had *proven*, and delivers a file whose sha256 matches what
HuggingFace published.

Transcript:
[`docs/results/RESUME1.txt`](https://github.com/openabstractions/abstractions/blob/main/docs/results/RESUME1.txt).

```
bytes on disk:   61163581
bytes PROVEN:    52824170
```

That gap is the design. The dead process wrote more than it had
checkpointed, nothing vouches for the difference, so the resume starts from
the proven number and discards the rest.

Two things that test found, both now fixed:

- **Checkpointing was byte-triggered only.** At 8 MiB intervals, a download
  killed after 12 seconds on a slow link had saved *nothing*. Ten minutes on
  a bad connection would also have saved nothing — and a slow connection is
  exactly when resuming matters. Now it checkpoints on bytes **or** elapsed
  time.
- **The lease could expire mid-transfer.** Renewal rode the same callback,
  so a slowly-progressing download would be adopted as an orphan while
  still running.

## Limits

- **Immediately after a crash, `resume` waits.** The dead owner's lease has
  not lapsed, and nothing may write to a job another owner might still
  hold. It now says so, with a countdown, instead of "nothing to resume".
  Since the owner string carries host and PID, a local liveness check could
  break the lease at once rather than waiting — worth doing, not done.
- **Multi-file artifacts are not supported.** Sharded safetensors, diffusers
  directories and ONNX-with-external-data are one artifact in several
  files, and a Spec describes one file with one digest. Ollama's
  manifest-of-layers is the shape to copy.
- **Credentials are referenced, not stored** — fixed, and there is a
  regression test that reads the bytes on disk. The record holds
  `"credential": "hf"`; the secret comes from `$ABSTRACTION_CRED_HF` at the
  moment of the request (`HF_TOKEN` is accepted as an alias). The
  environment is not a strong store; on Windows this should use Credential
  Manager, which is the same "use the OS's own mechanism" call as BITS and
  the scheduled task. That is not done.
- **Anything resuming a job needs the credential in its own environment.** A
  supervisor started by the scheduler does not inherit your shell.
- **Only `.gguf` is auto-selected.** Name a file explicitly for anything
  else.

## Tested

### Regenerating the service API

`model.thrift` includes `../abstraction-download/request.thrift`.
Contributors need sibling `abstraction-model` and `abstraction-download`
source checkouts at the matching revisions supplied by the release tooling.
A lone model checkout does not contain the dependency schema needed for
regeneration. Generate the download request with `--named-codecs`, and
generate model with
`--go-import=request=github.com/openabstractions/abstraction-download/go/abstraction/download/request`.
The organization generation targets record these flags. Installed generated
API consumers use the normal language/package dependency on download
request; they do not need the sibling schema sources.

```bash
cd go && go test ./...
```

11 tests: reference parsing, revision pinning, refusing a file with no
published digest, refusing to guess between quantisations, rejecting
path-traversing filenames from a remote API, resolving Ollama from its
local store with no network, the local-store lookup, not offering the same
file twice, an end-to-end run that uses a local copy while the "remote" is
unreachable, and a corrupt local copy being caught despite a filename that
claims otherwise.

<!-- BEGIN GENERATED PERMISSION DEFINITIONS -->

## Permission definitions

Generated from the existing compiled catalogue. [RIGHTS.json](RIGHTS.json) contains the complete action/profile records, disclosure, lifecycle, first-use behavior, selector support and finite bundles.

This reference records compiled receiving support; RIGHTS.json names the compilation context. A connected runtime advertises current availability and selector support. These definitions supply no grant.

| Action | Resource profile | Owning operations | First-use targets | Compiled receiver |
|---|---|---|---|---|
| `abstraction.model/lookup` | `abstraction.rights/legacy-exact-string@1` | `abstraction.model/resolver@1#Resolve` | Legacy `exact` | served in compiled context |

<!-- END GENERATED PERMISSION DEFINITIONS -->
