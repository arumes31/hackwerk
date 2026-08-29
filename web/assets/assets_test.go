package assets

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestVoiceCaptureCancelsNavigationUploadAndUsesAmbiguousDeliveryMessage(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	for _, contract := range []string{
		`Der Übertragungsstatus ist unbekannt.`,
		`[401, 403, 404].includes(response.status)`,
	} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("voice capture script does not preserve %q", contract)
		}
	}
	if strings.Contains(javascript, "Audio wurde nicht dauerhaft gespeichert") {
		t.Fatal("voice capture still claims ambiguous network failures were not stored")
	}
	if !regexp.MustCompile(`(?s)window\.addEventListener\("pagehide", \(\) => \{\s*cancelled = true;`).MatchString(javascript) {
		t.Fatal("voice capture does not cancel the pending recorder upload before navigation")
	}
	if !regexp.MustCompile(`(?s)window\.addEventListener\("pagehide", \(\) => \{.*if \(recorder && recorder\.state !== "inactive"\) recorder\.stop\(\);`).MatchString(javascript) {
		t.Fatal("voice capture page-hide cleanup does not guard a missing recorder")
	}
}

func TestLoginFooterControlsOverrideDisabledPointerEvents(t *testing.T) {
	t.Parallel()

	stylesheet, err := Files.ReadFile("static/login.css")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?s)\.login-meta nav a,\s*\.login-meta nav button\s*\{[^}]*pointer-events:\s*auto`).Match(stylesheet) {
		t.Fatal("login footer navigation controls do not restore pointer events")
	}
}

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

func TestRouteLocationCardsAndFooterKeepStableLayoutContracts(t *testing.T) {
	t.Parallel()

	contents, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	stylesheet := string(contents)
	for _, contract := range []string{
		".site-footer__inner { min-height: 60px; padding-block: .5rem;",
		".site-footer__meta { display: flex;",
		".route-location-card { min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr);",
		".route-location-card > .form-grid { min-width: 0; width: 100%;",
		".route-location-card > .form-grid > .route-location-native-confirm,",
		".route-location-card__actions { width: 100%;",
	} {
		if !strings.Contains(stylesheet, contract) {
			t.Errorf("application stylesheet does not preserve %q", contract)
		}
	}
}

func TestRouteMapMarkersRemainCompactPointedAndTouchSized(t *testing.T) {
	t.Parallel()

	stylesheet, err := Files.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	styles := string(stylesheet)
	for _, contract := range []string{
		".route-map-marker::before",
		".route-map-marker::after",
		`[data-route-marker-scale="overview"]`,
		`[data-route-marker-scale="detail"]`,
	} {
		if !strings.Contains(styles, contract) {
			t.Errorf("route marker styles do not define %q", contract)
		}
	}

	script, err := Files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(script)
	for _, contract := range []string{`dataset.markerLabel`, `dataset.routeMarkerScale`, `anchor:"bottom"`} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("route marker script does not define %q", contract)
		}
	}
}

func TestMapLibreCSPWorkerUsesSupportedConfigurationAPI(t *testing.T) {
	t.Parallel()

	script, err := Files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(script)
	if !strings.Contains(javascript, `.setWorkerUrl(`) {
		t.Fatal("application script does not configure the MapLibre CSP worker with setWorkerUrl")
	}
	if strings.Contains(javascript, `.workerUrl=`) {
		t.Fatal("application script assigns unsupported MapLibre workerUrl property")
	}
}

func TestRouteLocationSaveConfirmsValidDraftAndKeepsNativeFallback(t *testing.T) {
	t.Parallel()

	script, err := Files.ReadFile("static/route-locations.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(script)
	for _, expected := range []string{
		`editor.querySelector("[data-route-location-native-confirmed]")`,
		`const custom = editor.closest("[data-route-location-custom]") || editor;`,
		`const latitude = editor.querySelector("[data-route-location-latitude]");`,
		`if (confirmLocation()) return;`,
		`address.value = text;`,
		`data-route-location-selected-result`,
		`Adresse ausgewählt. Bitte noch eine Bezeichnung eingeben`,
		`data-route-form-feedback`,
		`Bitte mindestens einen Auftrag auswählen.`,
		`dataset.routeLocationsReady = "true"`,
		`const setActive = (choice, selectionChanged = false) => {`,
		`setActive(choice, true)`,
		`Die Bezeichnung darf höchstens 120 Zeichen lang sein.`,
	} {
		if !strings.Contains(javascript, expected) {
			t.Fatalf("route-location save script missing %q", expected)
		}
	}
	if strings.Contains(javascript, `label.value = text;`) {
		t.Fatal("address search overwrites the editable route-location label")
	}
}
