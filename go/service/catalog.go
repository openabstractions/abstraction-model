package service

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
	modelidentity "github.com/openabstractions/abstraction-model/identity"
	"github.com/openabstractions/abstraction-storage/go/digest"
)

// catalogDocument is the release-curated catalogue (MODEL-K1), compiled in;
// slice 1 of the setup step fetches no newer copy.
//
//go:embed catalog.json
var catalogDocument []byte

// fitMargin is llama-server's own per-device margin in the fit rule
// (research/first-run/DECISION.md §3).
const fitMargin = 1 << 30

// CatalogSources is what Fit reads besides the compiled-in catalogue. The
// runtime binds both reads to its own services; neither is asked of a
// registry or a weight file.
type CatalogSources struct {
	// Pool answers the free bytes the fit rule compares against:
	// abstraction.resource/table@1's card:0 capacity minus held, or system
	// memory when the card reports no capacity.
	Pool func(ctx context.Context) (int64, error)
	// Served lists the families runtimes already serve for one router
	// profile, from abstraction.router/router@1 Offer, paired runtimes
	// included and hosted (cloud) servers left out.
	Served func(ctx context.Context, profile string) ([]ServedFamily, error)
}

// ServedFamily is one family a runtime serves now. Machine is empty for this
// computer and names the paired runtime otherwise.
type ServedFamily struct {
	Family  string
	Machine string
}

type catalogEntry struct {
	Title                string
	Source               wire.Ref
	Digest               string
	SizeBytes            int64
	KVBytesPerToken      int64
	StartupResidualBytes int64
	// TrainedContext is the most tokens one conversation may hold; zero
	// leaves the use's context as the only size.
	TrainedContext int64
	// ConversationSlots is the most conversations the engine keeps resident
	// at once, each with its own context; zero reads as one.
	ConversationSlots int
	License           string
	Gated             bool
	family            string
}

func (e catalogEntry) need(window int64) (int64, bool) {
	if window <= 0 || e.SizeBytes <= 0 || e.KVBytesPerToken <= 0 || e.StartupResidualBytes < 0 {
		return 0, false
	}
	remaining := int64(math.MaxInt64) - fitMargin
	if e.SizeBytes > remaining {
		return 0, false
	}
	remaining -= e.SizeBytes
	if e.StartupResidualBytes > remaining {
		return 0, false
	}
	remaining -= e.StartupResidualBytes
	if e.KVBytesPerToken > remaining/window {
		return 0, false
	}
	return e.SizeBytes + e.KVBytesPerToken*window + e.StartupResidualBytes + fitMargin, true
}

type catalogRef struct {
	Registry string `json:"registry"`
	Repo     string `json:"repo"`
	Revision string `json:"revision"`
	Quant    string `json:"quant"`
	File     string `json:"file"`
}

type catalog struct {
	Version string                     `json:"version"`
	Context map[string]int64           `json:"context"`
	Uses    map[string][]string        `json:"uses"`
	Entries map[string]json.RawMessage `json:"entries"`
	entries map[string]catalogEntry
}

// FamilyTitleLookup returns an immutable display lookup from the bundled
// catalogue. Unknown families and conflicting titles answer empty. The router
// normalizes the supplied text for its bounded display-title wire field.
// Family identity and catalogue ranking are unchanged.
func FamilyTitleLookup() func(string) string {
	c, err := parseCatalog(catalogDocument)
	if err != nil {
		return func(string) string { return "" }
	}
	return c.familyTitleLookup()
}

func (c catalog) familyTitleLookup() func(string) string {
	titles := make(map[string]string)
	for _, entry := range c.entries {
		title := strings.TrimSpace(entry.Title)
		if previous, found := titles[entry.family]; found && previous != title {
			title = ""
		}
		titles[entry.family] = title
	}
	return func(family string) string { return titles[family] }
}

