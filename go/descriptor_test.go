package model

import (
	api "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
	"testing"
)

func TestDescriptorRefPreservesNativeParsing(t *testing.T) {
	ref, err := ParseRef("hf://org/repo@revision#Q4_K_M")
	if err != nil {
		t.Fatal(err)
	}
	record := api.Ref{Registry: ref.Registry, Repo: ref.Repo, Revision: ref.Revision, Quant: ref.Quant, File: ref.File}
	got, err := api.Decode(api.Encode(&record))
	if err != nil {
		t.Fatal(err)
	}
	restored := Ref{Registry: got.Registry, Repo: got.Repo, Revision: got.Revision, Quant: got.Quant, File: got.File}
	if restored != ref || restored.String() != ref.String() {
		t.Fatal("reference changed")
	}
}
