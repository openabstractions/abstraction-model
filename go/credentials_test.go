package model

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type redirectTransport func(*http.Request) (*http.Response, error)

func (f redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestAppliedCredentialStopsBeforeRedirect(t *testing.T) {
	const secret = "fixture-model-credential"
	for _, next := range []struct {
		name, location string
	}{
		{"same host", "/next"},
		{"different port", "https://registry.example.test:9443/next"},
		{"subdomain", "https://sub.registry.example.test/next"},
		{"scheme downgrade", "http://registry.example.test/next"},
		{"different host", "https://other.example.test/next"},
	} {
		t.Run(next.name, func(t *testing.T) {
			var requests, applications int
			base := &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
				requests++
				if requests == 1 {
					if got := req.Header.Get("X-Api-Key"); got != secret {
						t.Errorf("origin credential = %q", got)
					}
					return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {next.location}},
						Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
				}
				if got := req.Header.Get("X-Api-Key"); got != "" {
					t.Errorf("redirect carried credential %q", got)
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader("followed")), Request: req}, nil
			})}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://registry.example.test/start", nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithCredentials(req.Context(), func(_ context.Context, name, host string) (map[string]string, error) {
				applications++
				if name != "model-key" || host != "registry.example.test" {
					t.Errorf("applier requested %q at %q", name, host)
				}
				return map[string]string{"X-Api-Key": secret}, nil
			})
			client, err := applyCredential(ctx, base, req, "model-key")
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusFound || requests != 1 || applications != 1 {
				t.Fatalf("status=%d requests=%d applications=%d; credentialed redirect must stop", resp.StatusCode, requests, applications)
			}
		})
	}
}

func TestCredentialApplyFailureSendsNoRequest(t *testing.T) {
	called := false
	base := &http.Client{Transport: redirectTransport(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected request")
	})}
	req, err := http.NewRequest(http.MethodGet, "https://registry.example.test/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithCredentials(req.Context(), func(context.Context, string, string) (map[string]string, error) {
		return nil, errors.New("applier unavailable")
	})
	_, err = applyCredential(ctx, base, req, "model-key")
	if err == nil || called {
		t.Fatalf("apply failure: err=%v request sent=%v", err, called)
	}
}

func TestUncredentialedModelClientStillFollowsRedirect(t *testing.T) {
	var requests int
	base := &http.Client{Transport: redirectTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		switch req.URL.Path {
		case "/credentialed":
			if got := req.Header.Get("X-Api-Key"); got != "fixture-model-credential" {
				t.Errorf("credentialed origin header = %q", got)
			}
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"/credentialed-next"}},
				Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		case "/start":
			if got := req.Header.Get("X-Api-Key"); got != "" {
				t.Errorf("uncredentialed origin carried %q", got)
			}
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"/next"}},
				Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
		case "/next":
			if got := req.Header.Get("X-Api-Key"); got != "" {
				t.Errorf("uncredentialed redirect carried %q", got)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader("followed")), Request: req}, nil
		}
		return nil, errors.New("unexpected redirect request")
	})}
	credentialed, err := http.NewRequest(http.MethodGet, "https://registry.example.test/credentialed", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithCredentials(credentialed.Context(), func(context.Context, string, string) (map[string]string, error) {
		return map[string]string{"X-Api-Key": "fixture-model-credential"}, nil
	})
	credentialedClient, err := applyCredential(ctx, base, credentialed, "model-key")
	if err != nil {
		t.Fatal(err)
	}
	credentialedResponse, err := credentialedClient.Do(credentialed)
	if err != nil {
		t.Fatal(err)
	}
	credentialedResponse.Body.Close()
	if credentialedResponse.StatusCode != http.StatusFound || requests != 1 {
		t.Fatalf("credentialed status=%d requests=%d", credentialedResponse.StatusCode, requests)
	}
	req, err := http.NewRequest(http.MethodGet, "https://registry.example.test/start", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := base.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || requests != 3 {
		t.Fatalf("status=%d requests=%d", resp.StatusCode, requests)
	}
}
