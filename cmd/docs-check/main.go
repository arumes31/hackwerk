// Command docs-check validates local Markdown links without requiring Node.js.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var markdownLink = regexp.MustCompile(`\[[^]]*\]\(([^)]+)\)`)

func main() {
	if err := run("."); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = fmt.Println("Dokumentationslinks sind gültig.")
}

func run(root string) error {
	var failures []error
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			name := entry.Name()
			if (strings.HasPrefix(name, ".") && path != root) || name == "bin" || name == "dist" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		broken, checkErr := brokenLinks(path)
		if checkErr != nil {
			return checkErr
		}
		failures = append(failures, broken...)
		return nil
	})
	if err != nil {
		return err
	}
	return errors.Join(failures...)
}

func brokenLinks(document string) ([]error, error) {
	// #nosec G304 -- document is produced only by WalkDir beneath the selected repository root.
	file, err := os.Open(document)
	if err != nil {
		return nil, err
	}
	var failures []error
	scanner := bufio.NewScanner(file)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		for _, match := range markdownLink.FindAllStringSubmatch(scanner.Text(), -1) {
			target := strings.Trim(strings.TrimSpace(match[1]), "<>")
			if ignored(target) {
				continue
			}
			target, _, _ = strings.Cut(target, "#")
			target, _, _ = strings.Cut(target, "?")
			if target == "" {
				continue
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(document), filepath.FromSlash(target)))
			if _, statErr := os.Stat(resolved); statErr != nil {
				failures = append(failures, fmt.Errorf("%s:%d: Linkziel %q: %w", document, lineNumber, target, statErr))
			}
		}
	}
	return failures, errors.Join(scanner.Err(), file.Close())
}

func ignored(target string) bool {
	lower := strings.ToLower(target)
	return strings.HasPrefix(target, "#") || strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:") ||
		strings.HasPrefix(lower, "tel:") || strings.HasPrefix(lower, "data:")
}
