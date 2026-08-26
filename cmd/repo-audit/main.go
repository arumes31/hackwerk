// Command repo-audit performs deterministic repository policy checks without Node.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var secretAssignment = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?(?:password|pw)|password|passwd|secret|token)[^:=\r\n]{0,32}[:=]\s*["']?([^\s"'#},]+)`)

func main() {
	findings, err := auditRepository()
	if err != nil {
		fmt.Fprintf(os.Stderr, "repo-audit: scan failed (%T)\n", err)
		os.Exit(2)
	}
	if len(findings) > 0 {
		for _, finding := range findings {
			fmt.Fprintln(os.Stderr, finding)
		}
		os.Exit(1)
	}
	fmt.Println("repo-audit: no Node artifacts or committed secret candidates")
}

func auditRepository() (findings []string, resultErr error) {
	findings = make([]string, 0)
	repository, err := os.OpenRoot(".")
	if err != nil {
		return nil, errors.New("repository root unavailable")
	}
	defer func() {
		resultErr = errors.Join(resultErr, repository.Close())
	}()
	for _, root := range []string{"cmd", "db", "docs", "internal", "scripts", "web", "tests", "acceptance", "reference", ".github", "codex/tasks", "Dockerfile", "compose.yaml", "compose.prod.example.yaml", ".env.example", "Makefile", "go.mod", "go.sum"} {
		if err := scanRoot(repository, root, &findings); err != nil {
			return nil, err
		}
	}
	return findings, nil
}

func scanRoot(repository *os.Root, root string, findings *[]string) error {
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
				*findings = append(*findings, clean+": forbidden Node directory")
				return filepath.SkipDir
			}
			return nil
		}
		if forbiddenNodeFile(entry.Name()) {
			*findings = append(*findings, clean+": forbidden Node manifest or lockfile")
		}
		if !scannable(entry.Name()) {
			return nil
		}
		file, err := repository.Open(clean)
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 16<<20)
		line := 0
		for scanner.Scan() {
			line++
			value := scanner.Text()
			if strings.Contains(value, "-----BEGIN ") && strings.Contains(value, "PRIVATE KEY-----") {
				if clean == "cmd/repo-audit/main.go" {
					continue
				}
				*findings = append(*findings, fmt.Sprintf("%s:%d: private key material", clean, line))
				continue
			}
			match := secretAssignment.FindStringSubmatch(value)
			if len(match) == 3 && secretCandidate(match[2]) {
				*findings = append(*findings, fmt.Sprintf("%s:%d: possible committed secret", clean, line))
			}
		}
		return errors.Join(scanner.Err(), file.Close())
	})
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
