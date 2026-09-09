# abstraction-model

A model reference such as `hf://org/repo#Q4_K_M` resolves to the published
sha256, the places the bytes can be obtained and any copy already on this
machine before a byte moves, and two names from two hosts can be asked whether
they mean one model.

## The problem

A program that wants model weights has a name, not a hash, and the layers that
can resume a transfer or refuse corrupt bytes need a hash. This layer obtains
the published digest first — from HuggingFace's `lfs.sha256`, or from the digest
Ollama already names its blobs by — and refuses to fetch anything a registry
will not vouch for. What it returns is a `download.Spec`: the artifact's digest
and size with an ordered list of sources. Having the digest makes it cheap to
check whether the bytes are already on disk, because the Ollama and HuggingFace
caches name files by content, so the check is a `stat` rather than a scan and a
local copy becomes the highest-priority source. One repository may hold twenty
quantisations of the same weights, so `hf://org/repo@revision#quant` selects
among them, refuses to guess when the choice is ambiguous, and pins to the
resolved commit rather than to a branch that can move.

## Words

| word | meaning |
|---|---|
| **reference** | `hf://org/repo[@revision][/file][#quant]` or `ollama://name[:tag]` |
| **registry** | something that resolves a reference to a digest, a size and sources; HuggingFace and Ollama are supplied |
| **quant** | one quantisation of the same weights: one model for routing, two files for storage |
| **family** | the name under which several hosts hold one model; every token that is not known packaging is kept |
| **local hit** | a copy already on this machine, found by digest; a candidate, not an answer, because Ollama does not verify its own blob digests |

No rule on this page carries a tag, and no conformance scenario cites this
layer.

## Obtain

- **Go.** `go get github.com/openabstractions/abstraction-model/go`, and
  `go install github.com/openabstractions/abstraction-model/go/cmd/modelget@latest`
  for the command. The module path ends in `/go`; the package is `model`, so
  import it with an explicit alias. The newest tag is `go/v0.1.0`; `@main` is
  the tree as it stands.
