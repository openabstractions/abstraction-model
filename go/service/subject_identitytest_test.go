//go:build oa_identitytest

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/identitytest"
	model "github.com/openabstractions/abstraction-model/go"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
)

type subjectCall struct {
	peer     *identity.Peer
	rechecks int
}

func (c *subjectCall) Peer() (*identity.Peer, error) { return c.peer, nil }
func (c *subjectCall) Recheck() error                { c.rechecks++; return nil }

type subjectTransport func(*http.Request) (*http.Response, error)

func (f subjectTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type subjectFixtureFailure struct{ testing.TB }

func (subjectFixtureFailure) Fatal(args ...any) { panic(fmt.Sprint(args...)) }

func TestSubjectIdentityFixtureReentryRefuses(t *testing.T) {
	path := subjectImage(t, t.TempDir())
	facts := identitytest.Facts{User: identity.User{Kind: "windows", SID: "fixture-owner"}, Path: path,
		Package: "SubjectFixture_1.0.0.0_x64__pzs8sxrjxfjjc", PackageRoot: filepath.Dir(path)}
	peer := identitytest.New(t, facts)[0]
	if peer.Process.Known() || peer.Code.Known() {
		t.Fatal("fixture supplies native process or code authority")
	}
	defer func() {
		if got := recover(); got != "identitytest: package resolver already in use" {
			t.Fatalf("expected prompt package-resolver refusal, got %v", got)
		}
	}()
	identitytest.New(subjectFixtureFailure{t}, facts)
}

func subjectImage(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "app.exe")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func resolveSubjectCredential(t *testing.T, peer *identity.Peer, owner, expected string) {
	t.Helper()
	applies, requests := 0, 0
	transport := subjectTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "fixture-grant" {
			t.Fatal("credential was not applied")
		}
		body := fmt.Sprintf(`{"sha":"pinned","siblings":[{"rfilename":"weights.gguf","size":12,"lfs":{"sha256":"%s","size":12}}]}`, strings.Repeat("a", 64))
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	h := &Host{owner: owner, registry: model.NewServiceRegistry(model.HF{BaseURL: "https://fixture.invalid", Client: &http.Client{Transport: transport}})}
	h.apply = func(_ context.Context, account, program, name, host string) (map[string]string, error) {
		applies++
		if account != owner || program != expected || name != "named-fixture" || host != "fixture.invalid" {
			return nil, errors.New("credential grant does not name caller")
		}
		return map[string]string{"Authorization": "fixture-grant"}, nil
	}
	call := &subjectCall{peer: peer}
	r := &receiver{host: h, call: call, ctx: context.Background()}
	result, err := r.Resolve(wire.Ref{Registry: "hf", Repo: "org/repo", Credential: "named-fixture"})
	if err != nil || result.Outcome != wire.LookupOutcomeResolved || applies != 1 || requests != 1 || call.rechecks != 1 {
		t.Fatalf("Resolve=%+v,%v applies=%d requests=%d rechecks=%d", result, err, applies, requests, call.rechecks)
	}
}

func TestSubjectIdentityPackagedCredential(t *testing.T) {
	root := t.TempDir()
	path := subjectImage(t, filepath.Join(root, "v1"))
	peer := identitytest.New(t, identitytest.Facts{User: identity.User{Kind: "windows", SID: "fixture-owner"}, Path: path, Package: "SubjectFixture_1.0.0.0_x64__pzs8sxrjxfjjc", PackageRoot: filepath.Dir(path)})[0]
	expected, err := identity.SubjectProgram(peer, identity.ProofBound)
	if err != nil || expected != "msix:SubjectFixture_pzs8sxrjxfjjc" {
		t.Fatal(expected, err)
	}
	resolveSubjectCredential(t, peer, "fixture-owner", expected)
}

func TestSubjectIdentityUnknownAccountsRefused(t *testing.T) {
	path := subjectImage(t, t.TempDir())
	for _, tc := range []struct {
		name  string
		user  identity.User
		owner string
	}{
		{"negative uid", identity.User{Kind: "posix", UID: -1}, "-1"},
		{"empty sid", identity.User{Kind: "windows"}, ""},
		{"unknown kind", identity.User{Kind: "other", UID: 7}, "7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peer := identitytest.New(t, identitytest.Facts{User: tc.user, Path: path})[0]
			calls := 0
			h := &Host{owner: tc.owner, registry: model.NewServiceRegistry(), apply: func(context.Context, string, string, string, string) (map[string]string, error) {
				calls++
				return nil, nil
			}}
			r := &receiver{host: h, call: &subjectCall{peer: peer}, ctx: context.Background()}
			result, err := r.Resolve(wire.Ref{Registry: "hf", Repo: "org/repo", Credential: "named-fixture"})
			if err != nil || result.Outcome != wire.LookupOutcomeForbidden || calls != 0 {
				t.Fatal(result, err, calls)
			}
		})
	}
}
