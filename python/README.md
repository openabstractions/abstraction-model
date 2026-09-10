# model — pull weights without losing them, and without fetching what you already have

```
$ modelget where ollama://qwen2.5:0.5b
digest  sha256:c5396e06af294bd101b30dce59131a76d2b773e76950acc870eda801d3ab0515
size    379.4 MiB
local   %USERPROFILE%\.ollama\models\blobs\sha256-c5396e06...  (ollama)

these exact bytes are already on this machine 1 time(s).
fetching this would cost nothing but a copy.
```

This is the AI-specific layer. Below it, [`download`](../download/README.md) moves
bytes and refuses anything whose digest is wrong; below that,
[`job`](../job/README.md) makes the work outlive the process that asked for it.
This layer knows the few things that are specific to model weights.

## Is that the same model as this one?

```
$ modelget identify Qwen3.6-35B-A3B-MTP-GGUF qwen/qwen3.6-35b-a3b
qwen3.6-35b-a3b   Qwen3.6-35B-A3B-MTP-GGUF
qwen3.6-35b-a3b   qwen/qwen3.6-35b-a3b

same model. one of these can answer for both.
```

Four hosts on one machine name one model four ways, and none of them can tell
that the other three are holding it. Two applications each pointed at their own
host loaded the same weights twice and cost 18.3 GB of GPU memory. Answering
that question is what `identity` is for, and it is a package with no imports
outside the standard library so that a router can take it without taking the
download stack.

```go
identity.Family("unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf")
// "qwen3.6-35b-a3b"
identity.Same("Qwen3.6-35B-A3B-MTP-GGUF", "qwen/qwen3.6-35b-a3b")  // true
```

**Every token that is not known packaging is kept.** The rule everyone reaches
for first is a list of noise to strip, and it is wrong in the direction that
matters: it collapses `gemma-4-26b-a4b-it-ultra-uncensored-heretic` onto
`Gemma-4-26B-A4B-it`, and then a request for Google's release is answered by an
abliterated fine-tune. Over-splitting costs a missed saving; over-merging
answers with the wrong model.

**Quantisation is not identity, and it is not nothing either.** The same weights
at `Q4_K_M` and at `UD-Q4_K_XL` are one model for routing and two files for
storage — one `Family`, two `Quant`s, and 275.8 GB across four silos on the
machine this was written against contains zero byte-identical duplicates.

The 42 identifiers in [`testdata/identifiers.tsv`](testdata/identifiers.tsv)
were read from the four running services, and Go and Python are held to them
byte-for-byte by `scripts/identity-conformance.sh`. Python is in
[`python/`](python/) because the consumers are a broker and a ComfyUI node.

## One copy, two applications

```
$ python python/resident.py            # a broker and a window on :11800
$ powershell -File python/install.ps1  # the same, at every logon
```

Point both applications at `http://127.0.0.1:11800/v1` and the second one does
not load a second copy. The broker polls Lemonade, LM Studio and Ollama, groups
what each of them holds by `family`, and on a request either hands it to a host
already holding that family or picks a host that has the weights on disk and
loads it there. Placement is serialised per family, so two applications arriving
together wait on one load instead of starting two.

Measured on the owner's laptop: two applications wanting one 35B model cost
**44.11 GB in two copies and 25.77 GB in one**, and both answered faster
(61.6 s → 48.6 s). Warm, the second application added **zero bytes**. Full
numbers, and what the hardware permits, in
[`research/residency-strix-halo/MEASURED.txt`](../research/residency-strix-halo/MEASURED.txt).

The window at `/` names what is resident, who holds the GPU, and every routing
decision. `GET /findings` emits the same document in `polite-monitor`'s shape.
**The panel that matters is "loaded more than once"**: identity is a judgement
about names, it will be wrong eventually, and when it is wrong the machine
quietly holds two copies. That failure now has a name on a screen rather than
only a number in a file — and a remedy. Every resident copy is a `resident` job
in `~/.abstraction`, held by the broker on that host's behalf; a doubled family
gets the copy routing would not use **recalled**, and the broker honours its own
recall by unloading it (Lemonade `/api/v1/unload`, Ollama `keep_alive: 0`,
LM Studio `/api/v1/models/unload`). A host that will not unload keeps its copy
and loses its traffic: the lease lapses, the copy is evicted from routing, and
the host's own idle timeout finishes the job. The same recall is open to anyone
holding the store — `jobctl recall <id> --epoch N --reason "make room"` unloads a
model within one poll. Contract in [`job/README.md`](../job/README.md).

