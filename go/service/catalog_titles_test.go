package service

import "testing"

func TestCatalogFamilyTitles(t *testing.T) {
	c := catalog{entries: map[string]catalogEntry{
		"a": {family: "known", Title: "  A model  "},
		"b": {family: "conflict", Title: "First"},
		"c": {family: "conflict", Title: "Second"},
		"d": {family: "unsafe", Title: "Bad\nname"},
		"e": {family: "empty", Title: " "},
		"f": {family: "unicode", Title: "模型 · Café"},
	}}
	lookup := c.familyTitleLookup()
	c.entries["a"] = catalogEntry{family: "known", Title: "Changed"}
	for family, want := range map[string]string{"known": "A model", "conflict": "", "empty": "", "missing": "", "unicode": "模型 · Café"} {
		if got := lookup(family); got != want {
			t.Errorf("%s: %q, want %q", family, got, want)
		}
	}
	if lookup := FamilyTitleLookup(); lookup("missing") != "" {
		t.Fatal("unknown bundled family has a title")
	}
	parsed, err := parseCatalog(catalogDocument)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range parsed.entries {
		if FamilyTitleLookup()(entry.family) != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("bundled catalogue supplies no titles")
	}
}
