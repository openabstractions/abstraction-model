package model

import (
	"fmt"
	"strings"
)

// Ref names a model. It is not a URL, for the same reason a download source is
// not a URL: one reference can be satisfied from several places, and the
// reference itself has structure a URL cannot carry.
//
//	hf://bartowski/Qwen2.5-0.5B-Instruct-GGUF          latest revision, any gguf
//	hf://bartowski/Qwen2.5-0.5B-Instruct-GGUF#Q4_K_M   pick a quantisation
//	hf://org/repo@41ba88dbac95fed2528c92514c131d73eb5a174b   pin a revision
//	ollama://qwen2.5:0.5b                              a model already pulled
//
// Pinning matters more than it looks. C3 recorded llama.cpp writing a file whose
// size matched nothing at `main` in the repo it claimed to come from — a moving
// tag and a filename are not an identity, which is the whole reason this layer
// insists on a digest before transferring anything.
type Ref struct {
	Registry string // "hf", "ollama"
	Repo     string // "org/name" for hf, "name" for ollama
	Revision string // git revision for hf, tag for ollama; empty means default
	Quant    string // "Q4_K_M"; hf only; empty means any
	File     string // an explicit filename, when the caller knows it
}

func (r Ref) String() string {
	switch r.Registry {
	case "hf":
		s := "hf://" + r.Repo
		if r.Revision != "" {
			s += "@" + r.Revision
		}
		if r.File != "" {
			s += "/" + r.File
		} else if r.Quant != "" {
			s += "#" + r.Quant
		}
		return s
	case "ollama":
		s := "ollama://" + r.Repo
		if r.Revision != "" {
			s += ":" + r.Revision
		}
		return s
	}
	return r.Repo
}

// ParseRef reads a model reference.
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	scheme, rest, ok := strings.Cut(s, "://")
	if !ok {
		return Ref{}, fmt.Errorf("model: %q has no scheme; try hf://org/repo or ollama://name:tag", s)
	}
	switch scheme {
	case "hf":
		return parseHF(rest)
	case "ollama":
		return parseOllama(rest)
	default:
		return Ref{}, fmt.Errorf("model: unsupported scheme %q", scheme)
	}
}

// parseHF reads org/repo[@revision][/file.gguf | #quant].
//
// The file form wins over the quant form when both could apply, because naming a
// file is the caller being specific and a quant is the caller being approximate.
func parseHF(rest string) (Ref, error) {
	r := Ref{Registry: "hf"}

	if i := strings.IndexByte(rest, '#'); i >= 0 {
		r.Quant = rest[i+1:]
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '@'); i >= 0 {
		after := rest[i+1:]
		rest = rest[:i]
		// A revision may be followed by an explicit file path.
		if j := strings.IndexByte(after, '/'); j >= 0 {
			r.Revision, r.File = after[:j], after[j+1:]
		} else {
			r.Revision = after
		}
	}

	parts := strings.Split(rest, "/")
	switch {
	case len(parts) < 2:
		return Ref{}, fmt.Errorf("model: hf reference %q needs org/repo", rest)
	case len(parts) == 2:
		r.Repo = parts[0] + "/" + parts[1]
	default:
		// org/repo/some/file.gguf — anything past the second segment is a path
		// inside the repo.
		r.Repo = parts[0] + "/" + parts[1]
		if r.File == "" {
			r.File = strings.Join(parts[2:], "/")
		}
	}
	if r.Repo == "/" || strings.HasPrefix(r.Repo, "/") || strings.HasSuffix(r.Repo, "/") {
		return Ref{}, fmt.Errorf("model: hf reference %q needs org/repo", rest)
	}
	return r, nil
}

// parseOllama reads name[:tag]. Ollama's own default tag is "latest".
func parseOllama(rest string) (Ref, error) {
	r := Ref{Registry: "ollama", Revision: "latest"}
	if rest == "" {
		return Ref{}, fmt.Errorf("model: ollama reference needs a name")
	}
	if i := strings.LastIndexByte(rest, ':'); i >= 0 {
		r.Repo, r.Revision = rest[:i], rest[i+1:]
	} else {
		r.Repo = rest
	}
	if r.Repo == "" {
		return Ref{}, fmt.Errorf("model: ollama reference needs a name")
	}
	return r, nil
}