func parseCatalog(raw []byte) (catalog, error) {
	var c catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		return catalog{}, err
	}
	if c.Version == "" || len(c.Version) > 32 {
		return catalog{}, errors.New("catalog: version required")
	}
	c.entries = map[string]catalogEntry{}
	for id, body := range c.Entries {
		var e struct {
			Title                string     `json:"title"`
			Source               catalogRef `json:"source"`
			Digest               string     `json:"digest"`
			SizeBytes            int64      `json:"size_bytes"`
			KVBytesPerToken      int64      `json:"kv_bytes_per_token"`
			StartupResidualBytes int64      `json:"startup_residual_bytes"`
			TrainedContext       int64      `json:"trained_context"`
			ConversationSlots    int        `json:"conversation_slots"`
			License              string     `json:"license"`
			Gated                bool       `json:"gated"`
		}
		if err := json.Unmarshal(body, &e); err != nil {
			return catalog{}, fmt.Errorf("catalog entry %s: %w", id, err)
		}
		if e.Title == "" || e.Source.Registry == "" || e.Source.Repo == "" || e.Source.Revision == "" || e.Source.File == "" ||
			!digest.Valid(e.Digest) || e.SizeBytes <= 0 || e.KVBytesPerToken <= 0 || e.StartupResidualBytes < 0 ||
			e.TrainedContext < 0 || e.ConversationSlots < 0 || e.ConversationSlots > maxConversationSlots {
			return catalog{}, fmt.Errorf("catalog entry %s is incomplete", id)
		}
		c.entries[id] = catalogEntry{Title: e.Title, Source: wire.Ref{Registry: e.Source.Registry, Repo: e.Source.Repo, Revision: e.Source.Revision, Quant: e.Source.Quant, File: e.Source.File},
			Digest: e.Digest, SizeBytes: e.SizeBytes, KVBytesPerToken: e.KVBytesPerToken, StartupResidualBytes: e.StartupResidualBytes,
			TrainedContext: e.TrainedContext, ConversationSlots: e.ConversationSlots, License: e.License, Gated: e.Gated,
			family: modelidentity.Family(e.Source.File)}
	}
	for use, ids := range c.Uses {
		if _, ok := wire.ParseCatalogUse(use); !ok {
			return catalog{}, fmt.Errorf("catalog use %q is not a CatalogUse", use)
		}
		if c.Context[use] <= 0 {
			return catalog{}, fmt.Errorf("catalog use %q has no context length", use)
		}
		for _, id := range ids {
			if _, ok := c.entries[id]; !ok {
				return catalog{}, fmt.Errorf("catalog use %q names unknown entry %q", use, id)
			}
			if _, ok := c.entries[id].needFor(c.Context[use], c.entries[id].slots()); !ok {
				return catalog{}, fmt.Errorf("catalog entry %s need overflows at use %s", id, use)
			}
		}
	}
	return c, nil
}

// useProfile is the router profile that serves a use: code and chat are both
// answered by the chat profile.
func useProfile(use wire.CatalogUse) string {
	switch use {
	case wire.CatalogUseEmbed:
		return "embed"
	case wire.CatalogUseImage:
		return "image"
	case wire.CatalogUseVideo:
		return "video"
	}
	return "chat"
}

// FitCatalog answers catalog@1 Fit for use from the compiled-in catalogue and
// the two reads in sources. It reads no weight file and asks no registry
// (MODEL-K2). A gated entry never appears (DR-16). An entry whose family a
// runtime already serves is served there with nothing to download, this
// computer before a paired one; every other entry needs a download and fits
// when size, KV cache at the use's context in each conversation slot, the
// recorded startup residual and the 1 GiB margin are within the free pool.
func FitCatalog(ctx context.Context, use wire.CatalogUse, sources CatalogSources) wire.FitResult {
	c, err := parseCatalog(catalogDocument)
	if err != nil || !use.Known() {
		return wire.FitResult{Outcome: wire.FitOutcomeInvalid, Entries: []wire.FitEntry{}}
	}
	ids, listed := c.Uses[use.String()]
	if !listed {
		return wire.FitResult{Outcome: wire.FitOutcomeInvalid, Entries: []wire.FitEntry{}}
	}
	if sources.Pool == nil || sources.Served == nil {
		return wire.FitResult{Outcome: wire.FitOutcomeUnavailable, Entries: []wire.FitEntry{}}
	}
	pool, err := sources.Pool(ctx)
	if err != nil || pool < 0 {
		return wire.FitResult{Outcome: wire.FitOutcomeUnavailable, Entries: []wire.FitEntry{}}
	}
	served, err := sources.Served(ctx, useProfile(use))
	if err != nil {
		return wire.FitResult{Outcome: wire.FitOutcomeUnavailable, Entries: []wire.FitEntry{}}
	}
	window := c.Context[use.String()]
	out := wire.FitResult{Outcome: wire.FitOutcomeFitted, CatalogVersion: c.Version, PoolBytes: pool, Entries: []wire.FitEntry{}}
	for _, id := range ids {
		e := c.entries[id]
		if e.Gated {
			continue
		}
		need, _ := e.needFor(window, e.slots()) // parseCatalog checked every entry at this use's context.
		row := wire.FitEntry{Title: e.Title, Family: e.family, Fit: wire.FitNo, Availability: wire.AvailabilityDownload,
			DownloadBytes: e.SizeBytes, NeedBytes: need, Context: window, License: e.License, Source: e.Source, Digest: e.Digest}
		if need <= pool {
			row.Fit = wire.FitFits
		}
		if machine, ok := servedOn(served, e.family); ok {
			row.Availability, row.Machine, row.DownloadBytes = wire.AvailabilityServed, machine, 0
			if machine != "" {
				// A paired runtime's memory is its own; this pool says nothing about it.
				row.Fit = wire.FitFits
			}
		}
		out.Entries = append(out.Entries, row)
		if len(out.Entries) == 64 {
			break
		}
	}
	return out
}

