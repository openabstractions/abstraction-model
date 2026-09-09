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
	if strings.Contains(name, "..") {
		return download.Spec{}, fmt.Errorf("ollama: suspicious model name %q", ref.Repo)
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
	if len(digest) != 64 {
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
