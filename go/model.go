// Package model is the AI-specific layer above the generic download.
//
// The layer below it does not know what a model is: it moves bytes from typed
// sources to a path and refuses anything whose digest is wrong. This layer knows
// the things that are specific to model weights, and there are only a few — but
// each one is real, and each one is why this is a separate layer rather than
// clutter in the generic one:
//
//   - **The digest is published, not supplied.** A caller asking for a model
//     does not know its hash. HuggingFace publishes lfs.sha256; Ollama names its
//     blobs by digest. This layer's job is to get that number before a byte is
//     transferred, so the generic layer has something to refuse against. When a
//     registry will not say, this layer refuses rather than fetching bytes
//     nobody can check.
//   - **A model reference is not a URL.** hf://org/repo@revision#quant selects
//     one file out of a repo that may hold twenty quantisations of the same
//     weights.
//   - **You probably already have it.** Four stores on this machine hold 116 GB
//     between them, and the same weights appear in more than one. See local.go.
package model

import (
	"context"
	"fmt"
	"strings"

	"github.com/openabstractions/abstraction-download/go"
	job "github.com/openabstractions/abstraction-job/go"
)

// Resolver turns a model reference into something the download layer can act
// on: an artifact identity, and the places it can be had.
//
// One per registry. The interface is small because everything hard — resume,
// verification, delegation, ownership — already belongs to the layers below.
type Resolver interface {
	// Registry is the ref scheme this handles: "hf", "ollama", or a provider's
	// own scheme. For other schemes, Ref.Repo is the opaque locator after ://;
	// the resolver interprets its syntax. Built-in schemes retain ParseRef rules.
	Registry() string
	// Resolve produces a spec with the artifact's digest filled in. It must
	// refuse rather than return a spec with an empty digest: a download nobody
	// can verify is the thing this project exists to stop.
	Resolve(ctx context.Context, ref Ref) (download.Spec, error)
}

// Registry holds the resolvers available.
type Registry struct {
	resolvers []Resolver
	local     *Local
}

func NewRegistry(rs ...Resolver) *Registry {
	return &Registry{resolvers: rs, local: NewLocal()}
}

// Default is every resolver that needs no configuration.
func Default() *Registry { return NewRegistry(HF{}, Ollama{}) }

func (r *Registry) Add(x Resolver) { r.resolvers = append(r.resolvers, x) }

// SetLocal replaces the local-store lookup, mostly so tests can point it at a
// directory they control.
func (r *Registry) SetLocal(l *Local) { r.local = l }

// Resolve finds the artifact and every way of getting it, local copies first.
func (r *Registry) Resolve(ctx context.Context, refStr string) (download.Spec, error) {
	refStr = strings.TrimSpace(refStr)
	scheme, locator, ok := strings.Cut(refStr, "://")
	if !ok || scheme == "" || locator == "" {
		return download.Spec{}, fmt.Errorf("model: %q needs a scheme and a nonempty locator", refStr)
	}
	ref := Ref{Registry: scheme, Repo: locator}
	if scheme == "hf" || scheme == "ollama" {
		var err error
		ref, err = ParseRef(refStr)
		if err != nil {
			return download.Spec{}, err
		}
	}
	for _, res := range r.resolvers {
		if res.Registry() != ref.Registry {
			continue
		}
		spec, err := res.Resolve(ctx, ref)
		if err != nil {
			return download.Spec{}, err
		}
		if spec.Artifact.Digest == "" {
			return download.Spec{}, fmt.Errorf("model: %s resolved %s without a digest; refusing to fetch bytes nobody can check",
				res.Registry(), refStr)
		}
		if r.local != nil {
			spec = r.local.Augment(spec)
		}
		return spec, nil
	}
	return download.Spec{}, fmt.Errorf("model: no resolver for %q", ref.Registry)
}

// Submit resolves a reference and submits it as a download job. It returns the
// job id, which outlives this process — that is the point of the whole stack.
func Submit(ctx context.Context, reg *Registry, store job.Store, refStr, dest string, requires ...string) (string, download.Spec, error) {
	spec, err := reg.Resolve(ctx, refStr)
	if err != nil {
		return "", download.Spec{}, err
	}
	spec.Sink.Final = dest
	id, err := download.Submit(store, spec, requires...)
	if err != nil {
		return "", spec, err
	}
	return id, spec, nil
}