// servedOn names where family is served: this computer ("") before any
// paired runtime, then the first paired runtime in Offer's order.
func servedOn(served []ServedFamily, family string) (string, bool) {
	machine, found := "", false
	for _, s := range served {
		if !modelidentity.Same(s.Family, family) && modelidentity.Family(s.Family) != family {
			continue
		}
		if s.Machine == "" {
			return "", true
		}
		if !found {
			machine, found = s.Machine, true
		}
	}
	return machine, found
}

// EnableCatalog serves abstraction.model/catalog@1 beside resolver@1 on this
// endpoint. policy authorizes each Fit for the rechecked caller like router@1
// Offer; wrap ErrPolicyUnavailable when the decision cannot be obtained.
// Configure it before Serve.
func (h *Host) EnableCatalog(sources CatalogSources, policy func(context.Context, *identity.Peer) error) error {
	h.lifecycle.Lock()
	defer h.lifecycle.Unlock()
	if h.serving || h.ctx.Err() != nil {
		return errors.New("model service: configure the catalogue before Serve")
	}
	if sources.Pool == nil || sources.Served == nil || policy == nil {
		return errors.New("model service: catalogue sources and policy required")
	}
	if _, err := parseCatalog(catalogDocument); err != nil {
		return err
	}
	h.catalog = &catalogBinding{sources: sources, policy: policy}
	return nil
}

type catalogBinding struct {
	sources CatalogSources
	policy  func(context.Context, *identity.Peer) error
}

type catalogReceiver struct {
	host *Host
	call callerEvidence
	ctx  context.Context
}

func (r *catalogReceiver) Fit(use wire.CatalogUse) (wire.FitResult, error) {
	refused := func(outcome wire.FitOutcome) (wire.FitResult, error) {
		return wire.FitResult{Outcome: outcome, Entries: []wire.FitEntry{}}, nil
	}
	resolver := receiver{host: r.host, call: r.call, ctx: r.ctx}
	if !resolver.authorized() || r.call.Recheck() != nil {
		return refused(wire.FitOutcomeForbidden)
	}
	r.host.lifecycle.Lock()
	binding := r.host.catalog
	r.host.lifecycle.Unlock()
	if binding == nil {
		return refused(wire.FitOutcomeUnavailable)
	}
	peer, err := r.call.Peer()
	if err != nil {
		return refused(wire.FitOutcomeForbidden)
	}
	if err := binding.policy(r.ctx, peer); err != nil {
		if r.ctx.Err() != nil || errors.Is(err, ErrPolicyUnavailable) {
			return refused(wire.FitOutcomeUnavailable)
		}
		return refused(wire.FitOutcomeForbidden)
	}
	return FitCatalog(r.ctx, use, binding.sources), nil
}

// exchange answers one frame: catalog@1 when it is enabled, else resolver@1
// alone as before.
func (h *Host) exchange(ctx context.Context, call *listen.FramedCall) ([]byte, error) {
	resolver := &wire.ModelResolverDispatcher{Handler: &receiver{host: h, call: call, ctx: ctx}}
	h.lifecycle.Lock()
	enabled := h.catalog != nil
	h.lifecycle.Unlock()
	if !enabled {
		return resolver.ExchangeFrame(call.Frame)
	}
	catalog := &wire.ModelCatalogDispatcher{Handler: &catalogReceiver{host: h, call: call, ctx: ctx}}
	return wire.ServeEndpoint(call.Frame, "", "", resolver, catalog)
}
