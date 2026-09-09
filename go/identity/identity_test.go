package identity

import (
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) [][]string {
	t.Helper()
	b, err := os.ReadFile("../../testdata/identifiers.tsv")
	if err != nil {
		t.Fatal(err)
	}
	var rows [][]string
	for _, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			t.Fatalf("malformed fixture row %q", line)
		}
		rows = append(rows, f)
	}
	if len(rows) == 0 {
		t.Fatal("fixture is empty")
	}
	return rows
}

func TestFixture(t *testing.T) {
	for _, r := range fixture(t) {
		got := Parse(r[0])
		if got.Family != r[1] || got.Quant != r[2] {
			t.Errorf("%s\n got  %q %q\n want %q %q", r[0], got.Family, got.Quant, r[1], r[2])
		}
	}
}

func TestFineTuneNeverJoinsItsBase(t *testing.T) {
	base := Family("Gemma-4-26B-A4B-it-GGUF")
	for _, tuned := range []string{
		"gemma-4-26b-a4b-it-ultra-uncensored-heretic",
		"llmfan46/gemma-4-26B-A4B-it-abliterated-GGUF",
		"someone/gemma-4-26b-a4b-it-distill",
	} {
		if Family(tuned) == base {
			t.Errorf("%s collapsed onto %s", tuned, base)
		}
	}
}

func TestQuantisationDoesNotChangeTheFamily(t *testing.T) {
	want := Family("qwen/qwen3.6-35b-a3b")
	for _, s := range []string{
		"Qwen3.6-35B-A3B-GGUF",
		"Qwen3.6-35B-A3B-MTP-GGUF",
		"lmstudio-community/Qwen3.6-35B-A3B-GGUF/Qwen3.6-35B-A3B-Q4_K_M.gguf",
		"unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf",
	} {
		if got := Family(s); got != want {
			t.Errorf("%s -> %q, want %q", s, got, want)
		}
	}
	a := Parse("lmstudio-community/Qwen3.6-35B-A3B-GGUF/Qwen3.6-35B-A3B-Q4_K_M.gguf")
	b := Parse("unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf")
	if a.Quant == b.Quant {
		t.Errorf("two quantisations reported the same quant %q", a.Quant)
	}
}

func TestNothingUsefulSurvivesAnEmptyName(t *testing.T) {
	for _, s := range []string{"", "   ", "gguf", "hf://"} {
		if got := Family(s); got != "" {
			t.Errorf("%q -> %q, want empty", s, got)
		}
	}
}
