package main

import (
	"strings"
	"testing"
)

func TestApplyPatchesIsIdempotent(t *testing.T) {
	t.Parallel()
	list := []patch{{"header", "H\n", "H\nX\n"}, {"site", "a(b)", "a(c(b))"}}
	once, err := applyPatches("bundle.js", "H\nfoo a(b) bar", list)
	if err != nil {
		t.Fatal(err)
	}
	if once != "H\nX\nfoo a(c(b)) bar" {
		t.Fatalf("patched content = %q", once)
	}
	twice, err := applyPatches("bundle.js", once, list)
	if err != nil || twice != once {
		t.Fatalf("second run = %q, %v; want unchanged", twice, err)
	}
}

func TestApplyPatchesRejectsRemainingOriginalSite(t *testing.T) {
	t.Parallel()
	list := []patch{{"site", "a(b)", "a(c(b))"}}
	for _, content := range []string{
		"a(c(b)) a(b)", // one site patched, another copy still original
		"a(b) a(b)",    // ambiguous pristine site
		"nothing here", // missing site
	} {
		if _, err := applyPatches("bundle.js", content, list); err == nil || !strings.Contains(err.Error(), "want exactly 1") {
			t.Fatalf("applyPatches(%q) error = %v, want patch-site error", content, err)
		}
	}
}