- **Python.** Not on any index. `python/pyproject.toml` builds a wheel
  (`pip wheel python/`) of `abstraction_model`; `resident.py` imports
  `abstraction_job` from
  [abstraction-job](https://github.com/openabstractions/abstraction-job).
- **C++.** None.

## Example

Parsing is offline; resolving contacts the registry.

```go
package main

import (
	"context"
	"fmt"

	model "github.com/openabstractions/abstraction-model/go"
)

func main() {
	ref, err := model.ParseRef("hf://bartowski/Qwen2.5-0.5B-Instruct-GGUF#IQ2_M")
	if err != nil {
		panic(err)
	}
	fmt.Println(ref.Registry, ref.Repo, ref.Quant)
	fmt.Println(ref.String())

	// Contacts huggingface.co. Resolve returns an error rather than a spec
	// when the registry publishes no digest for the chosen file.
	spec, err := model.Default().Resolve(context.Background(), ref.String())
	if err != nil {
		panic(err)
	}
	fmt.Println("digest:", spec.Artifact.Digest)
	fmt.Println("size:", spec.Artifact.Size)
	for _, s := range spec.Sources {
		fmt.Printf("source: %s priority %d\n", s.Scheme, s.Priority)
	}
}
```

Output, on a machine that does not already hold these bytes:

```
hf bartowski/Qwen2.5-0.5B-Instruct-GGUF IQ2_M
hf://bartowski/Qwen2.5-0.5B-Instruct-GGUF#IQ2_M
digest: sha256:2fc237e65e1f963310e9c961d8e71e932734a72f90e3216a972d83edd1feb756
size: 328597408
source: https priority 0
```

A `file` source at priority 0 appears ahead of `https` when a local store
already holds the digest.

### Command line

```bash
modelget get <ref> -o <dir|file> [--background]
modelget list      # what is in flight, and what finished
modelget resume    # pick up everything interrupted
modelget where <ref>
```

`modelget where` prints the digest, the size, and every place on this machine
that already holds those bytes. Where a transfer happens is not an argument:
`modelget` hands the job to whatever is configured and reachable, best first.

Environment: `MODELGET_STORE` or `ABSTRACTION_STORE` for where jobs live
(default `~/.abstraction`), `ABSTRACTION_NAS_STORE` for a job store on a share
that something else is watching, `OLLAMA_MODELS` for a relocated Ollama
directory, and `ABSTRACTION_CRED_HF` — or `HF_TOKEN` — for gated HuggingFace
repositories. The token is read at request time and is not written into the job
record; the record names the credential as `"credential": "hf"`.

## API overview

`ParseRef(string) (Ref, error)` reads `hf://org/repo[@revision][/file][#quant]`
and `ollama://name[:tag]`. `Ref` has `Registry`, `Repo`, `Revision`, `Quant`,
`File` and `String()`. An explicit `File` wins over a `Quant`.

`Resolver` is the interface a registry implements: `Registry() string` and
`Resolve(ctx, Ref) (download.Spec, error)`. `HF{}` and `Ollama{}` are supplied;
`HFCredential` is the credential name `HF` requests. `NewRegistry(resolvers...)`
and `Default()` build a `*Registry`, with `Add` and `SetLocal`.
`Registry.Resolve(ctx, refString)` parses, resolves, rejects an empty digest,
and adds local copies as sources.

`NewLocal()` searches the content-addressed stores discovered on this machine;
`NewLocalIn(*storage.Stores)` searches ones you supply. `Local.Find(digest)
[]Hit` lists every copy; `Local.Augment(spec)` inserts them as priority-0 `file`
sources without duplicating a location already in the spec. `SameVolume(a, b
string) bool` reports whether two paths are on one volume.

`Get(ctx, reg, store, refString, dest, requires...) (string, download.Spec,
error)` resolves and submits a download job, returning a job id that outlives
the calling process.

A local hit is a candidate, not an answer: Ollama does not verify its own blob
digests, so whatever copies from a local store still verifies, and that check
belongs to
[abstraction-download](https://github.com/openabstractions/abstraction-download).

## Today

Experimental, version 0.1.0. Two registries, no adopter outside this
organisation, no continuous integration.

- **Go**: the registry, the local lookup and `modelget`. The 12 tests run
  offline against fixtures and a local HTTP server; nothing here exercises a
  real registry automatically.
- **Python**: `abstraction_model.py` answers whether two names from two hosts
  mean one model; `resident.py` is a broker that routes two applications to one
  loaded copy, with `install.ps1` for running it at every logon.
- **Multi-file artifacts are not supported.** Sharded safetensors, diffusers
  directories and ONNX with external data are one artifact in several files, and
  a spec describes one file with one digest.
- **Only `.gguf` is auto-selected.** Name a file explicitly for anything else.
- **Only HuggingFace and Ollama**, and `Ollama` resolves from the local store
  only; it does not pull from the Ollama registry.
- **Credentials come from the environment**, which is not a strong store; on
  Windows this should use Credential Manager. Anything resuming a job needs the
  credential in its own environment, which a process started by a scheduler does
  not inherit from a shell.
- **`resume` waits immediately after a crash**, because the dead process still
  holds a time-limited claim on the job and nothing else may write to it until
  that claim expires.
- A store that publishes no digest cannot be matched against at all.

## Conformance

Go and Python are held to
[`testdata/identifiers.tsv`](testdata/identifiers.tsv) — identifiers read
from four running services — byte for byte by
[`identity-conformance.sh`](https://github.com/openabstractions/abstractions/blob/main/scripts/identity-conformance.sh):
a routing decision made from a name is only safe if every host-facing process
draws the same boundary. Nothing else on this page is checked across
languages.

## Where it sits

Below: [abstraction-download](https://github.com/openabstractions/abstraction-download)
moves the bytes, [abstraction-job](https://github.com/openabstractions/abstraction-job)
keeps the work, [abstraction-storage](https://github.com/openabstractions/abstraction-storage)
finds what is already here. Above: nothing of ours imports this layer; the
vocabulary below it contains no AI word.

One layer of [openabstractions](https://github.com/openabstractions/abstractions).
Every layer names one thing local tools rebuild on their own; the name means the
same in each language that implements it, and the conformance scenarios are what
hold an implementation to it.

## Requirements

Go 1.26 or newer. Depends on three other layers, all resolved by the Go module
system. Network access is needed to resolve a HuggingFace reference. Tested on
Windows and Linux.

## Licence

Apache-2.0. See [LICENSE](LICENSE).
