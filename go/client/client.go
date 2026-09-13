// Package client binds an explicitly selected model lookup service endpoint.
package client

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/openabstractions/abstraction-identity/listen"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
	"net/url"
	"strings"
	"time"
)

type Ref = wire.Ref
type LookupResult = wire.LookupResult
type Client struct{ transport listen.FrameClient }

func New(endpoint string) *Client {
	return NewWithTransport(listen.FrameClient{Endpoint: endpoint})
}

// NewWithTransport retains the caller's endpoint, server trust and waiting limits.
func NewWithTransport(transport listen.FrameClient) *Client {
	return &Client{transport: transport.WithDefaults(5*time.Second, 1<<20)}
}
func (c *Client) Resolve(ref Ref) (LookupResult, error) {
	return c.ResolveContext(context.Background(), ref)
}

// ResolveContext retains no context between calls. Waiting cancellation does not
// classify a registry lookup as permanent and does not submit or cancel work.
func (c *Client) ResolveContext(ctx context.Context, ref Ref) (LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return LookupResult{}, err
	}
	r, e := wire.NewModelResolverClient(c.transport.WithContext(ctx)).Resolve(ref)
	if e != nil {
		return LookupResult{}, e
	}
	if !validResult(r) {
		return LookupResult{}, errors.New("model: inconsistent lookup result")
	}
	return r, nil
}

// The service's result invariant is checked before exposing a download request.
func validResult(r LookupResult) bool {
	if (r.Outcome == wire.LookupOutcomeResolved) != (r.Request != nil) {
		return false
	}
	if r.Request == nil {
		return true
	}
	v := r.Request
	digest, e := hex.DecodeString(strings.TrimPrefix(v.Artifact.Digest, "sha256:"))
	if e != nil || !strings.HasPrefix(v.Artifact.Digest, "sha256:") || len(digest) != 32 || v.Artifact.Size < 0 || len(v.Sources) == 0 {
		return false
	}
	for _, s := range v.Sources {
		u, e := url.Parse(s.Locator)
		if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Scheme != s.Scheme {
			return false
		}
	}
	return true
}
