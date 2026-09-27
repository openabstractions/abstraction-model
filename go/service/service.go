// Package service exposes configured model registries through shared framed IPC.
package service

import (
	"context"
	"errors"
	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	model "github.com/openabstractions/abstraction-model/go"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const MaxFrameBytes = 1 << 20

type Host struct {
	lifecycle sync.Mutex
	serving   bool
	listener  listen.Listener
	owner     string
	registry  *model.Registry
	ctx       context.Context
	cancel    context.CancelFunc
	once      sync.Once
	workers   sync.WaitGroup
	slots     chan struct{}
	policy    LookupPolicy
	apply     CredentialApplier
	OnError   func(error)
	// Assign before Serve. Called when admission stops, before calls drain.
	OnStopped func()
}

// LookupPolicy authorizes a lookup in one registry for the rechecked receiving
// peer, after same-account proof and before any registry provider is asked. It
// must honor ctx and be safe for concurrent calls. Wrap ErrPolicyUnavailable
// when the decision cannot be obtained; every other error is a refusal.
type LookupPolicy func(ctx context.Context, peer *identity.Peer, registry string) error

// ErrPolicyUnavailable distinguishes a failed decision lookup from refusal.
var ErrPolicyUnavailable = errors.New("model service: lookup policy unavailable")

// EnablePolicy narrows lookup to callers the policy authorizes per registry.
// Configure it before Serve. Refusal returns forbidden and a failed decision
// returns unavailable; neither reaches a registry provider.
func (h *Host) EnablePolicy(policy LookupPolicy) error {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	if h.serving || h.ctx.Err() != nil {
		return errors.New("model service: configure policy before Serve")
	}
	if policy == nil {
		return errors.New("model service: explicit policy required")
	}
	h.policy = policy
	return nil
}

// CredentialApplier applies a named credential for the caller this service
// bound, to one registry request to host, through
// abstraction.credentials/applier@1 with consumer abstraction.model/resolver@1.
// account and program are the Program-proven receiving subject. It returns the
// headers for that request or a *download.CredentialError, honors ctx and is
// safe for concurrent calls.
type CredentialApplier func(ctx context.Context, account, program, name, host string) (map[string]string, error)

// EnableCredentials lets a Ref name a credential. Configure it before Serve.
// Without it every Ref naming a credential reads unsupported_mapping.
func (h *Host) EnableCredentials(apply CredentialApplier) error {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	if h.serving || h.ctx.Err() != nil {
		return errors.New("model service: configure credentials before Serve")
	}
	if apply == nil {
		return errors.New("model service: explicit credential applier required")
	}
	h.apply = apply
	return nil
}

// Listen requires explicit provider configuration. It performs no discovery.
func Listen(endpoint string, registry *model.Registry) (*Host, error) {
	if registry == nil {
		return nil, errors.New("model: configured registry required")
	}
	owner, e := user.Current()
	if e != nil {
		return nil, e
	}
	if owner.Uid == "" {
		return nil, errors.New("model: service principal unavailable")
	}
	l, e := listen.ListenFramed(endpoint, listen.Program)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{listener: listen.Sessions(l, listen.SessionOptions{MaxSessions: 32}), owner: owner.Uid, registry: registry, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 32)}, nil
}
func (h *Host) Close() error {
	var e error
	h.once.Do(func() { h.cancel(); e = h.listener.Close() })
	return e
}
func (h *Host) Serve(ctx context.Context) error {
	h.lifecycle.Lock()
	if h.serving {
		h.lifecycle.Unlock()
		return errors.New("model: host already served")
	}
	h.serving = true
	h.lifecycle.Unlock()
	stop := context.AfterFunc(ctx, func() { h.Close() })
	defer stop()
	defer h.workers.Wait()
	defer func() {
		if h.OnStopped != nil {
			h.OnStopped()
		}
	}()
	defer h.Close()
	for {
		conn, e := h.listener.Accept()
		if e != nil {
			if ctx.Err() != nil || h.ctx.Err() != nil {
				return nil
			}
			return e
		}
		select {
		case h.slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		h.workers.Add(1)
		go func() {
			defer h.workers.Done()
			defer func() { <-h.slots }()
			defer conn.Close()
			callCtx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
			defer cancel()
			call, e := listen.ReceiveFramed(callCtx, conn, listen.Program, MaxFrameBytes)
			if call != nil {
				defer call.Close()
			}
			if e == nil {
				dispatcher := wire.ModelResolverDispatcher{Handler: &receiver{host: h, call: call, ctx: callCtx}}
				var reply []byte
				reply, e = dispatcher.ExchangeFrame(call.Frame)
				if e == nil {
					e = call.Reply(reply)
				}
			}
			if e != nil && h.OnError != nil && h.ctx.Err() == nil {
				h.OnError(e)
			}
		}()
	}
}

type receiver struct {
	host *Host
	call *listen.FramedCall
	ctx  context.Context
}

func (r *receiver) authorized() bool {
	p, e := r.call.Peer()
	if e != nil {
		return false
	}
	u, e := p.User.AtLeast(listen.Program.User)
	if e != nil {
		return false
	}
	principal := ""
	if u.Kind == "windows" {
		principal = u.SID
	} else if u.Kind == "posix" {
		principal = strconv.Itoa(u.UID)
	}
	return principal != "" && principal == r.host.owner
}
func (r *receiver) Resolve(ref wire.Ref) (wire.LookupResult, error) {
	if !r.authorized() {
		return wire.LookupResult{Outcome: wire.LookupOutcomeForbidden}, nil
	}
	r.host.lifecycle.Lock()
	policy, apply := r.host.policy, r.host.apply
	r.host.lifecycle.Unlock()
	if policy != nil {
		peer, err := r.call.Peer()
		if err != nil {
			return wire.LookupResult{Outcome: wire.LookupOutcomeForbidden}, nil
		}
		err = policy(r.ctx, peer, ref.Registry)
		if r.ctx.Err() != nil || errors.Is(err, ErrPolicyUnavailable) {
			return wire.LookupResult{Outcome: wire.LookupOutcomeUnavailable}, nil
		}
		if err != nil {
			return wire.LookupResult{Outcome: wire.LookupOutcomeForbidden}, nil
		}
	}
	ctx := r.ctx
	if ref.Credential != "" && apply != nil {
		account, program, ok := r.subject()
		if !ok {
			return wire.LookupResult{Outcome: wire.LookupOutcomeForbidden}, nil
		}
		ctx = model.WithCredentials(ctx, func(ctx context.Context, name, host string) (map[string]string, error) {
			return apply(ctx, account, program, name, host)
		})
	}
	return lookup(ctx, r.host.registry, ref), nil
}

// subject is the Program-proven account and executable of the rechecked peer.
func (r *receiver) subject() (string, string, bool) {
	p, e := r.call.Peer()
	if e != nil || r.call.Recheck() != nil {
		return "", "", false
	}
	u, e := p.User.AtLeast(listen.Program.User)
	if e != nil {
		return "", "", false
	}
	account := ""
	if u.Kind == "windows" {
		account = u.SID
	} else if u.Kind == "posix" && u.UID >= 0 {
		account = strconv.Itoa(u.UID)
	}
	program, e := p.Path.AtLeast(listen.Program.Path)
	if e != nil || account == "" || account != r.host.owner || !filepath.IsAbs(program) {
		return "", "", false
	}
	return account, filepath.Clean(program), true
}
