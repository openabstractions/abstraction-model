package service

import (
	"context"
	"errors"
	download "github.com/openabstractions/abstraction-download/go"
	request "github.com/openabstractions/abstraction-download/go/abstraction/download/request"
	downloadserve "github.com/openabstractions/abstraction-download/go/serve"
	model "github.com/openabstractions/abstraction-model/go"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
	"strings"
	"unicode/utf8"
)

func lookup(ctx context.Context, registry *model.Registry, ref wire.Ref) wire.LookupResult {
	result := func(outcome string) wire.LookupResult { return wire.LookupResult{Outcome: outcome} }
	if !validRef(ref) {
		return result(wire.LookupOutcomeInvalid)
	}
	spec, e := registry.ResolveRef(ctx, model.Ref{Registry: ref.Registry, Repo: ref.Repo, Revision: ref.Revision, Quant: ref.Quant, File: ref.File})
	if errors.Is(e, model.ErrMissingDigest) {
		return result(wire.LookupOutcomeUnsupportedMapping)
	}
	if e != nil {
		return result(wire.LookupOutcomeUnavailable)
	}
	if spec.Sink != (download.Sink{}) || spec.Request != "" || spec.Artifact.Digest == "" {
		return result(wire.LookupOutcomeUnsupportedMapping)
	}
	value := request.Request{Artifact: request.Artifact{Digest: spec.Artifact.Digest, Size: spec.Artifact.Size}}
	for _, source := range spec.Sources {
		if len(source.Attrs) > 0 || len(source.Headers) > 0 || source.Priority != 0 {
			return result(wire.LookupOutcomeUnsupportedMapping)
		}
		value.Sources = append(value.Sources, request.Source{Scheme: source.Scheme, Locator: source.Locator})
	}
	// Prepare is the existing pure portable-request validator. Its private sink
	// allocation is discarded; lookup never opens a store or submits this work.
	if _, e = (downloadserve.HTTPExecution{}).Prepare("model-lookup-validation", download.Kind, request.Encode(&value)); e != nil {
		return result(wire.LookupOutcomeUnsupportedMapping)
	}
	return wire.LookupResult{Outcome: wire.LookupOutcomeResolved, Request: &value}
}
func validRef(ref wire.Ref) bool {
	if strings.TrimSpace(ref.Registry) == "" || strings.TrimSpace(ref.Repo) == "" {
		return false
	}
	for _, s := range []string{ref.Registry, ref.Repo, ref.Revision, ref.Quant, ref.File} {
		if !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\r\n") {
			return false
		}
	}
	if ref.Registry == "ollama" && (ref.Quant != "" || ref.File != "") {
		return false
	}
	if ref.Registry == "hf" {
		if ref.Quant != "" && ref.File != "" {
			return false
		}
		if strings.ContainsAny(ref.Revision, "/\\?#") {
			return false
		}
	}
	return true
}
