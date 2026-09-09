package model

import (
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openabstractions/abstraction-model/go/identity"
)

// Copy is one file of weights, in one tool's store, under whatever name that
// tool gave it.
//
// Digest is empty where the store publishes none, and that is the common case:
// of the four stores on the machine this was written against, only Ollama names
// a file by its content. Two copies with the same Family and different Quant are
// one model for routing and two files for storage — both facts have to survive,
// which is why they are separate fields rather than one identity string.
type Copy struct {
	Store  string
	Name   string
	Path   string
	Digest string
	Quant  string
	Size   int64
}

type Group struct {
	Family string
	Copies []Copy
}

// Inventory is every model file this machine holds, grouped by family.
//
// ABSTRACTION_MODEL_ROOTS adds directories for stores with no fixed location —
// ComfyUI's models tree is one, and it differs per installation. An entry may be
// `name=dir` to say which store the directory belongs to.
//
// Naming them is not decoration. The machine this was measured on holds nine
// model stores, not the three with fixed locations: ComfyUI, FastFlowLM's NPU
// cache, and four directories three OEM applications installed. Reported under
// one label they are one anonymous pile, and "which tool will break if this
// moves" is the only question an inventory is asked.
func Inventory() []Group {
	var all []Copy
	home, err := os.UserHomeDir()
	if err == nil {
		all = append(all, ollamaCopies(filepath.Join(home, ".ollama", "models"))...)
		all = append(all, treeCopies("huggingface", filepath.Join(home, ".cache", "huggingface", "hub"))...)
		all = append(all, treeCopies("lmstudio", filepath.Join(home, ".lmstudio", "models"))...)
	}
	for _, root := range filepath.SplitList(os.Getenv("ABSTRACTION_MODEL_ROOTS")) {
		if store, dir, named := strings.Cut(root, "="); named && store != "" && dir != "" {
			all = append(all, treeCopies(store, dir)...)
		} else if root != "" {
			all = append(all, treeCopies("extra", root)...)
		}
	}

	byFamily := map[string][]Copy{}
	for _, c := range foldShards(all) {
		byFamily[identity.Family(c.Name)] = append(byFamily[identity.Family(c.Name)], c)
	}
	out := make([]Group, 0, len(byFamily))
	for f, cs := range byFamily {
		sort.Slice(cs, func(i, j int) bool { return cs[i].Path < cs[j].Path })
		out = append(out, Group{Family: f, Copies: cs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Family < out[j].Family })
	return out
}

// foldShards makes one set of pieces one entry, because a sharded artifact split
// over three files is not three copies of anything.
func foldShards(all []Copy) []Copy {
	out := make([]Copy, 0, len(all))
	at := map[string]int{}
	for _, c := range all {
		if _, _, ok := identity.Shard(c.Name); !ok {
			out = append(out, c)
			continue
		}
		key := c.Store + "\x00" + filepath.Dir(c.Path) + "\x00" + identity.Family(c.Name) + "\x00" + c.Quant
		if i, seen := at[key]; seen {
			out[i].Size += c.Size
			out[i].Digest = ""
			continue
		}
		at[key] = len(out)
		out = append(out, c)
	}
	return out
}

func ollamaCopies(root string) []Copy {
	manifests := filepath.Join(root, "manifests", "registry.ollama.ai")
	var out []Copy
	filepath.WalkDir(manifests, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(manifests, p)
		if err != nil {
			return nil
		}
		slash := filepath.ToSlash(rel)
		i := strings.LastIndexByte(slash, '/')
		if i < 0 {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		var m ollamaManifest
		if json.Unmarshal(b, &m) != nil {
			return nil
		}
		for _, l := range m.Layers {
			if !strings.Contains(l.MediaType, ".image.model") {
				continue
			}
			digest := strings.ToLower(strings.TrimPrefix(l.Digest, "sha256:"))
			blob := filepath.Join(root, "blobs", "sha256-"+digest)
			st, err := os.Stat(blob)
			if err != nil {
				continue
			}
			name := slash[:i] + ":" + slash[i+1:]
			out = append(out, Copy{
				Store: "ollama", Name: name, Path: blob,
				Digest: "sha256:" + digest, Quant: identity.Parse(name).Quant, Size: st.Size(),
			})
		}
		return nil
	})
	return out
}

func treeCopies(store, root string) []Copy {
	var out []Copy
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !weightFile(d.Name()) {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		name := filepath.ToSlash(rel)
		if store == "huggingface" {
			name = hfName(name)
		}
		out = append(out, Copy{
			Store: store, Name: name, Path: p, Digest: blobDigest(p),
			Quant: identity.Parse(name).Quant, Size: st.Size(),
		})
		return nil
	})
	return out
}

// hfName turns models--org--repo/snapshots/<revision>/<file> back into the
// org/repo/file the rest of the world uses.
func hfName(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) < 4 || !strings.HasPrefix(parts[0], "models--") || parts[1] != "snapshots" {
		return rel
	}
	repo := strings.ReplaceAll(strings.TrimPrefix(parts[0], "models--"), "--", "/")
	return repo + "/" + strings.Join(parts[3:], "/")
}

// blobDigest reads the digest a HuggingFace cache publishes by linking a
// snapshot entry at blobs/<etag>, where the etag is the sha256 for LFS files.
func blobDigest(p string) string {
	real, err := filepath.EvalSymlinks(p)
	if err != nil || real == p {
		return ""
	}
	if filepath.Base(filepath.Dir(real)) != "blobs" {
		return ""
	}
	name := filepath.Base(real)
	if b, err := hex.DecodeString(name); err != nil || len(b) != 32 {
		return ""
	}
	return "sha256:" + name
}

func weightFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".gguf", ".safetensors", ".sft", ".onnx", ".pth", ".pt", ".bin":
		return true
	}
	return false
}
