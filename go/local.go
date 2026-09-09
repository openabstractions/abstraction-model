package model

import (
	"path/filepath"
	"runtime"
	"strings"

	download "github.com/openabstractions/abstraction-download/go"
	storage "github.com/openabstractions/abstraction-storage/go"
)

// Local finds bytes you already have.
//
// This is the part that pays for the whole design. Four tools on this machine
// hold 116 GB of model weights in four stores that know nothing about each
// other, and the same weights sit in more than one of them under different
// names. Once a registry has told us an artifact's digest, finding out whether
// those exact bytes are already on disk should not require downloading them
// again — and it does not require hashing 116 GB either, because two of the
// four stores already name their files by content.
//
// # What is left here, and what moved
//
// The scan itself is gone. It used to live in this file: a list of directories,
// a prefix per tool, and a stat. That was a STORAGE abstraction wearing the
// model layer's clothes — nothing about "is this digest on this disk" is
// AI-specific, and the download layer needed the same answer in order to stop
// making applications invent a destination.
//
// So it is now storage.Discover, and this type is the thin AI-shaped thing that
// remains: it knows a download.Spec, which the storage layer must never learn
// about, and it knows that a local copy should outrank the network.
type Local struct {
	stores *storage.Stores
}

// NewLocal looks in whatever content-addressed stores this machine has. Presence
// is the configuration: a machine without Ollama simply has no Ollama store.
func NewLocal() *Local { return &Local{stores: storage.New(storage.Discover()...)} }

// NewLocalIn is NewLocal against stores the caller already has, for tests and
// for anything that has already done discovery.
func NewLocalIn(s *storage.Stores) *Local { return &Local{stores: s} }

// Hit is one place the wanted bytes already are.
type Hit struct {
	Store string
	Path  string
	Size  int64
}

// Find returns every local copy of the artifact with this digest.
//
// It never hashes anything. A file found here is trusted only as far as its
// store's own naming convention goes — which is exactly why the download layer
// still verifies whatever it copies. Ollama's blobs hash to their own filenames
// (C2 measured that), but Ollama does not check them (G3 measured that too), so
// a corrupt blob in Ollama's store is a real possibility and this must not be
// the last word on whether the bytes are right.
func (l *Local) Find(digest string) []Hit {
	refs := l.stores.FindAll(digest)
	hits := make([]Hit, 0, len(refs))
	for _, r := range refs {
		p := l.stores.Path(r)
		if p == "" {
			// A store with no local filesystem behind it cannot offer a `file:`
			// source, and pretending otherwise would hand the download layer a
			// locator nothing can open.
			continue
		}
		hits = append(hits, Hit{Store: r.Store, Path: p, Size: r.Size})
	}
	return hits
}

// Augment puts every local copy in front of the remote sources, so the download
// layer reaches for bytes already on this disk before it touches the network.
//
// The ordering is the whole feature: download.Runner tries sources by priority,
// so cross-store dedup falls out of a design that was built for delegation, with
// no special case anywhere. A model already pulled by Ollama does not get
// downloaded again for ComfyUI — it gets copied, or hardlinked, and verified.
func (l *Local) Augment(spec download.Spec) download.Spec {
	hits := l.Find(spec.Artifact.Digest)
	if len(hits) == 0 {
		return spec
	}
	// A resolver may already have named the same file — the Ollama resolver
	// hands back the very blob this would find. Offering one path twice makes
	// the caller think there are two copies, and makes a failed source get
	// retried against itself.
	already := make(map[string]bool, len(spec.Sources))
	for _, s := range spec.Sources {
		if s.Scheme == "file" {
			already[strings.ToLower(filepath.Clean(s.Locator))] = true
		}
	}
	local := make([]download.Source, 0, len(hits))
	for _, h := range hits {
		key := strings.ToLower(filepath.Clean(h.Path))
		if already[key] {
			continue
		}
		already[key] = true
		local = append(local, download.Source{
			Scheme:   "file",
			Locator:  h.Path,
			Priority: -100 + len(local), // ahead of anything a registry supplied
			Attrs:    map[string]string{"store": h.Store},
		})
	}
	spec.Sources = append(local, spec.Sources...)
	return spec
}

// SameVolume reports whether two paths are on one volume, which decides whether
// a hardlink is possible. A hardlink makes the "download" free and the bytes
// shared rather than duplicated — the actual answer to four tools keeping four
// copies.
func SameVolume(a, b string) bool {
	if runtime.GOOS == "windows" {
		va, vb := filepath.VolumeName(strings.ToUpper(a)), filepath.VolumeName(strings.ToUpper(b))
		return va != "" && va == vb
	}
	// On POSIX this needs a stat comparison of device numbers, which the
	// caller can do; returning false is the safe answer because it only costs
	// a copy.
	return false
}