When it is wrong in the direction of splitting, `~\.abstraction\model-merges`
overrides it without a code change — one `wrong-key canonical-key` per line,
re-read on every poll. There is deliberately no override in the other direction:
a person can say two things are one model, and nothing can make the parser
answer a request with weights it was not asked for.

`adopters/comfyui-resident/` is a ComfyUI node that asks the broker instead of
loading anything; copy it into `custom_nodes/` and restart.

## What is already here, by family

```
$ modelget inventory
qwen3.6-35b-a3b
  huggingface      22.4 GB  ud_q4_k_xl  no published digest  unsloth/Qwen3.6-35B-A3B-GGUF/…
  huggingface      22.9 GB  ud_q4_k_xl  no published digest  unsloth/Qwen3.6-35B-A3B-MTP-GGUF/…
  lmstudio         21.2 GB  q4_k_m      no published digest  lmstudio-community/…
qwen2.5-0.5b
  huggingface       0.4 GB  q4_k_m      no published digest  bartowski/Qwen2.5-0.5B-Instruct-GGUF/…
  ollama            0.4 GB  -           sha256:c5396e0…      library/qwen2.5:0.5b
```

`ABSTRACTION_MODEL_ROOTS` adds directories for stores with no fixed home;
ComfyUI's models tree is one.

**"no published digest" is the common case and this says so.** Of the four
stores, only Ollama names a file by its content. The HuggingFace cache on the
machine measured has no `blobs/` directory at all — whatever wrote it put the
files straight into the snapshot — so the "a stat, not a scan" claim below holds
for Ollama and for a cache `huggingface_hub` wrote, and for nothing else here.
Numbers in [`research/model-identity/MEASURED.txt`](../research/model-identity/MEASURED.txt).

**Known limit.** A speculative-decoding draft head shipped as its own file
(`mtp-gemma-4-26B-A4B-it.gguf`) lands in its parent's family, because `mtp` has
to be dropped for `Qwen3.6-35B-A3B-MTP-GGUF` to meet `qwen/qwen3.6-35b-a3b` and
this parser does not care where in a name a token sits. `mmproj` and `vae` are
handled, as roles that never become the base.

## The three things it knows

**The digest is published, not supplied.** Nobody asking for a model knows its
hash. HuggingFace publishes `lfs.sha256`; Ollama names its blobs by digest. This
layer's job is to obtain that number *before a byte moves*, so the layer below
has something to refuse against. When a registry will not say, this refuses:

```
hf: model.gguf in org/repo@main has no lfs.sha256
    — refusing to fetch bytes nobody can check
```

**A model reference is not a URL.** `hf://org/repo@revision#quant` selects one
file from a repo that may hold twenty quantisations of the same weights. It
refuses to guess between them, because picking "the first" would make one
reference mean different bytes depending on what order an API returned. It also
pins to the **resolved commit** rather than the branch you named — a branch
moves, and a job that resumes next week must fetch the bytes it started on.

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

So once a registry has told us the digest, finding out whether those exact bytes
are already on disk is a **stat, not a scan** — no hashing of 116 GB. A hit
becomes a `file:` source at priority 0, and because `download.Runner` already
tries sources in priority order, cross-store dedup falls out with **no special
case anywhere**. A model Ollama pulled does not get downloaded again for
something else; it gets copied and verified.

LM Studio's absence from that table is the argument in miniature: it is the
largest store here at 44 GB and it publishes no digest, so nothing can tell
whether its bytes are anyone else's.

**A local hit is a candidate, not an answer.** Ollama never checks its own
digests — G3 corrupted a blob in place and Ollama listed it, loaded it and served
confident nonsense while the filename still claimed the original hash. So the
layer below verifies whatever it copies, and there is a test for exactly that.

## Use it

