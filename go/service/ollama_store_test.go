package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	model "github.com/openabstractions/abstraction-model/go"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
)

// writeOllamaManifest writes a manifest whose model layer names digest.
func writeOllamaManifest(t *testing.T, path, digest string) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"layers": []map[string]any{{"mediaType": "application/vnd.ollama.image.model", "digest": "sha256:" + digest, "size": 4}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// climbingDigest is a 64-character "digest" that, joined below the store's
// blobs folder, names outside/<file> beside the store.
func climbingDigest() string {
	base, name := "/../../../outside/", "t"
	if (64-len(base)-len(name))%2 != 0 {
		name = "tt"
	}
	return base + strings.Repeat("./", (64-len(base)-len(name))/2) + name
}

// TestOllamaLookupStaysInTheStore runs the runtime's Ollama resolver behind
// the generated model lookup IPC. A model in the store is read (the control).
// A revision or name that climbs to a manifest outside the store, and a
// manifest digest that climbs to a file outside the store, are refused: the
// lookup reports unavailable where reading the outside manifest and finding
// the outside file would report the store's own mapping.
func TestOllamaLookupStaysInTheStore(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	hexsum := strings.Repeat("ab", 32)
	if err := os.MkdirAll(filepath.Join(store, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "blobs", "sha256-"+hexsum), []byte("blob"), 0o644); err != nil {
		t.Fatal(err)
	}
	library := filepath.Join(store, "manifests", "registry.ollama.ai", "library")
	writeOllamaManifest(t, filepath.Join(library, "llama3", "latest"), hexsum)
	// Outside the store: a manifest naming the store's real blob, the file a
	// climbing digest names, and a manifest in the store using that digest.
	writeOllamaManifest(t, filepath.Join(dir, "outside", "m.json"), hexsum)
	outsideFile := filepath.Join(dir, "outside", strings.TrimLeft(climbingDigest()[len("/../../../outside/"):], "./"))
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeOllamaManifest(t, filepath.Join(library, "climb", "latest"), climbingDigest())

	_, c := liveHost(t, model.NewServiceRegistry(model.Ollama{Root: store}), false)
	// The resolver's file source carries its store attribute, which a
	// portable request never carries, so a model read from the store maps
	// to unsupported_mapping and an error maps to unavailable.
	r, e := c.Resolve(wire.Ref{Registry: "ollama", Repo: "llama3", Revision: "latest"})
	if e != nil || r.Outcome != wire.LookupOutcomeUnsupportedMapping {
		t.Fatalf("control lookup: %+v %v", r, e)
	}
	for _, ref := range []wire.Ref{
		{Registry: "ollama", Repo: "llama3", Revision: `..\..\..\..\..\outside\m.json`},
		{Registry: "ollama", Repo: "llama3", Revision: "../../../../../outside/m.json"},
		{Registry: "ollama", Repo: "x/../../../../outside/m.json"},
		{Registry: "ollama", Repo: "climb", Revision: "latest"},
		{Registry: "ollama", Repo: "llama3", Revision: "NUL"},
		{Registry: "ollama", Repo: "llama3", Revision: "COM¹"},
	} {
		r, e := c.Resolve(ref)
		if e != nil || r.Outcome != wire.LookupOutcomeUnavailable || r.Request != nil {
			t.Errorf("%s:%s left the store: %+v %v", ref.Repo, ref.Revision, r, e)
		}
	}
}
