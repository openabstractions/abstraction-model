package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"testing"
	"time"

	download "github.com/openabstractions/abstraction-download/go"
	identity "github.com/openabstractions/abstraction-identity"
	"github.com/openabstractions/abstraction-identity/listen"
	model "github.com/openabstractions/abstraction-model/go"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
	"github.com/openabstractions/abstraction-model/go/client"
	modelidentity "github.com/openabstractions/abstraction-model/identity"
)

const rungOneBytes = 22853663008

// rungOneKV is two resident conversation slots of the measured 22528
// bytes/token main-plus-one-draft KV.
const rungOneKV = 2 * 22528

// rungOneNeed is MODEL-K2's need for rung 1 at code's 32,768 tokens.
const rungOneStartupResidual = 3072 << 20
const rungOneNeed = rungOneBytes + rungOneKV*32768 + rungOneStartupResidual + 1<<30

// The two-slot sizing covers both slots' KV, the provider cache allowance and
// the engine Job peak measured on the pinned b10825 two-slot profile
// (Windows Vulkan, Radeon 8060S, 2026-11-05).
func TestCatalogueTwoSlotSizingCoversMeasuredPeak(t *testing.T) {
	const measuredEngineJobPeak = 28380758016
	const twoSlotCacheAllowance = (1024 + 2*2*128) << 20
	entry := FitCatalog(context.Background(), wire.CatalogUseCode, fitSources(0)).Entries[0]
	if entry.NeedBytes < measuredEngineJobPeak {
		t.Fatalf("Need %d is below the measured two-slot peak %d", entry.NeedBytes, int64(measuredEngineJobPeak))
	}
	if kv := entry.NeedBytes - rungOneBytes - rungOneStartupResidual - 1<<30; kv != 2*22528*int64(entry.Context) {
		t.Fatalf("Need KV %d is not two slots at %d tokens", kv, entry.Context)
	}
	if rungOneStartupResidual < twoSlotCacheAllowance {
		t.Fatal("startup residual is below the two-slot provider cache allowance")
	}
}

