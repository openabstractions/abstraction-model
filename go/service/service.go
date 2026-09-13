// Package service exposes configured model registries through shared framed IPC.
package service

import (
	"context"
	"errors"
	"github.com/openabstractions/abstraction-identity/listen"
	model "github.com/openabstractions/abstraction-model/go"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
	"os/user"
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
	OnError   func(error)
	// Assign before Serve. Called when admission stops, before calls drain.
	OnStopped func()
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
	l, e := listen.Listen(endpoint)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{listener: l, owner: owner.Uid, registry: registry, ctx: ctx, cancel: cancel, slots: make(chan struct{}, 32)}, nil
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
	return lookup(r.ctx, r.host.registry, ref), nil
}
