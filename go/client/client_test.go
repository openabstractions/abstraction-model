package client

import (
	"context"
	"fmt"
	request "github.com/openabstractions/abstraction-download/go/abstraction/download/request"
	"github.com/openabstractions/abstraction-identity/listen"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

type forged struct{ result wire.LookupResult }

func (f forged) Resolve(wire.Ref) (wire.LookupResult, error) { return f.result, nil }
func TestClientRejectsForgedMappingOverIPC(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Program process/path evidence unavailable")
	}
	valid := request.Request{Artifact: request.Artifact{Digest: "sha256:" + strings.Repeat("a", 64)}, Sources: []request.Source{{Scheme: "https", Locator: "https://example.invalid/weights"}}}
	for i, result := range []wire.LookupResult{{Outcome: wire.LookupOutcomeResolved}, {Outcome: wire.LookupOutcomeResolved, Request: &request.Request{}}, {Outcome: wire.LookupOutcomeUnavailable, Request: &valid}} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			endpoint := listen.Endpoint(fmt.Sprintf("model-forged-%d-%d", os.Getpid(), i))
			l, e := listen.Listen(endpoint)
			if e != nil {
				t.Fatal(e)
			}
			defer l.Close()
			done := make(chan error, 1)
			go func() {
				conn, e := l.Accept()
				if e != nil {
					done <- e
					return
				}
				defer conn.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				call, e := listen.ReceiveFramed(ctx, conn, listen.Program, 1<<20)
				if call != nil {
					defer call.Close()
				}
				if e == nil {
					d := wire.ModelResolverDispatcher{Handler: forged{result}}
					var reply []byte
					reply, e = d.ExchangeFrame(call.Frame)
					if e == nil {
						e = call.Reply(reply)
					}
				}
				done <- e
			}()
			if _, e := New(endpoint).Resolve(Ref{Registry: "fixture", Repo: "x"}); e == nil {
				t.Fatal("accepted inconsistent resolution")
			}
			if e := <-done; e != nil {
				t.Fatal(e)
			}
		})
	}
}
