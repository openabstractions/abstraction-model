package model

import (
	"context"
	"errors"
	"net/http"
	"strings"

	download "github.com/openabstractions/abstraction-download/go"
)

// CredentialHeaders applies a named credential to one registry request to host
// and returns the headers for exactly that request, or a
// *download.CredentialError. A resolving service supplies it for the caller it
// bound, through abstraction.credentials/applier@1 with consumer
// abstraction.model/resolver@1.
type CredentialHeaders func(ctx context.Context, name, host string) (map[string]string, error)

type credentialsKey struct{}

// WithCredentials returns ctx carrying the applier a registry provider uses for
// a Ref that names a credential. Without one such a Ref is refused.
func WithCredentials(ctx context.Context, apply CredentialHeaders) context.Context {
	return context.WithValue(ctx, credentialsKey{}, apply)
}

// applyCredential sets the named credential's headers on req, once, and makes
// the client drop them on a redirect to another host.
func applyCredential(ctx context.Context, client *http.Client, req *http.Request, name string) (*http.Client, error) {
	apply, _ := ctx.Value(credentialsKey{}).(CredentialHeaders)
	if apply == nil {
		return nil, download.CredentialRefusal(name, "forbidden")
	}
	headers, err := apply(ctx, name, strings.ToLower(req.URL.Hostname()))
	if err != nil {
		var typed *download.CredentialError
		if !errors.As(err, &typed) {
			return nil, download.CredentialRefusal(name, "unavailable")
		}
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	bound := *client
	next := bound.CheckRedirect
	origin := strings.ToLower(req.URL.Hostname())
	bound.CheckRedirect = func(to *http.Request, via []*http.Request) error {
		host := strings.ToLower(to.URL.Hostname())
		if host != origin && !strings.HasSuffix(host, "."+origin) {
			for k := range headers {
				to.Header.Del(k)
			}
		}
		if next != nil {
			return next(to, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &bound, nil
}
