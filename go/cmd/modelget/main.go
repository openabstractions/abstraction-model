// modelget pulls model weights, and does not lose them when the machine sleeps.
//
//	modelget get hf://bartowski/Qwen2.5-0.5B-Instruct-GGUF#Q4_K_M -o D:/models/
//	modelget list
//	modelget resume
//	modelget where <ref>
//
// What it is really doing is putting a job on disk and then working it. That is
// why closing the lid is survivable: the job outlives this process, and
// `modelget resume` picks up whatever was in flight — including transfers this
// process never started.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/openabstractions/abstraction-download/go"
	job "github.com/openabstractions/abstraction-job/go"
	"github.com/openabstractions/abstraction-model/go"
	"github.com/openabstractions/abstraction-model/go/identity"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	switch os.Args[1] {
	case "get":
		cmdGet(ctx, os.Args[2:])
	case "list":
		cmdList(os.Args[2:])
	case "resume":
		cmdResume(ctx, os.Args[2:])
	case "where":
		cmdWhere(ctx, os.Args[2:])
	case "identify":
		cmdIdentify(os.Args[2:])
	case "inventory":
		cmdInventory(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`modelget — pull model weights without losing them to a sleeping laptop

  modelget get <ref> -o <dir|file> [--background]
  modelget list                     what is in flight, and what finished
  modelget resume                   pick up everything interrupted
  modelget where <ref>              resolve a ref: digest, size, where it can be had
  modelget identify <name>...       what family is this string a host gave me
  modelget inventory                every model on this machine, grouped by family

refs:
  hf://org/repo                     the only .gguf in the repo
  hf://org/repo#Q4_K_M              pick a quantisation
  hf://org/repo@<revision>/file     pin a revision and a file
  ollama://qwen2.5:0.5b             a model Ollama already pulled

env:
  MODELGET_STORE         where jobs live (default ~/.abstraction)
  ABSTRACTION_STORE      the same thing under its generic name, shared with jobd
  ABSTRACTION_NAS_STORE  a job store on a share, watched by a jobd elsewhere
  ABSTRACTION_CRED_HF    bearer token for gated HuggingFace repos
  HF_TOKEN               accepted as an alias for the above
  ABSTRACTION_MODEL_ROOTS  model stores with no fixed location, "name=dir" and
                           listed the way PATH is. Without it "inventory" sees
                           only Ollama, the HuggingFace cache and LM Studio —
                           84 GiB of the machine it was measured on sat in six
                           stores that have to be named.

Where a download actually happens is a property of the machine, not an argument.
If a system downloader is running (jobd) it takes the work and decides for
itself whether that means a NAS, the OS transfer service, or its own two hands.
If none is running, the transfer happens in this process and stops when it does
— which is the bottom of the chain, not a failure. "jobd start" changes that.

The token is NEVER written to the job record — the record names the credential
and the value is resolved from the environment at the moment of the request.
Anything resuming this job later (jobd, another machine) needs the variable set
in its own environment too.`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "modelget:", err)
	os.Exit(status(err))
}

