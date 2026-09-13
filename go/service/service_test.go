package service

import (
	"context"
	"errors"
	"fmt"
	download "github.com/openabstractions/abstraction-download/go"
	"github.com/openabstractions/abstraction-identity/listen"
	model "github.com/openabstractions/abstraction-model/go"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
	"github.com/openabstractions/abstraction-model/go/client"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var serial atomic.Uint64

type resolverFunc struct {
	fn func(context.Context, model.Ref) (download.Spec, error)
}

func (resolverFunc) Registry() string { return "fixture" }
func (r resolverFunc) Resolve(ctx context.Context, ref model.Ref) (download.Spec, error) {
	return r.fn(ctx, ref)
}
func fixtureSpec() download.Spec {
	return download.Spec{Artifact: download.Artifact{Digest: "sha256:" + strings.Repeat("a", 64), Size: 12}, Sources: []download.Source{{Scheme: "https", Locator: "https://example.invalid/model.gguf"}}}
}
func liveHost(t *testing.T, reg *model.Registry, wrongUser bool) (*Host, *client.Client) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("Program process/path evidence is not implemented on macOS")
	}
	endpoint := listen.Endpoint(fmt.Sprintf("model-test-%d-%d", os.Getpid(), serial.Add(1)))
	h, e := Listen(endpoint, reg)
	if e != nil {
		t.Fatal(e)
	}
	if wrongUser {
		h.owner = "different-principal"
	}
	done := make(chan error, 1)
	go func() { done <- h.Serve(context.Background()) }()
	t.Cleanup(func() {
		h.Close()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(2 * time.Second):
			t.Error("model host failed to stop")
		}
	})
	return h, client.New(endpoint)
}
func TestGeneratedModelLookupPreservesTypedReference(t *testing.T) {
	want := model.Ref{Registry: "fixture", Repo: "opaque", Revision: "r1", Quant: "q", File: "f"}
	var calls atomic.Int32
	reg := model.NewServiceRegistry(resolverFunc{func(ctx context.Context, ref model.Ref) (download.Spec, error) {
		calls.Add(1)
		if ref != want {
			return download.Spec{}, errors.New("reference changed")
		}
		return fixtureSpec(), nil
	}})
	_, c := liveHost(t, reg, false)
	r, e := c.ResolveContext(context.Background(), wire.Ref{Registry: want.Registry, Repo: want.Repo, Revision: want.Revision, Quant: want.Quant, File: want.File})
	if e != nil || r.Outcome != wire.LookupOutcomeResolved || r.Request == nil {
		t.Fatalf("lookup: %+v %v", r, e)
	}
	if calls.Load() != 1 || r.Request.Artifact.Digest != fixtureSpec().Artifact.Digest || r.Request.Sources[0].Locator != fixtureSpec().Sources[0].Locator {
		t.Fatal("mapping changed")
	}
}
func TestGeneratedLookupRefusesWholeNonportableMapping(t *testing.T) {
	cases := map[string]func(*download.Spec){"credential": func(s *download.Spec) { s.Sources[0].Attrs = map[string]string{"credential": "hf"} }, "headers": func(s *download.Spec) { s.Sources[0].Headers = map[string]string{"X-Mode": "required"} }, "priority": func(s *download.Spec) { s.Sources[0].Priority = 1 }, "local": func(s *download.Spec) {
		s.Sources = append(s.Sources, download.Source{Scheme: "file", Locator: "private-path"})
	}, "sink": func(s *download.Spec) { s.Sink.Final = "private-destination" }, "handoff": func(s *download.Spec) { s.Request = "owned-handoff" }, "digest": func(s *download.Spec) { s.Artifact.Digest = "sha256:invalid" }, "missing_digest": func(s *download.Spec) { s.Artifact.Digest = "" }, "negative_size": func(s *download.Spec) { s.Artifact.Size = -1 }, "url_credentials": func(s *download.Spec) { s.Sources[0].Locator = "https://user:secret@example.invalid/file" }}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			spec := fixtureSpec()
			mutate(&spec)
			_, c := liveHost(t, model.NewServiceRegistry(resolverFunc{func(context.Context, model.Ref) (download.Spec, error) { return spec, nil }}), false)
			r, e := c.Resolve(wire.Ref{Registry: "fixture", Repo: "x"})
			if e != nil || r.Outcome != wire.LookupOutcomeUnsupportedMapping || r.Request != nil {
				t.Fatalf("silently projected mapping: %+v %v", r, e)
			}
		})
	}
}
func TestModelAuthorizationAndWaitingBeforeEffects(t *testing.T) {
	var calls atomic.Int32
	reg := model.NewServiceRegistry(resolverFunc{func(context.Context, model.Ref) (download.Spec, error) { calls.Add(1); return fixtureSpec(), nil }})
	_, forbidden := liveHost(t, reg, true)
	r, e := forbidden.Resolve(wire.Ref{Registry: "fixture", Repo: "x"})
	if e != nil || r.Outcome != wire.LookupOutcomeForbidden || r.Request != nil || calls.Load() != 0 {
		t.Fatalf("authorization: %+v %v calls%d", r, e, calls.Load())
	}
	_, c := liveHost(t, reg, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = c.ResolveContext(ctx, wire.Ref{Registry: "fixture", Repo: "x"}); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if calls.Load() != 0 {
		t.Fatal("expired request reached provider")
	}
	r, e = c.Resolve(wire.Ref{Registry: "fixture", Repo: "x"})
	if e != nil || r.Outcome != wire.LookupOutcomeResolved || calls.Load() != 1 {
		t.Fatal("client did not remain reusable", r, e)
	}
}
func TestUnavailableAndInvalidAreObservedResults(t *testing.T) {
	_, c := liveHost(t, model.NewServiceRegistry(), false)
	for _, tc := range []struct {
		ref     wire.Ref
		outcome string
	}{{wire.Ref{}, wire.LookupOutcomeInvalid}, {wire.Ref{Registry: "missing", Repo: "x"}, wire.LookupOutcomeUnavailable}} {
		r, e := c.Resolve(tc.ref)
		if e != nil || r.Outcome != tc.outcome || r.Request != nil {
			t.Fatal(r, e)
		}
	}
	if _, e := Listen("must-not-open", nil); e == nil {
		t.Fatal("nil provider accepted")
	}
}
func TestExistingHFProviderThroughGeneratedIPC(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/models/org/repo/revision/main" {
			t.Errorf("unexpected lookup %s", r.URL.Path)
		}
		fmt.Fprintf(w, `{"sha":"pinned","siblings":[{"rfilename":"weights.gguf","size":12,"lfs":{"sha256":"%s","size":12}}]}`, strings.Repeat("a", 64))
	}))
	defer server.Close()
	_, c := liveHost(t, model.NewServiceRegistry(model.HF{BaseURL: server.URL, Client: server.Client()}), false)
	r, e := c.Resolve(wire.Ref{Registry: "hf", Repo: "org/repo"})
	if e != nil || r.Outcome != wire.LookupOutcomeResolved || r.Request == nil || requests.Load() != 1 {
		t.Fatal(r, e)
	}
	if r.Request.Sources[0].Locator != server.URL+"/org/repo/resolve/pinned/weights.gguf" {
		t.Fatal("lost pinned revision", r.Request)
	}
}
