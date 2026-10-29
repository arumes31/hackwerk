package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// secretLine builds a matching assignment at runtime so this file does not
// itself trip the audit it tests.
func secretLine(index int) string {
	return "pass" + "word: " + fmt.Sprintf("Zq9xV7mW2kR4tL8p%04d", index)
}

func TestScanFileBoundsPerFileDiagnostics(t *testing.T) {
	t.Parallel()
	var content strings.Builder
	for index := range maxFindingsPerFile + 7 {
		content.WriteString(secretLine(index) + "\n")
	}
	findings := &report{}
	if err := scanFile(strings.NewReader(content.String()), "config/app.yaml", findings); err != nil {
		t.Fatal(err)
	}
	lines := findings.lines()
	if len(lines) != maxFindingsPerFile+1 {
		t.Fatalf("got %d diagnostics, want %d: %q", len(lines), maxFindingsPerFile+1, lines)
	}
	if lines[0] != `"config/app.yaml":1: possible committed secret` {
		t.Fatalf("first diagnostic = %q", lines[0])
	}
	if want := `"config/app.yaml": 7 further findings in this file suppressed`; lines[len(lines)-1] != want {
		t.Fatalf("summary = %q, want %q", lines[len(lines)-1], want)
	}
}

func TestReportCapsRetainedDiagnosticsAndStillFails(t *testing.T) {
	t.Parallel()
	findings := &report{}
	described := 0
	for range maxFindings + 25 {
		findings.add(1, func() string {
			described++
			return "finding"
		})
	}
	findings.add(4, func() string {
		described++
		return "summary"
	})
	if described != maxFindings {
		t.Fatalf("described %d diagnostics after cap, want %d", described, maxFindings)
	}
	lines := findings.lines()
	if len(lines) != maxFindings+1 || !findings.failed() {
		t.Fatalf("got %d lines (failed=%v), want %d and failure", len(lines), findings.failed(), maxFindings+1)
	}
	if want := "repo-audit: 29 further findings suppressed"; lines[len(lines)-1] != want {
		t.Fatalf("suppression summary = %q, want %q", lines[len(lines)-1], want)
	}
}

func TestScanFileQuotesControlCharactersInPaths(t *testing.T) {
	t.Parallel()
	findings := &report{}
	path := "evil\n\"cmd/ok.go\":1: forged\x1b[0m.yaml"
	if err := scanFile(strings.NewReader(secretLine(1)+"\n"), path, findings); err != nil {
		t.Fatal(err)
	}
	lines := findings.lines()
	if len(lines) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %q", len(lines), lines)
	}
	if strings.ContainsAny(lines[0], "\n\x1b") {
		t.Fatalf("diagnostic contains raw control characters: %q", lines[0])
	}
	if want := `"evil\n\"cmd/ok.go\":1: forged\x1b[0m.yaml":1: possible committed secret`; lines[0] != want {
		t.Fatalf("diagnostic = %q, want %q", lines[0], want)
	}
}

func TestAuditRepositoryReportsNodeArtifactsWithQuotedPaths(t *testing.T) {
	directory := t.TempDir()
	for _, path := range []string{filepath.Join("web", "node_modules"), "scripts"} {
		if err := os.MkdirAll(filepath.Join(directory, path), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "scripts", "package.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(directory)
	findings, err := auditRepository()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(findings.lines(), "\n")
	for _, want := range []string{`"scripts/package.json": forbidden Node manifest or lockfile`, `"web/node_modules": forbidden Node directory`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestAuditRepositoryPassesCleanTree(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "cmd"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "cmd", "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(directory)
	findings, err := auditRepository()
	if err != nil {
		t.Fatal(err)
	}
	if findings.failed() {
		t.Fatalf("clean tree failed audit: %q", findings.lines())
	}
}
