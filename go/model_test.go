package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openabstractions/abstraction-download/go"
	job "github.com/openabstractions/abstraction-job/go"
	storage "github.com/openabstractions/abstraction-storage/go"
)

func TestParseRef(t *testing.T) {
	cases := []struct {
		in   string
		want Ref
	}{
		{"hf://org/repo", Ref{Registry: "hf", Repo: "org/repo"}},
		{"hf://org/repo#Q4_K_M", Ref{Registry: "hf", Repo: "org/repo", Quant: "Q4_K_M"}},
		{"hf://org/repo@abc123", Ref{Registry: "hf", Repo: "org/repo", Revision: "abc123"}},
		{"hf://org/repo@abc123/sub/model.gguf", Ref{Registry: "hf", Repo: "org/repo", Revision: "abc123", File: "sub/model.gguf"}},
		{"hf://org/repo/model.gguf", Ref{Registry: "hf", Repo: "org/repo", File: "model.gguf"}},
		{"ollama://qwen2.5:0.5b", Ref{Registry: "ollama", Repo: "qwen2.5", Revision: "0.5b"}},
		{"ollama://qwen2.5", Ref{Registry: "ollama", Repo: "qwen2.5", Revision: "latest"}},
	}
	for _, c := range cases {
		got, err := ParseRef(c.in)
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseRef(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"org/repo", "hf://justone", "hf://", "ollama://", "ftp://x/y"} {
		if _, err := ParseRef(bad); err == nil {
			t.Fatalf("ParseRef(%q) was accepted", bad)
		}
	}
}

type privateRegistry struct {
	got    []Ref
	digest string
}

func (p *privateRegistry) Registry() string { return "private" }
func (p *privateRegistry) Resolve(_ context.Context, ref Ref) (download.Spec, error) {
	p.got = append(p.got, ref)
	return download.Spec{Artifact: download.Artifact{Digest: p.digest}}, nil
}

func TestRegisteredSchemeReachesItsResolver(t *testing.T) {
	p := &privateRegistry{digest: "sha256:" + strings.Repeat("a", 64)}
	r := NewRegistry()
	r.SetLocal(nil)
	r.Add(p)
	const locator = "tenant/model@release#variant?format=weights"
	spec, err := r.Resolve(context.Background(), "private://"+locator)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.got) != 1 || p.got[0].Registry != "private" || p.got[0].Repo != locator {
		t.Fatalf("resolver input: %+v", p.got)
	}
	if got := p.got[0].String(); got != "private://"+locator {
		t.Fatalf("reference lost its scheme: %s", got)
	}
	if spec.Artifact.Digest != p.digest {
		t.Fatal("resolver result was lost")
	}
	if _, err := r.Resolve(context.Background(), "private://"); err == nil {
		t.Fatal("empty locator accepted")
	}
	if _, err := r.Resolve(context.Background(), "unregistered://model"); err == nil {
		t.Fatal("unregistered scheme accepted")
	}
	if len(p.got) != 1 {
		t.Fatal("invalid input reached resolver")
	}
	p.digest = ""
	if _, err := r.Resolve(context.Background(), "private://"+locator); err == nil {
		t.Fatal("unverifiable resolver result accepted")
	}
}

