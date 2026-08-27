package assets

import (
	"regexp"
	"testing"
)

func TestLoadPathsReturnsContentVersionedURLs(t *testing.T) {
	t.Parallel()

	paths, err := LoadPaths()
	if err != nil {
		t.Fatal(err)
	}
	versioned := regexp.MustCompile(`^/assets/[^?]+\?v=[0-9a-f]{12}$`)
	for name, assetPath := range map[string]string{
		"application CSS":   paths.CSS,
		"control CSS":       paths.ControlFoundationCSS,
		"application JS":    paths.JavaScript,
		"route location JS": paths.RouteLocationsJavaScript,
		"calendar CSS":      paths.FullCalendarThemeCSS,
		"calendar JS":       paths.FullCalendarJavaScript,
		"login CSS":         paths.LoginCSS,
		"login loader":      paths.LoginLoaderJavaScript,
	} {
		if !versioned.MatchString(assetPath) {
			t.Errorf("%s path = %q, want content version", name, assetPath)
		}
	}
}
