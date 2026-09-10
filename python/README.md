# abstraction-model, in Python

Whether two strings from two hosts name one model. Hugging Face, Ollama, LM
Studio and a directory on disk each write the same weights a different way, so an
application that has already downloaded a model cannot tell that the one it is
about to fetch is the same file.

Every token that is not known packaging is kept. The reverse rule — a list of
noise to strip — collapses a fine-tune onto the model it was tuned from, and then
a request for one is answered by the other. One module, standard library only,
importing nothing of ours.

This page is the Python package. The Go implementation, the store side and what
is `UNPROVEN` are on
[the repository](https://github.com/openabstractions/abstraction-model).

## Install

Not on PyPI, and the name on PyPI is not ours.

    git clone https://github.com/openabstractions/abstraction-model
    pip install ./abstraction-model/python

Python 3.9 or later. `abstraction_model.py` is one file with no imports of ours,
so copying it into a `_vendor/` directory of your own is an equally complete
installation.

## An example that runs

```python
import abstraction_model as model

a = "bartowski/Qwen2.5-0.5B-Instruct-GGUF/Qwen2.5-0.5B-Instruct-IQ2_M.gguf"
b = "qwen2.5:0.5b-instruct-q4_K_M"

print(model.parse(a))
print(model.parse(b))
print("same model:", model.same(a, b))
print("same file: ", model.parse(a) == model.parse(b))
```

Two quantisations of one model are the same model and are still two files on
disk, and that is the distinction the layer exists to make: `same` answers the
routing question, equality of the parsed `Name` answers the storage one.

## What an application calls

| call | what it does |
|---|---|
| `parse(s)` | a `Name` with `family` and `quant` |
| `family(s)` | the family alone: what survives packaging, host prefixes and file extensions |
| `same(a, b)` | whether two host identifiers name one model for routing |
| `shard(name)` | the shard a multi-file model's part belongs to, when the name says |

## What may break

- **This module answers about names and nothing else.** It does not download,
  does not hash and never touches a store.
- **The algorithm is a judgment, not a standard.** It is compared against the Go
  implementation over a shared corpus of identifiers, so the two agree; there is
  no third party to be right against.
- **No conformance verdict.** No scenario in the suite cites this layer yet —
  [what is proven and what is not](https://openabstractions.org/coverage.html).
- **Not on any package index**, and no release carries an API stability promise.
  Pin a commit you have read.

Apache-2.0. See [LICENSE](LICENSE).
