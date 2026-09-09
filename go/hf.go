package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/openabstractions/abstraction-download/go"
)

// HF resolves HuggingFace references.
//
// The one thing it must do is come back with a digest. HuggingFace publishes
// `lfs.sha256` for large files, and that number is what makes everything
// downstream possible: without it the download layer has nothing to refuse
// against, and B2/C3 established that the advertised sha256 IS the hash of the
// bytes delivered — including from Xet-backed repos, where the CDN's
// X-Linked-ETag matches the digest we verified by hashing.
//
// When a file has no LFS record there is no digest to be had, and this refuses
// rather than returning a spec that would download unverifiable bytes.
type HF struct {
	BaseURL string
	Client  *http.Client
	// Token authenticates to gated repos. Sent as a bearer header, never put
	// in a URL or stored in a job record.
	Token string
}

func (HF) Registry() string { return "hf" }

type hfLFS struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type hfFile struct {
	Rfilename string `json:"rfilename"`
	Size      int64  `json:"size"`
	LFS       *hfLFS `json:"lfs,omitempty"`
}

type hfModel struct {
	SHA      string   `json:"sha"`
	Siblings []hfFile `json:"siblings"`
}

func (h HF) base() string {
	if h.BaseURL != "" {
		return h.BaseURL
	}
	return "https://huggingface.co"
}

func (h HF) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return http.DefaultClient
}

func (h HF) Resolve(ctx context.Context, ref Ref) (download.Spec, error) {
	if err := validRepo(ref.Repo); err != nil {
		return download.Spec{}, err
	}
	rev := ref.Revision
	if rev == "" {
		rev = "main"
	}

	api := fmt.Sprintf("%s/api/models/%s/revision/%s?blobs=true", h.base(), ref.Repo, rev)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return download.Spec{}, err
	}
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return download.Spec{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return download.Spec{}, fmt.Errorf("hf: %s %s", api, resp.Status)
	}
	var m hfModel
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return download.Spec{}, err
	}

	file, err := pick(m.Siblings, ref)
	if err != nil {
		return download.Spec{}, fmt.Errorf("%w (in %s@%s)", err, ref.Repo, rev)
	}
	if err := validFilename(file.Rfilename); err != nil {
		return download.Spec{}, err
	}
	if file.LFS == nil || file.LFS.SHA256 == "" {
		return download.Spec{}, fmt.Errorf(
			"hf: %s in %s@%s has no lfs.sha256 — refusing to fetch bytes nobody can check",
			file.Rfilename, ref.Repo, rev)
	}

	// Pin to the resolved commit rather than the branch we asked for. A branch
	// moves; a job that outlives a reboot and resumes next week must fetch the
	// same bytes it started, and C3 recorded what happens when a name is
	// trusted instead of a revision.
	pinned := rev
	if m.SHA != "" {
		pinned = m.SHA
	}

	size := file.Size
	if size == 0 && file.LFS.Size > 0 {
		size = file.LFS.Size
	}

	return download.Spec{
		Artifact: download.Artifact{Digest: "sha256:" + strings.ToLower(file.LFS.SHA256), Size: size},
		Sources: []download.Source{{
			Scheme:  "https",
			Locator: fmt.Sprintf("%s/%s/resolve/%s/%s", h.base(), ref.Repo, pinned, file.Rfilename),
			Attrs:   hfAttrs(h.Token),
		}},
	}, nil
}

// hfAttrs records that this source needs a credential — by NAME, never by
// value.
//
// An earlier version put "Authorization: Bearer <token>" in here, and Attrs go
// into the job record verbatim. The token would have sat in plain text in
// ~/.modelget/jobs/*.json, readable by anything that can list a directory, and
// copied into every transcript anyone pasted. The record is deliberately
// world-readable — that is what makes progress observable from outside — so it
// is the last place a secret may live.
//
// The download layer resolves the name at the moment of the request. See
// download/credentials.go.
func hfAttrs(token string) map[string]string {
	if token == "" {
		return nil
	}
	return map[string]string{download.CredentialAttr: HFCredential}
}

// HFCredential is the credential name for HuggingFace. The secret itself comes
// from $ABSTRACTION_CRED_HF at the moment it is needed.
const HFCredential = "hf"

// pick chooses the file the reference asked for.
func pick(files []hfFile, ref Ref) (hfFile, error) {
	if ref.File != "" {
		for _, f := range files {
			if f.Rfilename == ref.File {
				return f, nil
			}
		}
		return hfFile{}, fmt.Errorf("hf: no file named %q", ref.File)
	}

	var ggufs []hfFile
	for _, f := range files {
		if strings.EqualFold(path.Ext(f.Rfilename), ".gguf") {
			ggufs = append(ggufs, f)
		}
	}
	if len(ggufs) == 0 {
		return hfFile{}, fmt.Errorf("hf: no .gguf files; name one explicitly with hf://org/repo/<file>")
	}
	if ref.Quant != "" {
		var matched []hfFile
		for _, f := range ggufs {
			if strings.Contains(strings.ToUpper(f.Rfilename), strings.ToUpper(ref.Quant)) {
				matched = append(matched, f)
			}
		}
		if len(matched) == 0 {
			return hfFile{}, fmt.Errorf("hf: no .gguf matching quant %q", ref.Quant)
		}
		ggufs = matched
	}
	if len(ggufs) > 1 {
		// Refuse to guess between twenty quantisations of the same weights.
		// Picking "the first one" would make the same reference mean different
		// bytes depending on the order an API happened to return.
		sort.Slice(ggufs, func(i, j int) bool { return ggufs[i].Rfilename < ggufs[j].Rfilename })
		names := make([]string, 0, len(ggufs))
		for _, f := range ggufs {
			names = append(names, f.Rfilename)
		}
		if len(names) > 8 {
			names = append(names[:8], fmt.Sprintf("... and %d more", len(ggufs)-8))
		}
		return hfFile{}, fmt.Errorf("hf: %d files match; say which with #quant or /<file>:\n  %s",
			len(ggufs), strings.Join(names, "\n  "))
	}
	return ggufs[0], nil
}

func validRepo(repo string) error {
	if repo == "" {
		return fmt.Errorf("hf: empty repo id")
	}
	if strings.Contains(repo, "..") || strings.HasPrefix(repo, "/") {
		return fmt.Errorf("hf: suspicious repo id %q", repo)
	}
	if strings.Count(repo, "/") != 1 {
		return fmt.Errorf("hf: repo id %q must be org/name", repo)
	}
	return nil
}

// validFilename keeps a repo from naming a path outside the destination. The
// name arrives from a remote API and ends up in a job record that some other
// process, possibly running as a service, will later act on.
func validFilename(name string) error {
	if name == "" {
		return fmt.Errorf("hf: empty filename")
	}
	if strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) {
		return fmt.Errorf("hf: suspicious filename %q", name)
	}
	return nil
}
