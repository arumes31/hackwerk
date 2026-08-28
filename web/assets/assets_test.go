package assets

import (
	"regexp"
	"strings"
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

func TestEmbeddedStylesDefineFeedbackAndOverlayContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		file      string
		selectors []string
	}{
		{
			name: "application overlays",
			file: "static/app.css",
			selectors: []string{
				".planning-progress li > a",
				".planning-adoption-intro",
				".toast-stack",
				".toast--success",
				"dialog.modal",
				".nav-menu[open] .nav-menu__panel",
				"[popover]:popover-open",
				"[data-tooltip]::after",
				"[data-route-map] .maplibregl-popup-content",
				".route-map-legend .route-map-legend__line",
				".form-alert--warning",
				"@media (forced-colors: active)",
			},
		},
		{
			name:      "login feedback",
			file:      "static/login.css",
			selectors: []string{".login-alert", ".login-alert:focus"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			contents, err := Files.ReadFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}
			stylesheet := string(contents)
			for _, selector := range tt.selectors {
				if !strings.Contains(stylesheet, selector) {
					t.Errorf("%s does not define %q", tt.file, selector)
				}
			}
		})
	}
}
