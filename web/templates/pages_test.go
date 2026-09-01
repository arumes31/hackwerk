package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/dashboard"
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
		MobileCSSPath:               "/assets/mobile-app.css?v=mobile",
		ControlFoundationCSSPath:    "/assets/control-foundation.css?v=control",
		JSPath:                      "/assets/app.js?v=app",
		PresentationBootstrapJSPath: "/assets/presentation-bootstrap.js?v=bootstrap",
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
	if strings.Index(calendar, "app.css") > strings.Index(calendar, "mobile-app.css") {
		t.Fatal("mobile app presentation layer must load after application CSS")
	}
	if strings.Index(calendar, "presentation-bootstrap.js") > strings.Index(calendar, "control-foundation.css") {
		t.Fatal("presentation preferences must be applied before styles load")
	}
}

func TestMobileShellKeepsFiveDestinationsAndSeparatePrimaryAction(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	shell := ShellData{
		Page:       PageData{AppName: "HackWerk"},
		ActivePath: "/dashboard",
		Actor:      auth.Actor{Role: auth.RoleAdmin, DisplayName: "Test"},
	}
	if err := appHeader(shell).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	navigationStart := strings.Index(markup, `class="mobile-bottom-nav"`)
	if navigationStart < 0 {
		t.Fatal("mobile bottom navigation is missing")
	}
	navigationEndOffset := strings.Index(markup[navigationStart:], `</nav>`)
	if navigationEndOffset < 0 {
		t.Fatal("mobile bottom navigation is not closed")
	}
	navigation := markup[navigationStart : navigationStart+navigationEndOffset]
	if got := strings.Count(navigation, `data-mobile-nav-item`); got != 5 {
		t.Fatalf("mobile bottom navigation destinations = %d, want 5", got)
	}
	if strings.Contains(navigation, "mobile-primary-action") {
		t.Fatal("mobile primary action must not consume a bottom-navigation destination")
	}
	if !strings.Contains(markup, `class="mobile-primary-action`) || !strings.Contains(markup, `href="/customers/new"`) {
		t.Fatal("separate mobile primary action is missing")
	}
	for _, contract := range []string{
		`data-mobile-menu-open`,
		`aria-haspopup="dialog"`,
		`aria-controls="mobile-more-sheet"`,
		`<dialog id="mobile-more-sheet" class="mobile-more__dialog" data-mobile-menu`,
		`data-mobile-menu-close`,
		`class="mobile-more--fallback"`,
	} {
		if !strings.Contains(markup, contract) {
			t.Errorf("mobile More sheet is missing %q", contract)
		}
	}
}

