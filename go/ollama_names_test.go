package model

import "testing"

// TestOllamaNamesStayInTheStore: a model name or tag that would climb out of
// the store, name a drive or a stream, or that Win32 would rewrite, is
// refused before any file opens.
func TestOllamaNamesStayInTheStore(t *testing.T) {
	for _, s := range []string{"llama3", "library/llama3", "hf.co/org/model", "qwen2.5", "user/model", "hf.co/bartowski/Llama-3.2-1B-Instruct-GGUF"} {
		if !storePathParts(s, true) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range []string{"latest", "8b-instruct-q4_K_M", "0.5b", "3.1", "Q4_K_M", "1.5b-instruct-fp16"} {
		if !storePathParts(s, false) {
			t.Errorf("tag %q refused", s)
		}
	}
	for _, s := range []string{"", "..", "a/../b", `a\b`, "C:x", "a/./b", "a//b", "x.", "x ", "a:b", "con", "library/nul", "COM1.txt", "COM¹", "library/LPT³", "CON .x", "-x", ".x", "x　"} {
		if storePathParts(s, true) {
			t.Errorf("name %q allowed", s)
		}
	}
	for _, s := range []string{`..\..\..\outside\secret`, "../x", "a/b", "..", "x.", "COM¹", "nul .txt"} {
		if storePathParts(s, false) {
			t.Errorf("tag %q allowed", s)
		}
	}
}