// hfServer stands in for the HuggingFace API.
func hfServer(t *testing.T, model hfModel) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/models/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(model)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestHFResolvePinsRevisionAndDigest(t *testing.T) {
	srv := hfServer(t, hfModel{
		SHA: "41ba88dbac95fed2528c92514c131d73eb5a174b",
		Siblings: []hfFile{
			{Rfilename: "README.md", Size: 100},
			{Rfilename: "model-Q4_K_M.gguf", Size: 397808192, LFS: &hfLFS{SHA256: strings.Repeat("ab", 32)}},
		},
	})
	spec, err := HF{BaseURL: srv.URL}.Resolve(context.Background(), Ref{Registry: "hf", Repo: "org/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Artifact.Digest != "sha256:"+strings.Repeat("ab", 32) {
		t.Fatalf("digest = %q", spec.Artifact.Digest)
	}
	if spec.Artifact.Size != 397808192 {
		t.Fatalf("size = %d", spec.Artifact.Size)
	}
	// The resolved commit, not the branch we asked for. A branch moves; a job
	// that resumes next week must fetch the bytes it started on.
	if !strings.Contains(spec.Sources[0].Locator, "41ba88dbac95fed2528c92514c131d73eb5a174b") {
		t.Fatalf("source was not pinned to the resolved revision: %s", spec.Sources[0].Locator)
	}
}

// TestHFRefusesFileWithNoDigest is the layer's whole reason for existing: get a
// digest before any bytes move, or refuse.
func TestHFRefusesFileWithNoDigest(t *testing.T) {
	srv := hfServer(t, hfModel{Siblings: []hfFile{{Rfilename: "model.gguf", Size: 10}}}) // no LFS record
	_, err := HF{BaseURL: srv.URL}.Resolve(context.Background(), Ref{Registry: "hf", Repo: "org/repo"})
	if err == nil {
		t.Fatal("a file with no published digest was accepted")
	}
	if !strings.Contains(err.Error(), "nobody can check") {
		t.Fatalf("error does not explain the refusal: %v", err)
	}
}

// TestHFRefusesToGuessBetweenQuants: twenty quantisations of the same weights
// are twenty different artifacts. Picking the first would make one reference
// mean different bytes depending on API ordering.
func TestHFRefusesToGuessBetweenQuants(t *testing.T) {
	srv := hfServer(t, hfModel{Siblings: []hfFile{
		{Rfilename: "m-Q4_K_M.gguf", LFS: &hfLFS{SHA256: strings.Repeat("aa", 32)}},
		{Rfilename: "m-Q8_0.gguf", LFS: &hfLFS{SHA256: strings.Repeat("bb", 32)}},
	}})
	_, err := HF{BaseURL: srv.URL}.Resolve(context.Background(), Ref{Registry: "hf", Repo: "org/repo"})
	if err == nil {
		t.Fatal("resolver guessed between two quantisations")
	}
	if !strings.Contains(err.Error(), "say which") {
		t.Fatalf("error does not tell the user how to disambiguate: %v", err)
	}
	// With the quant named, it resolves.
	spec, err := HF{BaseURL: srv.URL}.Resolve(context.Background(), Ref{Registry: "hf", Repo: "org/repo", Quant: "Q8_0"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Artifact.Digest != "sha256:"+strings.Repeat("bb", 32) {
		t.Fatalf("picked the wrong file: %s", spec.Sources[0].Locator)
	}
}

func TestHFRejectsSuspiciousNames(t *testing.T) {
	srv := hfServer(t, hfModel{Siblings: []hfFile{
		{Rfilename: "../../../etc/passwd.gguf", LFS: &hfLFS{SHA256: strings.Repeat("cc", 32)}},
	}})
	if _, err := (HF{BaseURL: srv.URL}).Resolve(context.Background(), Ref{Registry: "hf", Repo: "org/repo"}); err == nil {
		t.Fatal("a path-traversing filename from a remote API was accepted")
	}
	if _, err := (HF{BaseURL: srv.URL}).Resolve(context.Background(), Ref{Registry: "hf", Repo: "../evil"}); err == nil {
		t.Fatal("a suspicious repo id was accepted")
	}
}

// --- local stores ---------------------------------------------------------

func fakeOllamaStore(t *testing.T, name, tag string, weights []byte) string {
	t.Helper()
	root := t.TempDir()
	sum := sha256.Sum256(weights)
	hexsum := hex.EncodeToString(sum[:])
	blobs := filepath.Join(root, "blobs")
	os.MkdirAll(blobs, 0o755)
	if err := os.WriteFile(filepath.Join(blobs, "sha256-"+hexsum), weights, 0o644); err != nil {
		t.Fatal(err)
	}
	mdir := filepath.Join(root, "manifests", "registry.ollama.ai", "library", name)
	os.MkdirAll(mdir, 0o755)
	man := ollamaManifest{Layers: []ollamaLayer{
		{MediaType: "application/vnd.ollama.image.license", Digest: "sha256:" + strings.Repeat("11", 32), Size: 10},
		{MediaType: "application/vnd.ollama.image.model", Digest: "sha256:" + hexsum, Size: int64(len(weights))},
	}}
	b, _ := json.Marshal(man)
	if err := os.WriteFile(filepath.Join(mdir, tag), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestOllamaResolvesFromLocalStoreWithNoNetwork: Ollama's manifests name every
// layer by digest and its blobs are named by content, so a model already pulled
// resolves to a verified identity and a local path without touching the network.
func TestOllamaResolvesFromLocalStore(t *testing.T) {
	weights := []byte("pretend these are model weights")
	root := fakeOllamaStore(t, "qwen2.5", "0.5b", weights)
	sum := sha256.Sum256(weights)

	spec, err := Ollama{Root: root}.Resolve(context.Background(), Ref{Registry: "ollama", Repo: "qwen2.5", Revision: "0.5b"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Artifact.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("digest = %s", spec.Artifact.Digest)
	}
	if spec.Sources[0].Scheme != "file" {
		t.Fatalf("source scheme = %s, want file", spec.Sources[0].Scheme)
	}
	if _, err := (Ollama{Root: root}).Resolve(context.Background(), Ref{Registry: "ollama", Repo: "not-pulled"}); err == nil {
		t.Fatal("resolved a model that was never pulled")
	}
}

// TestLocalFindIsAStatNotAScan: the lookup must not depend on hashing anything,
// because the stores on a real machine hold 116 GB between them.
func TestLocalFind(t *testing.T) {
	weights := []byte("shared bytes")
	sum := sha256.Sum256(weights)
	hexsum := hex.EncodeToString(sum[:])

	root := t.TempDir()
	ollamaBlobs := filepath.Join(root, "ollama", "blobs")
	hfBlobs := filepath.Join(root, "hf", "blobs")
	os.MkdirAll(ollamaBlobs, 0o755)
	os.MkdirAll(hfBlobs, 0o755)
	os.WriteFile(filepath.Join(ollamaBlobs, "sha256-"+hexsum), weights, 0o644)
	os.WriteFile(filepath.Join(hfBlobs, hexsum), weights, 0o644)

	l := NewLocalIn(storage.New(
		storage.NewForeignStore("ollama", ollamaBlobs, "sha256-"),
		storage.NewForeignStore("huggingface", hfBlobs, ""),
	))
	hits := l.Find("sha256:" + hexsum)
	if len(hits) != 2 {
		t.Fatalf("found %d copies, want 2: %+v", len(hits), hits)
	}
	if len(l.Find("sha256:"+strings.Repeat("ff", 32))) != 0 {
		t.Fatal("found bytes that are not there")
	}
}

// TestAugmentPutsLocalCopiesFirst is the dedup feature, and it works only
// because a source is a typed locator rather than a URL. The download layer
// tries sources in priority order, so "you already have this" needs no special
// case anywhere.
func TestAugmentPutsLocalCopiesFirst(t *testing.T) {
	weights := []byte("shared bytes")
	sum := sha256.Sum256(weights)
	hexsum := hex.EncodeToString(sum[:])
	blobs := filepath.Join(t.TempDir(), "blobs")
	os.MkdirAll(blobs, 0o755)
	os.WriteFile(filepath.Join(blobs, "sha256-"+hexsum), weights, 0o644)

	l := NewLocalIn(storage.New(storage.NewForeignStore("ollama", blobs, "sha256-")))
	spec := download.Spec{
		Artifact: download.Artifact{Digest: "sha256:" + hexsum, Size: int64(len(weights))},
		Sources:  []download.Source{{Scheme: "https", Locator: "https://example.invalid/model.gguf"}},
	}
	got := l.Augment(spec)
	if len(got.Sources) != 2 {
		t.Fatalf("sources = %d, want 2", len(got.Sources))
	}
	if got.Sources[0].Scheme != "file" || got.Sources[0].Priority >= got.Sources[1].Priority {
		t.Fatalf("the local copy was not preferred: %+v", got.Sources)
	}
	if got.Sources[0].Attrs["store"] != "ollama" {
		t.Fatal("the hit does not say which store it came from")
	}
}

// TestAugmentDoesNotDuplicateAResolversOwnFile: the Ollama resolver hands back
// the same blob the local lookup would find. Listing one path twice tells the
// user they have two copies and makes a failed source retry against itself.
func TestAugmentDoesNotDuplicate(t *testing.T) {
	weights := []byte("shared bytes")
	sum := sha256.Sum256(weights)
	hexsum := hex.EncodeToString(sum[:])
	blobs := filepath.Join(t.TempDir(), "blobs")
	os.MkdirAll(blobs, 0o755)
	blob := filepath.Join(blobs, "sha256-"+hexsum)
	os.WriteFile(blob, weights, 0o644)

	l := NewLocalIn(storage.New(storage.NewForeignStore("ollama", blobs, "sha256-")))
	spec := download.Spec{
		Artifact: download.Artifact{Digest: "sha256:" + hexsum},
		Sources:  []download.Source{{Scheme: "file", Locator: blob, Attrs: map[string]string{"store": "ollama"}}},
	}
	got := l.Augment(spec)
	if len(got.Sources) != 1 {
		t.Fatalf("the same file was offered %d times: %+v", len(got.Sources), got.Sources)
	}
}

// TestEndToEndUsesLocalCopyInsteadOfNetwork is the payoff, exercised through the
// real download runner: the registry says where the bytes are on the internet,
// the local store already has them, and nothing goes near the network.
func TestEndToEndUsesLocalCopyInsteadOfNetwork(t *testing.T) {
	weights := []byte(strings.Repeat("model weights ", 1000))
	sum := sha256.Sum256(weights)
	hexsum := hex.EncodeToString(sum[:])

	// The HF API knows about it; the "download" URL is unreachable on purpose,
	// so if this test passes, the bytes came from the local store.
	srv := hfServer(t, hfModel{
		SHA:      "deadbeef",
		Siblings: []hfFile{{Rfilename: "m.gguf", Size: int64(len(weights)), LFS: &hfLFS{SHA256: hexsum}}},
	})

	root := t.TempDir()
	blobs := filepath.Join(root, "blobs")
	os.MkdirAll(blobs, 0o755)
	os.WriteFile(filepath.Join(blobs, "sha256-"+hexsum), weights, 0o644)

	reg := NewRegistry(HF{BaseURL: srv.URL, Client: &http.Client{}})
	reg.SetLocal(NewLocalIn(storage.New(storage.NewForeignStore("ollama", blobs, "sha256-"))))

	store, err := job.NewFileStore(filepath.Join(root, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "out", "m.gguf")
	id, spec, err := Submit(context.Background(), reg, store, "hf://org/repo", dest)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Sources[0].Scheme != "file" {
		t.Fatalf("the local copy was not preferred: %+v", spec.Sources)
	}

	runner := download.NewRunner(store, "test")
	// Point the https fetcher at nothing: reaching the network is a failure.
	if err := runner.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(weights) {
		t.Fatal("delivered bytes differ")
	}
	rec, _ := store.Load(id)
	if rec.State != job.StateTransferred {
		t.Fatalf("state = %s", rec.State)
	}
}

// TestCorruptLocalCopyIsCaught: Ollama does not check its own digests — G3
// corrupted a blob in place and Ollama served it. So a local hit is a candidate,
// not an answer, and the layer below must still verify it.
func TestCorruptLocalCopyIsCaught(t *testing.T) {
	weights := []byte(strings.Repeat("model weights ", 1000))
	sum := sha256.Sum256(weights)
	hexsum := hex.EncodeToString(sum[:])

	root := t.TempDir()
	blobs := filepath.Join(root, "blobs")
	os.MkdirAll(blobs, 0o755)
	// The filename claims one thing; the contents are another. Exactly what G3
	// left behind in a real Ollama store.
	corrupt := append([]byte(nil), weights...)
	corrupt[len(corrupt)/2] ^= 0xFF
	os.WriteFile(filepath.Join(blobs, "sha256-"+hexsum), corrupt, 0o644)

	store, err := job.NewFileStore(filepath.Join(root, "jobs"))
	if err != nil {
		t.Fatal(err)
	}
	spec := download.Spec{
		Artifact: download.Artifact{Digest: "sha256:" + hexsum, Size: int64(len(weights))},
		Sink:     download.Sink{Final: filepath.Join(root, "out.gguf")},
	}
	l := NewLocalIn(storage.New(storage.NewForeignStore("ollama", blobs, "sha256-")))
	spec = l.Augment(spec)
	if len(spec.Sources) != 1 {
		t.Fatalf("expected the corrupt local copy to be offered as a source: %+v", spec.Sources)
	}

	id, err := download.Submit(store, spec)
	if err != nil {
		t.Fatal(err)
	}
	err = download.NewRunner(store, "test").Run(context.Background(), id)
	if err == nil {
		t.Fatal("a corrupt local copy was accepted because a filename said it was fine")
	}
	if !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("error = %v, want a digest mismatch", err)
	}
	fmt.Fprintf(os.Stderr, "refused: %v\n", err)
}

// TestTokenNeverReachesTheRecord is a regression test for a real leak. The HF
// resolver used to put "Authorization: Bearer <token>" into a Source's Attrs,
// and Attrs are written into the job record verbatim — a record that is
// deliberately readable by every other process, because that is what makes
// progress observable from outside.
//
// The record may name a credential. It may never hold one.
func TestTokenNeverReachesTheRecord(t *testing.T) {
	const secret = "hf_thisMustNeverAppearOnDisk_EXAMPLE"
	srv := hfServer(t, hfModel{
		SHA:      "abc",
		Siblings: []hfFile{{Rfilename: "m.gguf", Size: 10, LFS: &hfLFS{SHA256: strings.Repeat("ab", 32)}}},
	})

	spec, err := HF{BaseURL: srv.URL, Token: secret}.Resolve(
		context.Background(), Ref{Registry: "hf", Repo: "org/repo"})
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range spec.Sources[0].Attrs {
		if strings.Contains(v, secret) {
			t.Fatalf("the token is in the source attrs under %q — it would be written to the job record", k)
		}
	}
	if spec.Sources[0].Attrs[download.CredentialAttr] != HFCredential {
		t.Fatalf("the source does not name a credential: %+v", spec.Sources[0].Attrs)
	}

	// And prove it end to end: submit, then read the bytes actually on disk.
	root := t.TempDir()
	store, err := job.NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	spec.Sink.Final = filepath.Join(root, "m.gguf")
	id, err := download.Submit(store, spec)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "jobs", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("the token is in the job record on disk:\n%s", raw)
	}
	if !strings.Contains(string(raw), HFCredential) {
		t.Fatal("the record does not name the credential, so nothing could resolve it later")
	}
}

// TestNamedRootsKeepTheirStore is the attribution the inventory exists for. Nine
// stores on the measured machine, six of them nameable only through this
// variable; reported as "extra" they cannot answer which tool breaks if a file
// moves.
func TestNamedRootsKeepTheirStore(t *testing.T) {
	comfy := t.TempDir()
	anon := t.TempDir()
	os.WriteFile(filepath.Join(comfy, "wan2.2_ti2v_5B_fp16.safetensors"), []byte("w"), 0o644)
	os.WriteFile(filepath.Join(anon, "SmolLM2-135M-Instruct-Q4_K_M.gguf"), []byte("w"), 0o644)

	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("ABSTRACTION_MODEL_ROOTS", strings.Join([]string{"comfyui=" + comfy, anon}, string(filepath.ListSeparator)))

	got := map[string]string{}
	for _, g := range Inventory() {
		for _, c := range g.Copies {
			got[filepath.Base(c.Path)] = c.Store
		}
	}
	if got["wan2.2_ti2v_5B_fp16.safetensors"] != "comfyui" {
		t.Errorf("named root lost its store: %q", got["wan2.2_ti2v_5B_fp16.safetensors"])
	}
	if got["SmolLM2-135M-Instruct-Q4_K_M.gguf"] != "extra" {
		t.Errorf("bare root should stay extra: %q", got["SmolLM2-135M-Instruct-Q4_K_M.gguf"])
	}
}