func TestCatalogueStartupResidualFallbackAndOverflow(t *testing.T) {
	for _, value := range []string{"", "0", "17", "-1", "9223372036854775807"} {
		var document map[string]json.RawMessage
		if err := json.Unmarshal(catalogDocument, &document); err != nil {
			t.Fatal(err)
		}
		var entries map[string]map[string]json.RawMessage
		if err := json.Unmarshal(document["entries"], &entries); err != nil {
			t.Fatal(err)
		}
		entry := entries["qwen3.6-35b-a3b"]
		delete(entry, "startup_residual_bytes")
		if value != "" {
			entry["startup_residual_bytes"] = json.RawMessage(value)
		}
		document["entries"], _ = json.Marshal(entries)
		raw, _ := json.Marshal(document)
		parsed, err := parseCatalog(raw)
		if value == "-1" || value == "9223372036854775807" {
			if err == nil {
				t.Fatalf("invalid residual %s accepted", value)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		got, valid := parsed.entries["qwen3.6-35b-a3b"].needFor(32768, 2)
		want := int64(rungOneBytes + rungOneKV*32768 + 1<<30)
		if value == "17" {
			want += 17
		}
		if !valid || got != want {
			t.Fatalf("residual %q: need %d/%t, want %d", value, got, valid, want)
		}
	}
	entry := catalogEntry{SizeBytes: 1, KVBytesPerToken: 1, StartupResidualBytes: math.MaxInt64 - fitMargin - 2}
	if got, valid := entry.need(1); !valid || got != math.MaxInt64 {
		t.Fatalf("exact maximum need %d/%t", got, valid)
	}
	entry.StartupResidualBytes++
	if _, valid := entry.need(1); valid {
		t.Fatal("residual addition overflow accepted")
	}
	entry = catalogEntry{SizeBytes: 1, KVBytesPerToken: math.MaxInt64}
	if _, valid := entry.need(2); valid {
		t.Fatal("KV multiplication overflow accepted")
	}
}

func fitSources(pool int64, served ...ServedFamily) CatalogSources {
	return CatalogSources{
		Pool:   func(context.Context) (int64, error) { return pool, nil },
		Served: func(context.Context, string) ([]ServedFamily, error) { return served, nil },
	}
}

func TestCompiledCatalogueParses(t *testing.T) {
	c, err := parseCatalog(catalogDocument)
	if err != nil {
		t.Fatal(err)
	}
	if c.Context["code"] < 32768 {
		t.Fatalf("code context %d is under MODEL-K12's 32,768", c.Context["code"])
	}
	for id, e := range c.entries {
		if e.Gated {
			t.Fatalf("%s needs a licence agreement; DR-16 keeps it out of the first pick", id)
		}
	}
}

func TestFitCodeOnThisComputer(t *testing.T) {
	ctx := context.Background()
	got := FitCatalog(ctx, wire.CatalogUseCode, fitSources(rungOneNeed))
	if got.Outcome != wire.FitOutcomeFitted || got.CatalogVersion == "" || got.PoolBytes != rungOneNeed || len(got.Entries) == 0 {
		t.Fatalf("fit = %+v", got)
	}
	first := got.Entries[0]
	if first.Title != "Qwen3.6 35B" || first.Fit != wire.FitFits || first.Availability != wire.AvailabilityDownload || first.Machine != "" ||
		first.DownloadBytes != rungOneBytes || first.NeedBytes != rungOneNeed || first.Context != 32768 || first.License != "Apache-2.0" ||
		first.Source.Registry != "hf" || first.Source.File != "Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf" || len(first.Digest) != 71 {
		t.Fatalf("rung 1 = %+v", first)
	}
	if first.Family == "" || first.Family != modelidentity.Family("Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf") {
		t.Fatalf("rung 1 family %q", first.Family)
	}
	t.Logf("rung 1 family %q", first.Family)
	short := FitCatalog(ctx, wire.CatalogUseCode, fitSources(rungOneNeed-1))
	if short.Entries[0].Fit != wire.FitNo || short.Entries[0].Availability != wire.AvailabilityDownload {
		t.Fatalf("one byte short = %+v", short.Entries[0])
	}
}

func TestFitServedHereAndOnPairedRuntime(t *testing.T) {
	ctx := context.Background()
	family := modelidentity.Family("Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf")
	// The remote case: a paired runtime already serves the use. Fit says so
	// with nothing to download, whatever this machine's own pool.
	paired := FitCatalog(ctx, wire.CatalogUseCode, fitSources(0, ServedFamily{Family: family, Machine: "desktop"}))
	row := paired.Entries[0]
	if row.Availability != wire.AvailabilityServed || row.Machine != "desktop" || row.DownloadBytes != 0 || row.Fit != wire.FitFits {
		t.Fatalf("served by a paired runtime = %+v", row)
	}
	// This computer comes before a paired one.
	both := FitCatalog(ctx, wire.CatalogUseCode, fitSources(rungOneNeed, ServedFamily{Family: family, Machine: "desktop"}, ServedFamily{Family: family}))
	if row := both.Entries[0]; row.Availability != wire.AvailabilityServed || row.Machine != "" || row.DownloadBytes != 0 {
		t.Fatalf("served here and there = %+v", row)
	}
	// Another family served changes nothing.
	other := FitCatalog(ctx, wire.CatalogUseCode, fitSources(rungOneNeed, ServedFamily{Family: "llama3.2", Machine: "desktop"}))
	if row := other.Entries[0]; row.Availability != wire.AvailabilityDownload || row.DownloadBytes != rungOneBytes {
		t.Fatalf("unrelated served family = %+v", row)
	}
}

func TestFitRefusals(t *testing.T) {
	ctx := context.Background()
	if got := FitCatalog(ctx, wire.CatalogUseVision, fitSources(1<<40)); got.Outcome != wire.FitOutcomeInvalid || len(got.Entries) != 0 || got.CatalogVersion != "" {
		t.Fatalf("unlisted use = %+v", got)
	}
	if got := FitCatalog(ctx, wire.CatalogUse(99), fitSources(1<<40)); got.Outcome != wire.FitOutcomeInvalid {
		t.Fatalf("unknown use = %+v", got)
	}
	broken := CatalogSources{Pool: func(context.Context) (int64, error) { return 0, errors.New("no instrument") },
		Served: func(context.Context, string) ([]ServedFamily, error) { return nil, nil }}
	if got := FitCatalog(ctx, wire.CatalogUseCode, broken); got.Outcome != wire.FitOutcomeUnavailable || len(got.Entries) != 0 {
		t.Fatalf("pool unavailable = %+v", got)
	}
	offerless := CatalogSources{Pool: func(context.Context) (int64, error) { return 1 << 40, nil },
		Served: func(context.Context, string) ([]ServedFamily, error) { return nil, errors.New("router down") }}
	if got := FitCatalog(ctx, wire.CatalogUseCode, offerless); got.Outcome != wire.FitOutcomeUnavailable {
		t.Fatalf("offer unavailable = %+v", got)
	}
}

// catalogHost serves resolver@1 and catalog@1 on one endpoint, as the
// runtime host does.
func catalogHost(t *testing.T, sources CatalogSources, policy func(context.Context, *identity.Peer) error) *client.Client {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("Program process/path evidence is not implemented on macOS")
	}
	endpoint := listen.Endpoint(fmt.Sprintf("model-catalog-test-%d-%d", os.Getpid(), serial.Add(1)))
	reg := model.NewServiceRegistry(resolverFunc{func(context.Context, model.Ref) (download.Spec, error) { return fixtureSpec(), nil }})
	h, err := Listen(endpoint, reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.EnableCatalog(sources, policy); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- h.Serve(context.Background()) }()
	t.Cleanup(func() {
		h.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("model host failed to stop")
		}
	})
	return client.New(endpoint)
}