```bash
modelget get hf://bartowski/Qwen2.5-0.5B-Instruct-GGUF#IQ2_M -o D:/models/
modelget get <ref> --here         # do it in this process, whatever else exists
modelget list                     # what is in flight, and what finished
modelget resume                   # pick up everything interrupted
modelget where <ref>              # digest, size, and where it can be had
```

**Where the download happens is not an argument.** There used to be a `--service`
flag, and briefly a `--submit` one, and both were the wrong shape: somebody
asking for a model wants the model, and which tier fetches it is a property of
the machine. `modelget` registers whatever is configured and reachable, best
first, and hands the job to the first thing that will take it — a NAS if
`ABSTRACTION_NAS_STORE` points at a store something is watching, else the OS
transfer service, else this process.

```
$ export ABSTRACTION_NAS_STORE='//nas.invalid/home/abstraction-store'
$ modelget get hf://bartowski/Qwen2.5-0.5B-Instruct-GGUF#IQ2_M -o D:/models/
handed to the NAS. It continues if this process exits,
or the machine sleeps. Check on it with: modelget list
```

Nothing in that command mentions a NAS. Unset the variable and it uses BITS; the
command does not change. See [`deploy/nas`](../deploy/nas/README.md).

## The complaint this answers, tested

> you start a 40 GB download, you close the laptop, and it starts again from zero

```bash
bash scripts/kill-and-resume.sh
```

A **real** 313 MB model from HuggingFace, killed with `SIGKILL` partway through —
no graceful shutdown, which is what closing a lid looks like to a process. A
separate invocation finds the job, resumes from the byte the dead one had
*proven*, and delivers a file whose sha256 matches what HuggingFace published.

Transcript: [`docs/results/RESUME1.txt`](../docs/results/RESUME1.txt).

```
bytes on disk:   61163581
bytes PROVEN:    52824170
```

That gap is the design. The dead process wrote more than it had checkpointed,
nothing vouches for the difference, so the resume starts from the proven number
and discards the rest.

Two things that test found, both now fixed:

- **Checkpointing was byte-triggered only.** At 8 MiB intervals, a download
  killed after 12 seconds on a slow link had saved *nothing*. Ten minutes on a
  bad connection would also have saved nothing — and a slow connection is exactly
  when resuming matters. Now it checkpoints on bytes **or** elapsed time.
- **The lease could expire mid-transfer.** Renewal rode the same callback, so a
  slowly-progressing download would be adopted as an orphan while still running.

## Honest limits

- **Immediately after a crash, `resume` waits.** The dead owner's lease has not
  lapsed, and nothing may write to a job another owner might still hold. It now
  says so, with a countdown, instead of "nothing to resume". Since the owner
  string carries host and PID, a local liveness check could break the lease at
  once rather than waiting — worth doing, not done.
- **Multi-file artifacts are not supported.** Sharded safetensors, diffusers
  directories and ONNX-with-external-data are one artifact in several files, and
  a Spec describes one file with one digest. Ollama's manifest-of-layers is the
  shape to copy.
- **Credentials are referenced, not stored** — fixed, and there is a regression
  test that reads the bytes on disk. The record holds `"credential": "hf"`; the
  secret comes from `$ABSTRACTION_CRED_HF` at the moment of the request
  (`HF_TOKEN` is accepted as an alias). It used to inline
  `Authorization: Bearer <token>` into the source attrs, which are written to
  the record verbatim — a record that is deliberately readable by every other
  process, and that gets pasted into transcripts.
  The environment is not a strong store; on Windows this should use Credential
  Manager, which is the same "use the OS's own mechanism" call as BITS and the
  scheduled task. That is not done.
- **Anything resuming a job needs the credential in its own environment.** A
  supervisor started by the scheduler does not inherit your shell.
- **Only `.gguf` is auto-selected.** Name a file explicitly for anything else.

## Tested

```bash
cd model/go && go test ./...
```

11 tests: reference parsing, revision pinning, refusing a file with no published
digest, refusing to guess between quantisations, rejecting path-traversing
filenames from a remote API, resolving Ollama from its local store with no
network, the local-store lookup, not offering the same file twice, an end-to-end
run that uses a local copy while the "remote" is unreachable, and a corrupt local
copy being caught despite a filename that claims otherwise.