func TestDashboardRendersRoleSpecificChronologicalMobileToursWithoutChangingDesktopProjection(t *testing.T) {
	t.Parallel()

	firstStart := time.Date(2026, time.August, 25, 8, 30, 0, 0, time.FixedZone("Europe/Vienna", 2*60*60))
	first := dashboard.Appointment{
		ID: "appointment-1", JobID: "job-1", CustomerID: "customer-1", JobNumber: "HW-101", Lifecycle: "fixed", Confirmation: "accepted",
		JobType: "whole_tree", VolumeM3: "24", CustomerName: "Erster Halt", Locality: "Krems",
		Drivers: "Franz Fahrer", Resources: "Hackmaschine 1", Chippers: "Hackmaschine 1", MapsURL: "https://maps.example/first", StartsAt: firstStart, EndsAt: firstStart.Add(2 * time.Hour),
	}
	second := dashboard.Appointment{
		ID: "appointment-2", JobID: "job-2", CustomerID: "customer-2", Lifecycle: "proposed", Confirmation: "pending",
		JobType: "logs", VolumeM3: "18", CustomerName: "Zweiter Halt", Locality: "Melk",
		Drivers: "Franz Fahrer", Resources: "Hackmaschine 1", MapsURL: "https://maps.example/second", StartsAt: firstStart.Add(3 * time.Hour), EndsAt: firstStart.Add(5 * time.Hour),
	}
	render := func(admin bool, driverID string) string {
		t.Helper()
		var output bytes.Buffer
		component := Dashboard(DashboardData{
			Shell: ShellData{Page: PageData{AppName: "HackWerk"}, ActivePath: "/dashboard", Actor: auth.Actor{Role: auth.RoleDriver, DriverID: driverID, DisplayName: "Test Fahrer"}},
			View: dashboard.View{
				Admin: admin, Date: "2026-08-25", DateLabel: "Dienstag, 25. August", PreviousDate: "2026-08-24", NextDate: "2026-08-26",
				Today:  []dashboard.Appointment{first, second},
				Groups: []dashboard.AppointmentGroup{{ResourceName: "Hackmaschine 1", Appointments: []dashboard.Appointment{second, first}}},
			},
		})
		if admin {
			component = Dashboard(DashboardData{
				Shell: ShellData{Page: PageData{AppName: "HackWerk"}, ActivePath: "/dashboard", Actor: auth.Actor{Role: auth.RoleAdmin, DisplayName: "Test Admin"}},
				View:  dashboard.View{Admin: true, Date: "2026-08-25", DateLabel: "Dienstag, 25. August", PreviousDate: "2026-08-24", NextDate: "2026-08-26", Today: []dashboard.Appointment{first, second}, Groups: []dashboard.AppointmentGroup{{ResourceName: "Hackmaschine 1", Appointments: []dashboard.Appointment{first, second}}}},
			})
		}
		if err := component.Render(context.Background(), &output); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}

	driverMarkup := render(false, "driver-1")
	for _, contract := range []string{
		`data-dashboard-role="driver"`,
		`data-dashboard-projection="driver-tour"`,
		`data-dashboard-projection="resource-groups"`,
		`href="/my-route?date=2026-08-25"`,
		`data-driver-tour-appointment`,
	} {
		if !strings.Contains(driverMarkup, contract) {
			t.Errorf("driver dashboard is missing mobile tour contract %q", contract)
		}
	}
	if strings.Contains(driverMarkup, `data-dashboard-projection="admin-tour"`) {
		t.Fatal("driver dashboard must not render the admin tour projection")
	}
	tourStart := strings.Index(driverMarkup, `data-dashboard-projection="driver-tour"`)
	if tourStart < 0 {
		t.Fatal("driver mobile tour section boundaries are missing")
	}
	tourEnd := strings.Index(driverMarkup[tourStart:], `</section>`)
	if tourEnd < 0 {
		t.Fatal("driver mobile tour section boundaries are missing")
	}
	tourMarkup := driverMarkup[tourStart : tourStart+tourEnd]
	if firstAt, secondAt := strings.Index(tourMarkup, "Erster Halt"), strings.Index(tourMarkup, "Zweiter Halt"); firstAt < 0 || secondAt <= firstAt {
		t.Fatalf("driver mobile tour does not preserve View.Today chronology: %s", tourMarkup)
	}
	if got := strings.Count(tourMarkup, `data-driver-tour-appointment`); got != 2 {
		t.Fatalf("driver mobile tour appointments = %d, want 2", got)
	}
	if strings.Contains(tourMarkup, `<h3><a`) {
		t.Fatal("driver mobile tour must expose the customer name as text and keep the explicit 44px order action")
	}
	withoutProfile := render(false, "")
	if strings.Contains(withoutProfile, `class="button button--quiet driver-tour__route"`) {
		t.Fatal("driver without an assigned profile must not receive the own-route action")
	}

	adminMarkup := render(true, "")
	for _, contract := range []string{
		`data-dashboard-role="admin"`,
		`data-dashboard-projection="admin-tour"`,
		`data-dashboard-projection="resource-groups"`,
		`href="/calendar?date=2026-08-25"`,
		`data-admin-tour-appointment`,
		`href="/calendar/appointments/appointment-1"`,
		`href="/customers/customer-1#job-job-1"`,
		`href="/customers/customer-1#notes-job-1"`,
		`href="https://maps.example/first"`,
		`Auftrag HW-101`,
	} {
		if !strings.Contains(adminMarkup, contract) {
			t.Errorf("admin dashboard is missing mobile disposition contract %q", contract)
		}
	}
	if strings.Contains(adminMarkup, `data-dashboard-projection="driver-tour"`) {
		t.Fatal("admin dashboard must not render the driver tour projection")
	}
	adminTourStart := strings.Index(adminMarkup, `data-dashboard-projection="admin-tour"`)
	if adminTourStart < 0 {
		t.Fatal("admin mobile tour section boundaries are missing")
	}
	adminTourEnd := strings.Index(adminMarkup[adminTourStart:], `</section>`)
	if adminTourEnd < 0 {
		t.Fatal("admin mobile tour section boundaries are missing")
	}
	adminTourMarkup := adminMarkup[adminTourStart : adminTourStart+adminTourEnd]
	if firstAt, secondAt := strings.Index(adminTourMarkup, "Erster Halt"), strings.Index(adminTourMarkup, "Zweiter Halt"); firstAt < 0 || secondAt <= firstAt {
		t.Fatalf("admin mobile tour does not preserve View.Today chronology: %s", adminTourMarkup)
	}
	if got := strings.Count(adminTourMarkup, `data-admin-tour-appointment`); got != 2 {
		t.Fatalf("admin mobile tour appointments = %d, want 2", got)
	}
	for previous, next := range map[string]string{
		"Termin öffnen":  "Auftrag öffnen",
		"Auftrag öffnen": "Navigation",
		"Navigation":     "Notiz ergänzen",
	} {
		if previousAt, nextAt := strings.Index(adminTourMarkup, previous), strings.Index(adminTourMarkup, next); previousAt < 0 || nextAt <= previousAt {
			t.Fatalf("admin mobile action hierarchy must keep %q before %q", previous, next)
		}
	}
	if strings.Contains(adminTourMarkup, `<form`) || strings.Contains(adminTourMarkup, `/my-route`) {
		t.Fatal("admin mobile tour must remain a read-only navigation surface without driver-only route actions")
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
			markup := output.String()
			panelAt := strings.Index(markup, `class="mobile-more__panel"`)
			if panelAt < 0 {
				t.Fatal("mobile more panel missing")
			}
			panel := markup[panelAt:]
			want := `href="` + test.href + `" class="nav-link nav-link--active">` + test.label
			if !strings.Contains(panel, want) {
				t.Fatalf("mobile more current link missing %q: %s", want, panel)
			}
			if strings.Contains(panel, `aria-current="page"`) {
				t.Fatal("hidden mobile-more links must not duplicate current-page semantics")
			}
			triggerAt := strings.Index(markup, `data-mobile-menu-open`)
			if triggerAt < 0 || !strings.Contains(markup[triggerAt:min(triggerAt+320, len(markup))], `aria-current="page"`) {
				t.Fatal("visible mobile More destination does not expose current-page semantics")
			}
		})
	}
}