func TestCatalogServedBesideResolver(t *testing.T) {
	ctx := context.Background()
	family := modelidentity.Family("Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf")
	allow := func(context.Context, *identity.Peer) error { return nil }
	c := catalogHost(t, fitSources(0, ServedFamily{Family: family, Machine: "desktop"}), allow)
	got, err := c.FitContext(ctx, client.CatalogUseCode)
	if err != nil || got.Outcome != client.FitOutcomeFitted || got.Entries[0].Machine != "desktop" || got.Entries[0].Availability != client.AvailabilityServed {
		t.Fatalf("fit over IPC = %+v, %v", got, err)
	}
	// resolver@1 still answers on the same endpoint.
	r, err := c.ResolveContext(ctx, wire.Ref{Registry: "fixture", Repo: "r", Revision: "v", File: "f"})
	if err != nil || r.Outcome != wire.LookupOutcomeResolved {
		t.Fatalf("resolver beside catalog = %+v, %v", r, err)
	}
}

func TestCatalogPolicyRefusesAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	deny := catalogHost(t, fitSources(1<<40), func(context.Context, *identity.Peer) error { return errors.New("not permitted") })
	if got, err := deny.FitContext(ctx, client.CatalogUseCode); err != nil || got.Outcome != client.FitOutcomeForbidden || len(got.Entries) != 0 {
		t.Fatalf("refused fit = %+v, %v", got, err)
	}
	down := catalogHost(t, fitSources(1<<40), func(context.Context, *identity.Peer) error { return ErrPolicyUnavailable })
	if got, err := down.FitContext(ctx, client.CatalogUseCode); err != nil || got.Outcome != client.FitOutcomeUnavailable || len(got.Entries) != 0 {
		t.Fatalf("undecided fit = %+v, %v", got, err)
	}
}

func TestCatalogNotEnabledIsUnknownService(t *testing.T) {
	reg := model.NewServiceRegistry(resolverFunc{func(context.Context, model.Ref) (download.Spec, error) { return fixtureSpec(), nil }})
	_, c := liveHost(t, reg, false)
	if _, err := c.FitContext(context.Background(), client.CatalogUseCode); err == nil {
		t.Fatal("a host without the catalogue answered Fit")
	}
}

func TestEnableCatalogRequiresSourcesAndPolicy(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Program process/path evidence is not implemented on macOS")
	}
	endpoint := listen.Endpoint(fmt.Sprintf("model-catalog-enable-%d-%d", os.Getpid(), serial.Add(1)))
	reg := model.NewServiceRegistry(resolverFunc{func(context.Context, model.Ref) (download.Spec, error) { return fixtureSpec(), nil }})
	h, err := Listen(endpoint, reg)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.EnableCatalog(CatalogSources{}, func(context.Context, *identity.Peer) error { return nil }); err == nil {
		t.Fatal("catalogue enabled without sources")
	}
	if err := h.EnableCatalog(fitSources(1), nil); err == nil {
		t.Fatal("catalogue enabled without a policy")
	}
}
