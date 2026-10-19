package model

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openabstractions/abstraction-download/go"
)

// Ollama resolves a model already pulled by Ollama, straight out of its local
// store.
//
// This resolver does no network I/O at all, and that is the interesting part.
// Ollama's manifests list every layer by digest and its blobs are named
// `sha256-<hex>` — C2 established that the blob content really does hash to its
// own filename. So a reference like ollama://qwen2.5:0.5b resolves to an
// artifact whose digest is known and whose only source is a file already on
// this disk.
//
// What that buys: the 26 GB Lemonade holds and the 23 GB Ollama holds are no
// longer two unrelated piles. Ask for weights by digest and whichever store has
// them answers.
//
// What it does NOT buy, and this matters: Ollama never checks those digests.
// G3 corrupted a blob in place, and Ollama listed it, loaded it and served
// confident nonsense while the filename still claimed the original hash. So a
// blob found here is a *candidate*, and the download layer verifies it like any
// other source. That is the whole reason verification lives below this layer
// rather than in it.
type Ollama struct {
	// Root is the models directory. Empty means the usual per-user location.
	Root string
}

func (Ollama) Registry() string { return "ollama" }

func (o Ollama) root() string {
	if o.Root != "" {
		return o.Root
	}
	if v := os.Getenv("OLLAMA_MODELS"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ollama", "models")
}

type ollamaLayer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type ollamaManifest struct {
	Layers []ollamaLayer `json:"layers"`
}

func (o Ollama) Resolve(ctx context.Context, ref Ref) (download.Spec, error) {
	root := o.root()
	if root == "" {
		return download.Spec{}, fmt.Errorf("ollama: cannot locate the models directory")
	}
	name, tag := ref.Repo, ref.Revision
	if tag == "" {
		tag = "latest"
	}
	// Ollama namespaces bare names under library/.
	if !strings.Contains(name, "/") {
		name = "library/" + name
	}
	// The name and tag are path parts below the store: each part is a plain
	// name, never a separator, a drive, a stream or a climb.
	if !storePathParts(name, true) || !storePathParts(tag, false) {
		return download.Spec{}, fmt.Errorf("ollama: suspicious model name %q", ref.Repo+":"+ref.Revision)
	}

	mpath := filepath.Join(root, "manifests", "registry.ollama.ai", filepath.FromSlash(name), tag)
	b, err := os.ReadFile(mpath)
	if err != nil {
		return download.Spec{}, fmt.Errorf("ollama: %s:%s is not pulled locally (%s)", ref.Repo, tag, mpath)
	}
	var m ollamaManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return download.Spec{}, fmt.Errorf("ollama: unreadable manifest %s: %w", mpath, err)
	}

	// The weights are the model layer. Ollama tags it explicitly, so there is no
	// need to guess by size.
	var weights *ollamaLayer
	for i := range m.Layers {
		if strings.Contains(m.Layers[i].MediaType, ".image.model") {
			weights = &m.Layers[i]
			break
		}
	}
	if weights == nil {
		return download.Spec{}, fmt.Errorf("ollama: %s:%s has no model layer", ref.Repo, tag)
	}
	digest := strings.ToLower(strings.TrimPrefix(weights.Digest, "sha256:"))
	// The digest names the blob's file below the store, so it is 64 hex
	// digits and nothing a path could climb with.
	if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
		return download.Spec{}, fmt.Errorf("ollama: manifest digest %q is not sha256", weights.Digest)
	}

	blob := filepath.Join(root, "blobs", "sha256-"+digest)
	if _, err := os.Stat(blob); err != nil {
		return download.Spec{}, fmt.Errorf("ollama: manifest names a blob that is not there: %s", blob)
	}

	return download.Spec{
		Artifact: download.Artifact{Digest: "sha256:" + digest, Size: weights.Size},
		Sources: []download.Source{{
			Scheme:  "file",
			Locator: blob,
			Attrs:   map[string]string{"store": "ollama"},
		}},
	}, nil
}

// storePathParts reports whether s is one name, or with slashes a run of
// names separated by "/", each of which stays below the store as a path part.
func storePathParts(s string, slashes bool) bool {
	if s == "" || len(s) > 512 || strings.ContainsAny(s, "\\:\x00") {
		return false
	}
	parts := []string{s}
	if slashes {
		parts = strings.Split(s, "/")
	} else if strings.Contains(s, "/") {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || windowsDeviceName(part) {
			return false
		}
		// The store's name grammar: a letter, digit or underscore, then
		// ASCII letters, digits, "_", "-" and ".". It leaves out the
		// superscript-digit and spaced device names Win32 also opens.
		for i := 0; i < len(part); i++ {
			c := part[i]
			word := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
			if !word && (i == 0 || c != '-' && c != '.') {
				return false
			}
		}
	}
	return true
}

// windowsDeviceName reports a name Windows opens as a device whatever its
// folder: CON, PRN, AUX, NUL, COM0 to COM9 and LPT0 to LPT9, with or without
// an extension.
func windowsDeviceName(part string) bool {
	base, _, _ := strings.Cut(strings.ToUpper(part), ".")
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	return len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9'
}
