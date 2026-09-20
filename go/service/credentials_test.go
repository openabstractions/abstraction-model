package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	download "github.com/openabstractions/abstraction-download/go"
	"github.com/openabstractions/abstraction-identity/listen"
	model "github.com/openabstractions/abstraction-model/go"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
	"github.com/openabstractions/abstraction-model/go/client"
)

// gatedHF answers the Hugging Face model API only with the expected bearer
// header, and 401 without it, recording what each request carried.
func gatedHF(t *testing.T, token string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "gated", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"sha": "0123456789abcdef", "siblings": []map[string]any{
			{"rfilename": "model-Q4_K_M.gguf", "size": 12, "lfs": map[string]any{"sha256": strings.Repeat("b", 64), "size": 12}},
		}})
	}))
	t.Cleanup(server.Close)
	return server, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

func TestLookupAppliesTheRefCredentialForTheBoundCaller(t *testing.T) {
	const token = "hf_MODEL_LOOKUP_SECRET_EXAMPLE"
	server, seen := gatedHF(t, token)
	reg := model.NewServiceRegistry(model.HF{BaseURL: server.URL, Client: server.Client()})
	_, c := liveHost(t, reg, false)
	var mu sync.Mutex
	var asked []string
	h2, c2 := credentialHost(t, reg, func(ctx context.Context, account, program, name, host string) (map[string]string, error) {
		mu.Lock()
		asked = append(asked, fmt.Sprintf("%s|%s|%s", account, name, host))
		mu.Unlock()
		switch name {
		case "hf":
			return map[string]string{"Authorization": "Bearer " + token}, nil
		case "down":
			return nil, download.CredentialRefusal(name, "unavailable")
		}
		return nil, download.CredentialRefusal(name, "not_permitted")
	})
	ctx := context.Background()
	ref := wire.Ref{Registry: "hf", Repo: "org/gated", Quant: "Q4_K_M", Credential: "hf"}
	r, e := c2.ResolveContext(ctx, ref)
	if e != nil || r.Outcome != wire.LookupOutcomeResolved || r.Request == nil || len(r.Request.Sources) != 1 || r.Request.Sources[0].Credential != "hf" {
		t.Fatalf("credentialed lookup: %+v %v", r, e)
	}
	host := strings.ToLower(strings.Split(strings.TrimPrefix(server.URL, "https://"), ":")[0])
	if len(asked) != 1 || asked[0] != h2.owner+"|hf|"+host {
		t.Fatalf("applier asked %v", asked)
	}
	if got := seen(); len(got) != 1 || got[0] != "Bearer "+token {
		t.Fatalf("origin saw %v", got)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), token) {
		t.Fatal("lookup reply carries the secret")
	}
	for name, want := range map[string]wire.LookupOutcome{"other": wire.LookupOutcomeUnsupportedMapping, "down": wire.LookupOutcomeUnavailable} {
		ref.Credential = name
		if r, e := c2.ResolveContext(ctx, ref); e != nil || r.Outcome != want || r.Request != nil {
			t.Fatalf("%s: %+v %v", name, r, e)
		}
	}
	// Without the credential the gated origin refuses the listing.
	ref.Credential = ""
	if r, e := c2.ResolveContext(ctx, ref); e != nil || r.Outcome != wire.LookupOutcomeUnavailable {
		t.Fatalf("anonymous gated lookup: %+v %v", r, e)
	}
	// A host with no applier refuses a Ref naming a credential before any request.
	before := len(seen())
	ref.Credential = "hf"
	if r, e := c.ResolveContext(ctx, ref); e != nil || r.Outcome != wire.LookupOutcomeUnsupportedMapping {
		t.Fatalf("no applier: %+v %v", r, e)
	}
	if len(seen()) != before {
		t.Fatal("a Ref naming a credential reached the origin without an applier")
	}
}

// credentialHost serves reg with an applier configured before Serve.
func credentialHost(t *testing.T, reg *model.Registry, apply CredentialApplier) (*Host, *client.Client) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("Program process/path evidence is not implemented on macOS")
	}
	endpoint := listen.Endpoint(fmt.Sprintf("model-test-%d-%d", os.Getpid(), serial.Add(1)))
	h, e := Listen(endpoint, reg)
	if e != nil {
		t.Fatal(e)
	}
	if e := h.EnableCredentials(apply); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- h.Serve(context.Background()) }()
	t.Cleanup(func() {
		h.Close()
		if e := <-done; e != nil {
			t.Error(e)
		}
	})
	return h, client.New(endpoint)
}