func TestCommandSearchHasProgressiveNoJavaScriptFallbacks(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	shell := ShellData{
		Page: PageData{AppName: "HackWerk"}, Actor: auth.Actor{Role: auth.RoleAdmin, DisplayName: "Test"}, CSRFToken: "csrf",
	}
	if err := appHeader(shell).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	if count := strings.Count(markup, `data-command-search-fallback`); count != 2 {
		t.Fatalf("desktop/mobile no-JavaScript search fallbacks = %d, want 2", count)
	}
	for _, contract := range []string{`method="post" action="/search"`, `name="q" type="search"`, `value="csrf"`} {
		if !strings.Contains(markup, contract) {
			t.Errorf("no-JavaScript search fallback is missing %q", contract)
		}
	}
}

func TestUserAccessSeparatesRoleFromActivationDecision(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	data := UsersData{
		Shell: ShellData{Page: PageData{AppName: "HackWerk"}, Actor: auth.Actor{Role: auth.RoleAdmin}, CSRFToken: "csrf"},
		Users: []auth.UserSummary{{
			ID: "user-1", Username: "fahrer", DisplayName: "Fahrer Test", Role: auth.RoleDriver, Active: true, Version: 7,
		}},
	}
	if err := Users(data).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	for _, contract := range []string{
		`class="form-stack user-manage__panel user-access-role"`,
		`class="form-stack user-manage__panel user-access-status"`,
		`name="active" value="true"`,
		`name="role" value="driver"`,
		`Berechtigungen gelten ab der nächsten Anfrage`,
		`Alle aktiven Sitzungen dieses Zugangs werden sofort widerrufen`,
	} {
		if !strings.Contains(markup, contract) {
			t.Errorf("separated access controls do not preserve %q", contract)
		}
	}
	if strings.Contains(markup, "Rolle &amp; Status") || strings.Contains(markup, "Zugriff speichern") {
		t.Fatal("role and active state remain bundled in one ambiguous decision")
	}
}

func TestVoiceCaptureExplainsLocalGermanProcessing(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	data := VoiceCaptureData{
		Shell: ShellData{Page: PageData{AppName: "HackWerk"}}, Enabled: true, LocalProvider: true,
		MaxBytes: 15 << 20, MaxSeconds: 90, ProcessingMinutes: 10,
	}
	if err := VoiceCapture(data).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	for _, expected := range []string{"voice-info__icon", "primär als deutsche Sprache", "internen CPU-Dienst", "bis zu 10 Minuten", "immer nur ein Entwurf", `for="voice-level-meter"`, `for="voice-upload-progress"`, `name="idempotency_key"`} {
		if !strings.Contains(markup, expected) {
			t.Errorf("voice page missing %q", expected)
		}
	}
	if strings.Contains(markup, "Externer Anbieter:") {
		t.Fatal("local voice page claims an external provider")
	}
}
