// Package identity answers whether two strings from two hosts name one model.
//
// Every token that is not known packaging is kept. The reverse rule — a list of
// noise to strip — collapses a fine-tune onto the model it was tuned from, and
// then a request for one is answered by the other.
package identity

import "strings"

type Name struct {
	Family string
	Quant  string
}

func Parse(s string) Name {
	var toks []string
	var quant string
	for _, p := range parts(s) {
		t, q := tokens(p)
		if q != "" {
			quant = q
		}
		toks = append(toks, t...)
	}
	return assemble(toks, quant)
}

func Family(s string) string { return Parse(s).Family }

// Same reports whether two host identifiers name one model for routing. Two
// quantisations of one model are Same and are still two files on disk.
func Same(a, b string) bool {
	fa := Family(a)
	return fa != "" && fa == Family(b)
}

func parts(s string) []string {
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if r, ok := strings.CutPrefix(s, "models--"); ok {
		s = strings.ReplaceAll(r, "--", "/")
	}
	var out []string
	for _, seg := range withoutPublisher(strings.Split(s, "/")) {
		seg, _, _ = strings.Cut(seg, "@")
		for _, p := range strings.FieldsFunc(seg, func(r rune) bool { return r == ':' || r == '#' }) {
			if p = strings.TrimSuffix(p, extension(p)); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// withoutPublisher drops the account a file was published under. unsloth's GGUF
// of qwen's release is qwen's weights, so the publisher cannot be part of the
// identity — what makes a fine-tune distinct is in the name, not the account.
func withoutPublisher(segs []string) []string {
	if len(segs) > 1 {
		return segs[1:]
	}
	return segs
}

func extension(p string) string {
	i := strings.LastIndexByte(p, '.')
	if i < 0 {
		return ""
	}
	if extensions[p[i:]] {
		return p[i:]
	}
	return ""
}

func tokens(p string) ([]string, string) {
	raw := strings.FieldsFunc(p, func(r rune) bool {
		return r == '-' || r == '_' || r == ' ' || r == '+'
	})
	var kept, quant []string
	for i := 0; i < len(raw); i++ {
		if shardRun(raw, i) {
			i += 2
			continue
		}
		run := quantRun(raw, i)
		if run == 0 {
			kept = append(kept, raw[i])
			continue
		}
		if quant == nil {
			quant = raw[i : i+run]
		}
		i += run - 1
	}
	return kept, strings.Join(quant, "_")
}

// Shard reports whether a filename is one piece of a set that is one artifact —
// "…-00002-of-00003.gguf". The pieces share a family, so anything counting
// copies has to count sets rather than files.
func Shard(name string) (index, count int, ok bool) {
	for _, p := range parts(name) {
		raw := strings.FieldsFunc(p, func(r rune) bool {
			return r == '-' || r == '_' || r == ' ' || r == '+'
		})
		for i := range raw {
			if shardRun(raw, i) {
				return digits(raw[i]), digits(raw[i+2]), true
			}
		}
	}
	return 0, 0, false
}

func shardRun(raw []string, i int) bool {
	return i+2 < len(raw) && raw[i+1] == "of" && digits(raw[i]) > 0 && digits(raw[i+2]) > 0
}

func digits(t string) int {
	if t == "" {
		return 0
	}
	n := 0
	for _, r := range t {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func quantRun(raw []string, i int) int {
	n := 0
	if raw[i] == "ud" && i+1 < len(raw) && quantRun(raw, i+1) > 0 {
		n = 1
	}
	t := raw[i+n]
	switch {
	case quantWord[t]:
		n++
		if (t == "fp8" || t == "f8" || t == "fp4") && i+n < len(raw) && isBlockFormat(raw[i+n]) {
			n++
		}
	case isQuantLevel(t):
		n++
		for i+n < len(raw) && quantTail[raw[i+n]] {
			n++
		}
	default:
		return 0
	}
	return n
}

func assemble(toks []string, quant string) Name {
	var kept, roles []string
	isRole := map[string]bool{}
	for _, t := range toks {
		switch {
		case packaging[t]:
		case role[t]:
			if !isRole[t] {
				isRole[t] = true
				roles = append(roles, t)
			}
		default:
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return Name{Family: strings.Join(roles, "-"), Quant: quant}
	}
	base, rest := kept[0], kept[1:]
	seen := map[string]bool{base: true}
	if len(rest) > 0 && isVersion(rest[0]) {
		seen[rest[0]] = true
		base, rest = base+rest[0], rest[1:]
		seen[base] = true
	}
	var params, variants []string
	for _, t := range rest {
		if seen[t] {
			continue
		}
		seen[t] = true
		if isParams(t) {
			params = append(params, t)
		} else {
			variants = append(variants, t)
		}
	}
	parts := append(append(append([]string{base}, params...), variants...), roles...)
	return Name{Family: strings.Join(parts, "-"), Quant: quant}
}

func isVersion(t string) bool {
	t = strings.TrimPrefix(t, "v")
	if t == "" {
		return false
	}
	for _, r := range t {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return t[0] >= '0' && t[0] <= '9'
}

func isParams(t string) bool {
	if t == "" {
		return false
	}
	switch t[len(t)-1] {
	case 'b', 'm', 'k':
	default:
		return false
	}
	body := t[:len(t)-1]
	body = strings.TrimPrefix(body, "a")
	if body == "" {
		return false
	}
	digits, dots, xs := 0, 0, 0
	for _, r := range body {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.':
			dots++
		case r == 'x':
			xs++
		default:
			return false
		}
	}
	return digits > 0 && dots <= 1 && xs <= 1
}

func isQuantLevel(t string) bool {
	t = strings.TrimPrefix(t, "i")
	if len(t) < 2 || t[0] != 'q' {
		return false
	}
	for _, r := range t[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isBlockFormat(t string) bool {
	if len(t) < 4 || t[0] != 'e' {
		return false
	}
	return strings.ContainsRune(t, 'm')
}

var extensions = map[string]bool{
	".gguf": true, ".safetensors": true, ".sft": true, ".bin": true, ".pt": true,
	".pt2": true, ".pth": true, ".ckpt": true, ".onnx": true, ".npz": true, ".pkl": true,
}

var quantWord = map[string]bool{
	"bf16": true, "f16": true, "fp16": true, "f32": true, "fp32": true, "bf32": true,
	"f8": true, "fp8": true, "fp6": true, "fp4": true, "mxfp4": true, "nvfp4": true,
	"rocmfp4": true, "int2": true, "int4": true, "int8": true, "awq": true, "gptq": true,
	"2bit": true, "3bit": true, "4bit": true, "6bit": true, "8bit": true,
}

var quantTail = map[string]bool{
	"0": true, "1": true, "k": true, "s": true, "m": true, "l": true,
	"xs": true, "xl": true, "xxs": true, "xxl": true, "nl": true,
}

var packaging = map[string]bool{
	"gguf": true, "ggml": true, "safetensors": true, "safetensor": true, "onnx": true,
	"mlx": true, "pytorch": true, "hf": true, "model": true, "models": true,
	"weights": true, "snapshot": true, "snapshots": true, "blobs": true, "main": true,
	"latest": true, "mtp": true, "scaled": true, "fast": true, "ud": true,
	"moe": true, "flm": true, "oga": true, "ort": true, "npu": true, "cpu": true,
	"gpu": true, "cuda": true, "rocm": true, "hip": true, "vulkan": true, "dml": true,
	"hybrid": true, "llamacpp": true, "ryzenai": true,

	// Ollama never writes "instruct" and everything it serves is tuned, so
	// instruction tuning is the default and a base model is the marked case.
	"it": true, "instruct": true, "chat": true,
}

// A role names a distinct artifact beside the weights, and hosts write it at
// either end of the name — "mmproj-Qwen3.6-…" and "…-Qwen3.6-mmproj" are one
// thing, so a role never becomes the base.
var role = map[string]bool{
	"mmproj": true, "vae": true, "lora": true, "adapter": true,
	"projector": true, "clip": true, "draft": true,
}
