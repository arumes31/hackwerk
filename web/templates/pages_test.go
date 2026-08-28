package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/planning"
)

func TestRoutePointCountIncludesDistinctEndpoints(t *testing.T) {
	t.Parallel()

	stops := []planning.RouteStop{
		{Location: planning.Point{Latitude: 48.2, Longitude: 14.2}},
		{Location: planning.Point{Latitude: 48.3, Longitude: 14.3}},
	}
	tests := []struct {
		name  string
		route planning.RouteDraft
		want  int
	}{
		{
			name:  "separate end point",
			route: planning.RouteDraft{Start: planning.Point{Latitude: 48.1, Longitude: 14.1}, End: planning.Point{Latitude: 48.4, Longitude: 14.4}, Stops: stops},
			want:  4,
		},
		{
			name:  "last job is end point",
			route: planning.RouteDraft{Start: planning.Point{Latitude: 48.1, Longitude: 14.1}, End: stops[len(stops)-1].Location, Stops: stops},
			want:  3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := routePointCount(test.route); got != test.want {
				t.Fatalf("routePointCount() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestFullCalendarAssetsArePageSpecific(t *testing.T) {
	t.Parallel()

	page := PageData{
		AppName:                     "HackWerk",
		CSSPath:                     "/assets/app.css?v=app",
		ControlFoundationCSSPath:    "/assets/control-foundation.css?v=control",
		JSPath:                      "/assets/app.js?v=app",
		FullCalendarJSPath:          "/assets/fullcalendar.min.js?v=calendar",
		FullCalendarThemeJSPath:     "/assets/fullcalendar-theme.min.js?v=calendar",
		FullCalendarSkeletonCSSPath: "/assets/fullcalendar-skeleton.css?v=calendar",
		FullCalendarThemeCSSPath:    "/assets/fullcalendar-theme.css?v=calendar",
		FullCalendarPaletteCSSPath:  "/assets/fullcalendar-palette.css?v=calendar",
	}

	render := func(calendar bool) string {
		t.Helper()
		var output bytes.Buffer
		component := documentHead(page, "Test")
		if calendar {
			component = calendarDocumentHead(page, "Kalender")
		}
		if err := component.Render(context.Background(), &output); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}

	regular := render(false)
	if strings.Contains(regular, "fullcalendar") {
		t.Fatalf("regular page loads calendar assets: %s", regular)
	}
	calendar := render(true)
	for _, assetName := range []string{"fullcalendar.min.js", "fullcalendar-theme.min.js", "fullcalendar-skeleton.css", "fullcalendar-theme.css", "fullcalendar-palette.css"} {
		if !strings.Contains(calendar, assetName) {
			t.Errorf("calendar head does not load %s", assetName)
		}
	}
	if strings.Index(calendar, "control-foundation.css") > strings.Index(calendar, "app.css") {
		t.Fatal("control foundation must load before application CSS")
	}
}

func TestMobileMoreUsesCurrentPageSemantics(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, href, label string
		role                    auth.Role
		driverID                string
	}{
		{name: "admin subpage", path: "/admin/resources", href: "/admin/resources", label: "Ressourcen", role: auth.RoleAdmin},
		{name: "driver subpage", path: "/availability", href: "/availability", label: "Meine Verfügbarkeit", role: auth.RoleDriver, driverID: "driver-1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			shell := ShellData{
				Page: PageData{AppName: "HackWerk"}, ActivePath: test.path,
				Actor: auth.Actor{Role: test.role, DriverID: test.driverID, DisplayName: "Test"},
			}
			if err := appHeader(shell).Render(context.Background(), &output); err != nil {
				t.Fatal(err)
			}
			panelAt := strings.Index(output.String(), `class="mobile-more__panel"`)
			if panelAt < 0 {
				t.Fatal("mobile more panel missing")
			}
			panel := output.String()[panelAt:]
			want := `href="` + test.href + `" class="nav-link nav-link--active" aria-current="page">` + test.label
			if !strings.Contains(panel, want) {
				t.Fatalf("mobile more current link missing %q: %s", want, panel)
			}
		})
	}
}
