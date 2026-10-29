// Command repo-audit performs deterministic repository policy checks without Node.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Diagnostic retention is bounded so a tree with many matches cannot exhaust
// CI memory; suppressed findings are still counted and still fail the audit.
const (
	maxFindingsPerFile = 5
	maxFindings        = 200
)

var secretAssignment = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?(?:password|pw)|password|passwd|secret|token)[^:=\r\n]{0,32}[:=]\s*["']?([^\s"'#},]+)`)

func main() {
	findings, err := auditRepository()
	if err != nil {
		fmt.Fprintf(os.Stderr, "repo-audit: scan failed (%T)\n", err)
		os.Exit(2)
	}
	if findings.failed() {
		for _, finding := range findings.lines() {
			fmt.Fprintln(os.Stderr, finding)
		}
		os.Exit(1)
	}
	fmt.Println("repo-audit: no Node artifacts or committed secret candidates")
}

// report retains at most maxFindings diagnostics and counts the remainder.
type report struct {
	entries    []string
	suppressed int
}

// add records count findings; describe only runs while the cap has room, so
// suppressed findings never allocate a diagnostic.
func (r *report) add(count int, describe func() string) {
	if len(r.entries) >= maxFindings {
		r.suppressed += count
		return
	}
	r.entries = append(r.entries, describe())
}

func (r *report) failed() bool {
	return len(r.entries) > 0 || r.suppressed > 0
}

func (r *report) lines() []string {
	if r.suppressed == 0 {
		return r.entries
	}
	return append(r.entries[:len(r.entries):len(r.entries)], fmt.Sprintf("repo-audit: %d further findings suppressed", r.suppressed))
}

func auditRepository() (findings *report, resultErr error) {
	findings = &report{}
	repository, err := os.OpenRoot(".")
	if err != nil {
		return nil, errors.New("repository root unavailable")
	}
	defer func() {
		resultErr = errors.Join(resultErr, repository.Close())
	}()
	for _, root := range []string{"cmd", "db", "internal", "scripts", "web", "tests", "reference", ".github", "Dockerfile", "compose.yaml", "compose.prod.example.yaml", ".env.example", "Makefile", "go.mod", "go.sum"} {
		if err := scanRoot(repository, root, findings); err != nil {
			return nil, err
		}
	}
	return findings, nil
}

func scanRoot(repository *os.Root, root string, findings *report) error {
	if _, err := repository.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		clean := filepath.ToSlash(path)
		if entry.IsDir() {
			if clean != "." && ignoredDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			if entry.Name() == "node_modules" {
				findings.add(1, func() string { return strconv.Quote(clean) + ": forbidden Node directory" })
				return filepath.SkipDir
			}
			return nil
		}
		if forbiddenNodeFile(entry.Name()) {
			findings.add(1, func() string { return strconv.Quote(clean) + ": forbidden Node manifest or lockfile" })
		}
		if !scannable(entry.Name()) {
			return nil
		}
		file, err := repository.Open(clean)
		if err != nil {
			return err
		}
		return errors.Join(scanFile(file, clean, findings), file.Close())
	})
}

// scanFile reports up to maxFindingsPerFile line diagnostics for one file and
// folds any further matches into a single per-file summary.
func scanFile(file io.Reader, clean string, findings *report) error {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 16<<20)
	line, fileFindings := 0, 0
	record := func(kind string) {
		fileFindings++
		if fileFindings > maxFindingsPerFile {
			return
		}
		number := line
		findings.add(1, func() string { return fmt.Sprintf("%q:%d: %s", clean, number, kind) })
	}
	for scanner.Scan() {
		line++
		value := scanner.Text()
		if strings.Contains(value, "-----BEGIN ") && strings.Contains(value, "PRIVATE KEY-----") {
			if clean == "cmd/repo-audit/main.go" {
				continue
			}
			record("private key material")
			continue
		}
		match := secretAssignment.FindStringSubmatch(value)
		if len(match) == 3 && secretCandidate(match[2]) {
			record("possible committed secret")
		}
	}
	if extra := fileFindings - maxFindingsPerFile; extra > 0 {
		findings.add(extra, func() string {
			return fmt.Sprintf("%q: %d further findings in this file suppressed", clean, extra)
		})
	}
	return scanner.Err()
}

func ignoredDirectory(name string) bool {
	switch name {
	case ".git", ".agents", ".codex", ".idea", ".vscode", "bin", "dist", "graphify-out":
		return true
	}
	return false
}
func forbiddenNodeFile(name string) bool {
	switch strings.ToLower(name) {
	case "package.json", "package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml":
		return true
	}
	return false
}
func scannable(name string) bool {
	lower := strings.ToLower(name)
	if lower == "dockerfile" || strings.HasPrefix(lower, ".env") {
		return true
	}
	switch filepath.Ext(lower) {
	case ".go", ".templ", ".js", ".css", ".feature", ".json", ".md", ".sql", ".toml", ".yaml", ".yml", ".sh":
		return true
	}
	return false
}
func secretCandidate(value string) bool {
	value = strings.TrimSpace(strings.Trim(value, "\"'"))
	lower := strings.ToLower(value)
	if len(value) < 16 || strings.ContainsAny(value, "${}") || strings.Contains(lower, "redacted") || strings.Contains(lower, "example") || strings.Contains(lower, "development-only") || strings.Contains(lower, "test-only") || strings.HasPrefix(value, "/run/secrets/") {
		return false
	}
	unique := make(map[rune]struct{})
	letters, digits := 0, 0
	for _, char := range value {
		unique[unicode.ToLower(char)] = struct{}{}
		if unicode.IsLetter(char) {
			letters++
		}
		if unicode.IsDigit(char) {
			digits++
		}
	}
	if len(unique) < 6 || letters == 0 || digits == 0 {
		return false
	}
	return true
}
