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

// applyCredential sets the named credential's headers on one registry request.
// A redirect needs a new target decision, so this client stops before sending
// another request with headers approved for the original target.
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
	bound.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &bound, nil
}