// storeRoot finds the job store, and MODELGET_STORE wins because this is
// modelget. ABSTRACTION_STORE is the generic name jobd and anything else that
// speaks the job record use; honouring it means the supervisor and this tool see
// one store by default rather than two, which is the point of a store that is
// just files.
func storeRoot() string {
	for _, name := range []string{"MODELGET_STORE", "ABSTRACTION_STORE"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fatal(err)
	}
	if legacy := filepath.Join(home, ".modelget"); exists(legacy) {
		return legacy
	}
	return filepath.Join(home, ".abstraction")
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func openStore() job.Store {
	s, err := job.NewFileStore(storeRoot())
	if err != nil {
		fatal(err)
	}
	return s
}

func registry() *model.Registry {
	return model.NewRegistry(
		// The token is passed only so the resolver knows a credential is needed;
		// it records the NAME, never the value. See model/hf.go.
		model.HF{Token: hfToken()},
		model.Ollama{},
	)
}

// The hand-rolled tier chain that used to live here is gone.
//
// It imported bits and nas directly and ranked them itself — NAS first, then
// the OS transfer service, then this process — which is an application deciding
// who fetches its bytes. That is the departure discover.go warns about in its
// own comment: a library that logs does not know which sink is configured.
//
// It also duplicated the priority order that tiers.go owns, and found the NAS
// through nas.FromEnv() — an environment variable — while jobd found it through
// the machine config file. Two discovery mechanisms that disagreed, which is how
// modelget picked BITS in the same session where jobd picked the NAS.
//
// One consequence worth stating rather than discovering later: reaching the NAS
// or BITS now requires a supervisor to be running (`jobd start`). Without one,
// modelget downloads in this process, which is the bottom of the chain and not a
// failure. That is exactly what dl does, and what scripts/tiers.sh proves.
//
// The one thing genuinely lost is that the NAS used to stage model files under
// "models/" rather than the generic "files/", because this layer knew what it
// was carrying. That was the specific naming the generic, which is the right
// direction — but it only affects a staging path on the NAS, and the file still
// arrives where the caller asked. It belongs in the sink, which is a storage
// layer that does not exist yet.

// hfToken bridges the well-known HF_TOKEN to the generic credential name the
// download layer resolves. Setting it in this process's environment means a
// resolver, a runner and a supervisor in the same process all see it, while the
// job record on disk still holds nothing but the name.
func hfToken() string {
	const envName = "ABSTRACTION_CRED_HF"
	if v := os.Getenv(envName); v != "" {
		return v
	}
	if v := os.Getenv("HF_TOKEN"); v != "" {
		os.Setenv(envName, v)
		return v
	}
	return ""
}

func hostOwner() string {
	h, _ := os.Hostname()
	return fmt.Sprintf("modelget@%s:%d", h, os.Getpid())
}

func cmdGet(ctx context.Context, args []string) {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	out := fs.String("o", ".", "destination directory, or a file path")
	background := fs.Bool("background", false, "submit and exit immediately, rather than watching")
	// --here is gone. It asked this program to choose a tier, which is the one
	// decision the whole design takes away from applications: whoever wants a
	// model wants the model, and which machine fetches it is a property of the
	// machine. To watch a transfer happen in front of you, stop the supervisor
	// (`jobd stop`) — that is the same request, made of the machine rather than
	// smuggled through the application.
	ref := need(fs, args, "a model reference")[0]

	// The store comes from THIS tool's resolution, not the download layer's,
	// because MODELGET_STORE has to keep working: real stores exist under it,
	// and silently moving to a different directory orphans whatever is in them.
	// download.Open would resolve the store itself and ignore that variable —
	// which sent a test's 300 MB download into the machine's real store before
	// anyone noticed.
	//
	// Everything ABOVE the store is the service's: whether a supervisor exists,
	// which tier it uses, who moves the bytes. This program picks a directory
	// and nothing else.
	store := openStore()
	svc := download.NewClient(download.DiscoverIn(store))
	reg := registry()

	spec, err := reg.Resolve(ctx, ref)
	if err != nil {
		fatal(err)
	}

	dest := *out
	if isDirPath(dest) {
		dest = filepath.Join(dest, filenameFor(ref, spec))
	}
	// Normalise before printing, not just before storing, so the path shown to
	// the person running the command is the same string another machine will
	// read out of the record.
	dest = download.Portable(dest)
	spec.Sink.Final = dest

	// Say what is about to happen before it happens, including whether this is
	// going to cost any network at all.
	fmt.Printf("%s\n", ref)
	fmt.Printf("  digest  %s\n", spec.Artifact.Digest)
	if spec.Artifact.Size > 0 {
		fmt.Printf("  size    %s\n", human(spec.Artifact.Size))
	}
	fmt.Printf("  to      %s\n", dest)
	for _, s := range spec.Sources {
		where := s.Locator
		if s.Scheme == "file" {
			fmt.Printf("  source  already on this disk (%s): %s\n", s.Attrs["store"], where)
		} else {
			fmt.Printf("  source  %s\n", where)
		}
	}

	h, err := svc.Submit(spec)
	if err != nil {
		fatal(err)
	}
	id := h.ID()
	fmt.Printf("  job     %s\n", id)
	fmt.Printf("  fetched by %s\n\n", svc.Where())

	if *background {
		fmt.Println("left in the store. Check on it with: modelget list")
		return
	}
	if err := watch(ctx, store, id); err != nil {
		if ctx.Err() != nil {
			fmt.Println("\ninterrupted. Progress is saved — run the same command again to continue.")
			os.Exit(130)
		}
		fatal(err)
	}
	report(store, id)
}

// watch follows the job by reading records, which is the same thing any other
// process would do. Nothing here is privileged and nothing is in memory: whether
// the bytes are moving in this process, in a service, or on another machine, the
// picture comes from the same place.
//
// It used to reconcile delegated jobs as it went, because this program was its
// own supervisor. It is not any more.
func watch(ctx context.Context, store job.Store, id string) error {
	sub := job.Watch(store, download.Kind)
	defer sub.Close()
	for {
		for _, rec := range sub.Records() {
			if rec.ID != id {
				continue
			}
			if rec.Progress.Total > 0 {
				fmt.Printf("\r  %s / %s   ", human(rec.Progress.Done), human(rec.Progress.Total))
			}
			switch {
			case rec.State == job.StateTransferred || rec.State == job.StateComplete:
				fmt.Println()
				return nil
			case rec.State.Terminal():
				// A terminal record is *no* [DL-E2] — nothing will try it
				// again — and the caller of this process has to be told which
				// of the two endings it was rather than left to retry a job
				// the store has given up on. Failure is this layer's own
				// vehicle for a class crossing a boundary errors.Is cannot.
				return (&download.Failure{
					Text:      fmt.Sprintf("%s: %s", rec.State, rec.Error),
					Permanent: true,
				}).Err()
			}
		}
		select {
		case <-ctx.Done():
			fmt.Println("\nleaving it in the store. `modelget list` to check on it.")
			return ctx.Err()
		case <-sub.Changes():
		}
	}
}

func report(store job.Store, id string) {
	rec, err := store.Load(id)
	if err != nil {
		fatal(err)
	}
	spec, err := download.SpecOf(rec)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("verified %s\n%s\n", spec.Artifact.Digest, spec.Sink.Final)
}

func cmdList(args []string) {
	need(flag.NewFlagSet("list", flag.ContinueOnError), args)
	store := openStore()
	all, err := store.List()
	if err != nil {
		fatal(err)
	}
	n := 0
	for _, rec := range all {
		if rec.Kind != download.Kind {
			continue
		}
		n++
		spec, _ := download.SpecOf(rec)
		where := "here"
		if rec.Delegated() {
			where = rec.Delegation.System
		}
		pct := ""
		if rec.Progress.Total > 0 {
			pct = fmt.Sprintf(" %3.0f%%", 100*float64(rec.Progress.Done)/float64(rec.Progress.Total))
		}
		fmt.Printf("%-12s%-6s %-10s %s\n", rec.State, pct, where, filepath.Base(spec.Sink.Final))
		if rec.Error != "" {
			fmt.Printf("             %s\n", rec.Error)
		}
	}
	if n == 0 {
		fmt.Println("nothing here yet.")
	}
}

// cmdResume is the whole point of the stack: find work that was interrupted —
// by sleep, by a crash, by this program being closed — and finish it.
func cmdResume(ctx context.Context, args []string) {
	need(flag.NewFlagSet("resume", flag.ContinueOnError), args)
	store := openStore()
	// resume is the one place this program acts as a supervisor: it finishes
	// work whose owner is gone. That is a legitimate thing for a person at a
	// command line to ask for, and it is still the bottom tier — it adopts
	// orphans and runs them HERE, it does not choose a machine.
	//
	// Reconciling delegated jobs has gone with the tier chain. Anything handed
	// to BITS or a NAS was handed there by a supervisor, and that supervisor is
	// what reconciles it; `jobd start` is the answer, not a second implementation
	// of jobd hiding in this command.
	r := download.NewRunner(store, hostOwner())
	n, err := r.Adopt(ctx)
	if err != nil {
		fatal(err)
	}
	if n > 0 {
		fmt.Printf("finished %d interrupted transfer(s)\n", n)
		return
	}

	// "Nothing to resume" is the wrong answer when a job is unfinished but its
	// previous owner's lease has not lapsed yet. That is the normal state in the
	// seconds after a crash, and sending the user away right then is the one
	// moment they came here for.
	all, err := store.List()
	if err != nil {
		fatal(err)
	}
	waiting := 0
	for _, rec := range all {
		if rec.Kind != download.Kind || rec.State.Terminal() || rec.State == job.StateTransferred {
			continue
		}
		if store.Claimable(rec) {
			continue
		}
		waiting++
		spec, _ := download.SpecOf(rec)
		left := time.Until(rec.Lease.ExpiresAt.Time).Round(time.Second)
		pct := ""
		if rec.Progress.Total > 0 {
			pct = fmt.Sprintf(" (%.0f%% done)", 100*float64(rec.Progress.Done)/float64(rec.Progress.Total))
		}
		fmt.Printf("%s%s is still held by %s — free in %s\n",
			filepath.Base(spec.Sink.Final), pct, rec.Lease.Owner, left)
	}
	if waiting > 0 {
		fmt.Println("\nIf that owner is gone its lease lapses by itself; run this again then.")
		fmt.Println("The wait is what stops two processes writing one file at once.")
		return
	}
	fmt.Println("nothing to resume.")
}

func cmdWhere(ctx context.Context, args []string) {
	ref := need(flag.NewFlagSet("where", flag.ContinueOnError), args, "a model reference")[0]
	spec, err := registry().Resolve(ctx, ref)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("digest  %s\n", spec.Artifact.Digest)
	if spec.Artifact.Size > 0 {
		fmt.Printf("size    %s\n", human(spec.Artifact.Size))
	}
	local := 0
	for _, s := range spec.Sources {
		if s.Scheme == "file" {
			local++
			fmt.Printf("local   %s (%s)\n", s.Locator, s.Attrs["store"])
		} else {
			fmt.Printf("remote  %s\n", s.Locator)
		}
	}
	if local > 0 {
		fmt.Printf("\nthese exact bytes are already on this machine %d time(s).\n", local)
		if spec.Artifact.Size > 0 && local > 0 {
			fmt.Printf("fetching this would cost nothing but a copy.\n")
		}
	}
}

func filenameFor(ref string, spec download.Spec) string {
	for _, s := range spec.Sources {
		if s.Scheme == "https" || s.Scheme == "http" {
			if i := strings.LastIndexByte(s.Locator, '/'); i >= 0 {
				return s.Locator[i+1:]
			}
		}
	}
	name := strings.NewReplacer("hf://", "", "ollama://", "", "/", "-", ":", "-", "#", "-", "@", "-").Replace(ref)
	return name + ".gguf"
}

func human(n int64) string {
	const u = 1024
	if n < u {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(u), 0
	for m := n / u; m >= u; m /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// isDirPath decides whether -o named a directory to put the file in.
//
// Stat alone is not enough once the destination belongs to another machine. A
// NAS job says `-o models/`, and that directory exists on the NAS, not here —
// stat fails, and the old code silently treated the whole thing as a filename,
// producing a file literally called "models". A trailing separator is the
// caller saying "directory" in a way that does not require the directory to be
// reachable from where the command was typed.
func isDirPath(p string) bool {
	if strings.HasSuffix(p, "/") || strings.HasSuffix(p, `\`) || p == "." || p == ".." {
		return true
	}
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func cmdIdentify(args []string) {
	names := need(flag.NewFlagSet("identify", flag.ContinueOnError), args, "a model name...")
	for _, a := range names {
		n := identity.Parse(a)
		if n.Quant == "" {
			fmt.Printf("%s\t%s\n", n.Family, a)
		} else {
			fmt.Printf("%s\t%s\t(%s)\n", n.Family, a, n.Quant)
		}
	}
	if len(args) == 2 {
		if identity.Same(args[0], args[1]) {
			fmt.Println("\nsame model. one of these can answer for both.")
		} else {
			fmt.Println("\ndifferent models.")
		}
	}
}

func cmdInventory(args []string) {
	need(flag.NewFlagSet("inventory", flag.ContinueOnError), args)
	var files, dup int
	var total, waste int64
	for _, g := range model.Inventory() {
		if g.Family == "" {
			continue
		}
		fmt.Println(g.Family)
		var biggest int64
		for _, c := range g.Copies {
			q := c.Quant
			if q == "" {
				q = "-"
			}
			d := c.Digest
			if d == "" {
				d = "no published digest"
			} else {
				d = d[:14] + "…"
			}
			fmt.Printf("  %-12s %8.1f GB  %-11s %-22s %s\n", c.Store, float64(c.Size)/1e9, q, d, c.Name)
			files++
			total += c.Size
			if c.Size > biggest {
				biggest = c.Size
			}
		}
		if len(g.Copies) > 1 {
			dup++
			waste += total0(g.Copies) - biggest
		}
	}
	fmt.Printf("\n%d files, %.1f GB. %d families held more than once, %.1f GB of it.\n",
		files, float64(total)/1e9, dup, float64(waste)/1e9)
}

func total0(cs []model.Copy) int64 {
	var n int64
	for _, c := range cs {
		n += c.Size
	}
	return n
}
