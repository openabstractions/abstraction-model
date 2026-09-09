"""Whether two strings from two hosts name one model.

Every token that is not known packaging is kept. The reverse rule — a list of
noise to strip — collapses a fine-tune onto the model it was tuned from, and
then a request for one is answered by the other.

The Go package at model/go/identity is the same algorithm; the two are compared
over model/testdata/identifiers.tsv by scripts/identity-conformance.sh.
"""

EXTENSIONS = {".gguf", ".safetensors", ".sft", ".bin", ".pt", ".pt2", ".pth",
              ".ckpt", ".onnx", ".npz", ".pkl"}

QUANT_WORD = {"bf16", "f16", "fp16", "f32", "fp32", "bf32", "f8", "fp8", "fp6",
              "fp4", "mxfp4", "nvfp4", "rocmfp4", "int2", "int4", "int8", "awq",
              "gptq", "2bit", "3bit", "4bit", "6bit", "8bit"}

QUANT_TAIL = {"0", "1", "k", "s", "m", "l", "xs", "xl", "xxs", "xxl", "nl"}

PACKAGING = {
    "gguf", "ggml", "safetensors", "safetensor", "onnx", "mlx", "pytorch", "hf",
    "model", "models", "weights", "snapshot", "snapshots", "blobs", "main",
    "latest", "mtp", "scaled", "fast", "ud", "moe", "flm", "oga",
    "ort", "npu", "cpu", "gpu", "cuda", "rocm", "hip", "vulkan", "dml",
    "hybrid", "llamacpp", "ryzenai",
    # Ollama never writes "instruct" and everything it serves is tuned, so
    # instruction tuning is the default and a base model is the marked case.
    "it", "instruct", "chat",
}

# A role names a distinct artifact beside the weights, and hosts write it at
# either end of the name — "mmproj-Qwen3.6-…" and "…-Qwen3.6-mmproj" are one
# thing, so a role never becomes the base.
ROLE = {"mmproj", "vae", "lora", "adapter", "projector", "clip", "draft"}

SPLIT = "-_ +"


class Name:
    __slots__ = ("family", "quant")

    def __init__(self, family, quant):
        self.family, self.quant = family, quant

    def __eq__(self, other):
        return (self.family, self.quant) == (other.family, other.quant)

    def __repr__(self):
        return "Name(%r, %r)" % (self.family, self.quant)


def parse(s):
    toks, quant = [], ""
    for p in _parts(s):
        t, q = _tokens(p)
        if q:
            quant = q
        toks += t
    return _assemble(toks, quant)


def family(s):
    return parse(s).family


def same(a, b):
    """Whether two host identifiers name one model for routing. Two
    quantisations of one model are the same and are still two files on disk."""
    fa = family(a)
    return bool(fa) and fa == family(b)


def _parts(s):
    i = s.find("://")
    if i >= 0:
        s = s[i + 3:]
    s = s.strip().lower()
    if s.startswith("models--"):
        s = s[len("models--"):].replace("--", "/")
    segs = s.split("/")
    if len(segs) > 1:
        segs = segs[1:]
    out = []
    for seg in segs:
        seg = seg.split("@", 1)[0]
        for p in seg.replace("#", ":").split(":"):
            p = p[:len(p) - len(_extension(p))] if _extension(p) else p
            if p:
                out.append(p)
    return out


def _extension(p):
    i = p.rfind(".")
    if i >= 0 and p[i:] in EXTENSIONS:
        return p[i:]
    return ""


def _tokens(p):
    for c in SPLIT[1:]:
        p = p.replace(c, "-")
    raw = [t for t in p.split("-") if t]
    kept, quant, i = [], None, 0
    while i < len(raw):
        if _shard_run(raw, i):
            i += 3
            continue
        run = _quant_run(raw, i)
        if run == 0:
            kept.append(raw[i])
            i += 1
            continue
        if quant is None:
            quant = raw[i:i + run]
        i += run
    return kept, "_".join(quant or [])


def shard(name):
    """Whether a filename is one piece of a set that is one artifact —
    "…-00002-of-00003.gguf". The pieces share a family, so anything counting
    copies has to count sets rather than files. Returns (index, count) or None."""
    for p in _parts(name):
        for c in SPLIT[1:]:
            p = p.replace(c, "-")
        raw = [t for t in p.split("-") if t]
        for i in range(len(raw)):
            if _shard_run(raw, i):
                return int(raw[i]), int(raw[i + 2])
    return None


def _shard_run(raw, i):
    return (i + 2 < len(raw) and raw[i + 1] == "of"
            and _digits(raw[i]) > 0 and _digits(raw[i + 2]) > 0)


def _digits(t):
    return int(t) if t.isdigit() else 0


def _quant_run(raw, i):
    n = 0
    if raw[i] == "ud" and i + 1 < len(raw) and _quant_run(raw, i + 1) > 0:
        n = 1
    t = raw[i + n]
    if t in QUANT_WORD:
        n += 1
        if t in ("fp8", "f8", "fp4") and i + n < len(raw) and _is_block_format(raw[i + n]):
            n += 1
    elif _is_quant_level(t):
        n += 1
        while i + n < len(raw) and raw[i + n] in QUANT_TAIL:
            n += 1
    else:
        return 0
    return n


def _assemble(toks, quant):
    kept, roles = [], []
    for t in toks:
        if t in PACKAGING:
            continue
        if t in ROLE:
            if t not in roles:
                roles.append(t)
        else:
            kept.append(t)
    if not kept:
        return Name("-".join(roles), quant)
    base, rest = kept[0], kept[1:]
    seen = {base}
    if rest and _is_version(rest[0]):
        seen.add(rest[0])
        base, rest = base + rest[0], rest[1:]
        seen.add(base)
    params, variants = [], []
    for t in rest:
        if t in seen:
            continue
        seen.add(t)
        (params if _is_params(t) else variants).append(t)
    return Name("-".join([base] + params + variants + roles), quant)


def _is_version(t):
    t = t[1:] if t.startswith("v") else t
    return bool(t) and t[0].isdigit() and all(c.isdigit() or c == "." for c in t)


def _is_params(t):
    if not t or t[-1] not in "bmk":
        return False
    body = t[:-1]
    body = body[1:] if body.startswith("a") else body
    if not body:
        return False
    digits = sum(c.isdigit() for c in body)
    return (digits > 0 and body.count(".") <= 1 and body.count("x") <= 1
            and all(c.isdigit() or c in ".x" for c in body))


def _is_quant_level(t):
    t = t[1:] if t.startswith("i") else t
    return len(t) >= 2 and t[0] == "q" and t[1:].isdigit()


def _is_block_format(t):
    return len(t) >= 4 and t[0] == "e" and "m" in t
