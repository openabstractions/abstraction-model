package service

import (
	"strings"
	"testing"

	api "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
)

// FitResult and FitEntry are display records: a field a newer runtime adds
// is ignored, and the ranked list still reads. Ref, the reference a download
// acts on, still refuses an unknown field.
func TestFitDisplayRecordsIgnoreAnUnknownField(t *testing.T) {
	value := api.FitResult{Outcome: api.FitOutcomeFitted, CatalogVersion: "2026-11", Entries: []api.FitEntry{{
		Title: "Qwen3.6 35B", Family: "qwen3.6", Fit: api.FitFits, Availability: api.AvailabilityDownload,
		Source: api.Ref{Registry: "hf", Repo: "org/model", Revision: strings.Repeat("a", 40), File: "model.gguf"}}}}
	data, err := api.ToJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	text = strings.Replace(text, `"catalog_version":`, `"newer_summary": {"x": [1, 2]}, "catalog_version":`, 1)
	text = strings.Replace(text, `"title":`, `"newer_badge": "new", "title":`, 1)
	if !strings.Contains(text, "newer_summary") || !strings.Contains(text, "newer_badge") {
		t.Fatalf("fixture placed no unknown field: %s", text)
	}
	var got api.FitResult
	if err := api.FromJSON([]byte(text), &got); err != nil {
		t.Fatalf("a display record with an unknown field: %v\n%s", err, text)
	}
	if len(got.Entries) != 1 || got.Entries[0].Title != "Qwen3.6 35B" || got.CatalogVersion != "2026-11" {
		t.Fatalf("decoded %+v", got)
	}

	strict := strings.Replace(string(data), `"registry":`, `"newer_mirror": "x", "registry":`, 1)
	if err := api.FromJSON([]byte(strict), &got); err == nil {
		t.Fatal("a Ref with an unknown field decoded")
	}
}
