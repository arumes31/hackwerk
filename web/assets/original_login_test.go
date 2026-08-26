package assets

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOriginalLoginBackgroundIsImportedVerbatim(t *testing.T) {
	t.Parallel()

	read := func(path ...string) string {
		// #nosec G304 -- every caller supplies repository-owned constant fixture paths.
		data, err := os.ReadFile(filepath.Join(path...))
		if err != nil {
			t.Fatal(err)
		}
		return normalizeNewlines(string(data))
	}
	source := read("..", "..", "temp", "new-lgin.html")

	assertEqual := func(name, want, got string) {
		t.Helper()
		if want != got {
			t.Fatalf("%s differs from temp/new-lgin.html", name)
		}
	}

	assertEqual("CSS", between(t, source, "<style>\n", "\n</style>"), strings.TrimSuffix(read("static", "login-original.css"), "\n"))
	assertEqual("JavaScript", between(t, source, "<script>\n", "\n</script>"), strings.TrimSuffix(read("static", "login-background.js"), "\n"))

	const sceneStart = `<section class="scene">`
	scene := between(t, source, sceneStart, "\n\n<!-- Floating Transparent Login Screen Over the Right Side -->")
	scene = sceneStart + scene
	template := read("..", "templates", "login_background.templ")
	templateScene := sceneStart + between(t, template, sceneStart, "\n}")
	if canonicalXML(t, scene) != canonicalXML(t, templateScene) {
		t.Fatal("SVG scene differs from temp/new-lgin.html")
	}
}

func canonicalXML(t *testing.T, value string) string {
	t.Helper()

	decoder := xml.NewDecoder(strings.NewReader(value))
	var result strings.Builder
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return result.String()
		}
		if err != nil {
			t.Fatalf("parse SVG scene: %v", err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			fmt.Fprintf(&result, "S:%q:%q", token.Name.Space, token.Name.Local)
			for _, attribute := range token.Attr {
				fmt.Fprintf(&result, "A:%q:%q:%q", attribute.Name.Space, attribute.Name.Local, attribute.Value)
			}
		case xml.EndElement:
			fmt.Fprintf(&result, "E:%q:%q", token.Name.Space, token.Name.Local)
		case xml.CharData:
			if text := strings.Join(strings.Fields(string(token)), " "); text != "" {
				fmt.Fprintf(&result, "T:%q", text)
			}
		case xml.Comment:
			fmt.Fprintf(&result, "C:%q", strings.TrimSpace(string(token)))
		}
	}
}

func between(t *testing.T, value, start, end string) string {
	t.Helper()
	startIndex := strings.Index(value, start)
	if startIndex < 0 {
		t.Fatalf("start marker %q not found", start)
	}
	startIndex += len(start)
	endIndex := strings.Index(value[startIndex:], end)
	if endIndex < 0 {
		t.Fatalf("end marker %q not found", end)
	}
	return value[startIndex : startIndex+endIndex]
}

func normalizeNewlines(value string) string {
	return strings.ReplaceAll(value, "\r\n", "\n")
}
