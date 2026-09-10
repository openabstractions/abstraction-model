package main

import (
	"errors"
	"flag"
	"fmt"
	"testing"

	"github.com/openabstractions/abstraction-download/go"
	job "github.com/openabstractions/abstraction-job/go"
)

func getFlags() (*flag.FlagSet, *string, *bool) {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	return fs, fs.String("o", ".", "destination"), fs.Bool("background", false, "submit and exit")
}

func TestParseRefuses(t *testing.T) {
	for _, c := range []struct {
		name string
		fs   *flag.FlagSet
		args []string
		want []string
	}{
		{"a flag get does not honour", newGet(), []string{"hf://o/r", "--verify", "sha256:ab"}, []string{"a model reference"}},
		{"-o with nothing after it", newGet(), []string{"hf://o/r", "-o"}, []string{"a model reference"}},
		{"-o given an empty value", newGet(), []string{"hf://o/r", "-o", ""}, []string{"a model reference"}},
		{"a second reference", newGet(), []string{"hf://o/r", "hf://o/s"}, []string{"a model reference"}},
		{"no reference at all", newGet(), nil, []string{"a model reference"}},
		{"list given a positional", flag.NewFlagSet("list", flag.ContinueOnError), []string{"all"}, nil},
		{"resume given a positional", flag.NewFlagSet("resume", flag.ContinueOnError), []string{"hf://o/r"}, nil},
		{"inventory given a positional", flag.NewFlagSet("inventory", flag.ContinueOnError), []string{"ollama"}, nil},
		{"where given two references", flag.NewFlagSet("where", flag.ContinueOnError), []string{"a", "b"}, []string{"a model reference"}},
		{"identify given no names", flag.NewFlagSet("identify", flag.ContinueOnError), nil, []string{"a model name..."}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := parse(c.fs, c.args, c.want...); err == nil {
				t.Fatal("accepted; an argument the command will not act on must be a refusal")
			}
		})
	}
}

// A flag's VALUE is not a positional. The scan this replaced returned "D:/m" as
// the reference here, so `get -o D:/m hf://o/r` resolved a model named after
// the destination and wrote it to a file called "hf:".
func TestParseDoesNotMistakeAValueForAReference(t *testing.T) {
	fs, out, background := getFlags()
	pos, err := parse(fs, []string{"-o", "D:/m", "hf://o/r"}, "a model reference")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pos[0] != "hf://o/r" || *out != "D:/m" || *background {
		t.Fatalf("ref=%q out=%q background=%v", pos[0], *out, *background)
	}
}

func TestParseAcceptsFlagsAfterTheReference(t *testing.T) {
	fs, out, background := getFlags()
	pos, err := parse(fs, []string{"hf://o/r", "-o", "D:/m", "--background"}, "a model reference")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if pos[0] != "hf://o/r" || *out != "D:/m" || !*background {
		t.Fatalf("ref=%q out=%q background=%v", pos[0], *out, *background)
	}
}

func TestParseTakesEveryNameIdentifyIsGiven(t *testing.T) {
	pos, err := parse(flag.NewFlagSet("identify", flag.ContinueOnError), []string{"a", "b", "c"}, "a model name...")
	if err != nil || len(pos) != 3 {
		t.Fatalf("pos=%v err=%v", pos, err)
	}
}

func TestStatusCarriesTheClass(t *testing.T) {
	failed := (&download.Failure{Error: "failed: 404", Permanent: true}).Err()
	for _, c := range []struct {
		name string
		err  error
		want int
	}{
		{"nothing went wrong", nil, 0},
		{"a dropped connection is not now", errors.New("read: connection reset"), 1},
		{"the source refused is no", download.ErrRefused, 3},
		{"a record that ended failed is no", failed, 3},
		{"an unreadable record is no", fmt.Errorf("%w: spec", job.ErrInvalid), 3},
		{"a lost delegate answer is unknown", fmt.Errorf("nas: %w", download.ErrOutcomeUnknown), 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := status(c.err); got != c.want {
				t.Fatalf("status = %d, want %d", got, c.want)
			}
		})
	}
}

func newGet() *flag.FlagSet { fs, _, _ := getFlags(); return fs }
