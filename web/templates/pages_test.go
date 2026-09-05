package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/dashboard"
	"example.invalid/hackplan/internal/planning"
)

func TestCustomersUsesNeutralNewActionLabel(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	data := CustomerListData{
		Shell: ShellData{Page: PageData{AppName: "HackWerk"}},
		Page:  customers.Page[customers.CustomerSummary]{},
	}
	if err := Customers(data).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	if !strings.Contains(markup, `href="/customers/new">Neu</a>`) {
		t.Fatalf("customer list is missing neutral new action: %s", markup)
	}
	if strings.Contains(markup, `href="/customers/new">Neuer Auftrag</a>`) {
		t.Fatalf("customer list still uses order-only action label: %s", markup)
	}
}

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
	if strings.Index(calendar, "fullcalendar-palette.css") > strings.Index(calendar, "mobile-app.css") {
		t.Fatal("mobile app presentation layer must load after calendar styles so its responsive hierarchy wins the cascade")
	}
	if strings.Index(calendar, "presentation-bootstrap.js") > strings.Index(calendar, "control-foundation.css") {
		t.Fatal("presentation preferences must be applied before styles load")
	}
}

func TestMobileShellUsesOnlyBottomNavigationAndKeepsAdminDestinationsInMore(t *testing.T) {
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
	if strings.Contains(markup, `mobile-app-bar__title`) {
		t.Fatal("mobile utility bar must not repeat the page title")
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
	if strings.Contains(markup, `class="mobile-admin-nav"`) {
		t.Fatal("admin shell must not render a second mobile navigation")
	}
	if !strings.Contains(markup, `data-actor-role="admin"`) {
		t.Fatal("admin shell must expose a non-visual role hook for role-scoped mobile layout")
	}
	moreStart := strings.Index(markup, `<dialog id="mobile-more-sheet"`)
	if moreStart < 0 {
		t.Fatal("admin mobile More sheet is missing")
	}
	moreEndOffset := strings.Index(markup[moreStart:], `</dialog>`)
	if moreEndOffset < 0 {
		t.Fatal("admin mobile More sheet is not closed")
	}
	more := markup[moreStart : moreStart+moreEndOffset]
	for _, href := range []string{
		`href="/planning"`,
		`href="/planning/routes"`,
		`href="/transport-partners"`,
		`href="/admin/drivers"`,
		`href="/admin/resources"`,
		`href="/settings/route-locations"`,
		`href="/admin/notifications"`,
		`href="/admin/voice-recordings"`,
		`href="/admin/users"`,
	} {
		if !strings.Contains(more, href) {
			t.Errorf("admin mobile More sheet is missing %s", href)
		}
	}

	output.Reset()
	shell.Actor.Role = auth.RoleDriver
	shell.Actor.DriverID = "driver-1"
	if err := appHeader(shell).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	driverMarkup := output.String()
	if strings.Contains(driverMarkup, `class="mobile-admin-nav"`) {
		t.Fatal("driver shell must not render a second mobile navigation")
	}
	if !strings.Contains(driverMarkup, `data-actor-role="driver"`) {
		t.Fatal("driver shell must expose a non-visual role hook for role-scoped mobile layout")
	}
	if strings.Contains(driverMarkup, `href="/transport-partners"`) {
		t.Fatal("driver shell must not expose transport partners outside the administration menu")
	}
	driverNavigationStart := strings.Index(driverMarkup, `class="mobile-bottom-nav"`)
	if driverNavigationStart < 0 {
		t.Fatal("driver mobile bottom navigation boundaries are missing")
	}
	driverNavigationEndOffset := strings.Index(driverMarkup[driverNavigationStart:], `</nav>`)
	if driverNavigationEndOffset < 0 {
		t.Fatal("driver mobile bottom navigation boundaries are missing")
	}
	driverNavigation := driverMarkup[driverNavigationStart : driverNavigationStart+driverNavigationEndOffset]
	if !strings.Contains(driverNavigation, `>Übersicht</span>`) || strings.Contains(driverNavigation, `>Heute</span>`) {
		t.Fatal("driver bottom navigation must describe the dashboard as an overview, including on future dates")
	}
	for _, destination := range []string{`href="/dashboard"`, `href="/my-route"`, `href="/calendar"`, `href="/availability"`} {
		if !strings.Contains(driverNavigation, destination) {
			t.Errorf("driver mobile navigation is missing field destination %s", destination)
		}
	}
	for _, officeDestination := range []string{`href="/waitlist"`, `href="/customers"`} {
		if strings.Contains(driverNavigation, `data-mobile-nav-item `+officeDestination) {
			t.Errorf("driver mobile navigation must move office destination %s into More", officeDestination)
		}
	}
	driverMoreStart := strings.Index(driverMarkup, `<dialog id="mobile-more-sheet"`)
	if driverMoreStart < 0 {
		t.Fatal("driver mobile More sheet boundaries are missing")
	}
	driverMoreEndOffset := strings.Index(driverMarkup[driverMoreStart:], `</dialog>`)
	if driverMoreEndOffset < 0 {
		t.Fatal("driver mobile More sheet boundaries are missing")
	}
	driverMore := driverMarkup[driverMoreStart : driverMoreStart+driverMoreEndOffset]
	for _, destination := range []string{`href="/waitlist"`, `href="/customers"`} {
		if !strings.Contains(driverMore, destination) {
			t.Errorf("driver mobile More sheet is missing office destination %s", destination)
		}
	}
	if !strings.Contains(driverMore, `data-install-open`) {
		t.Fatal("driver mobile More sheet must provide a deliberate install entry instead of an automatic overlay")
	}
	for _, contract := range []string{`data-install-later`, `data-install-dismiss`, `>Später</button>`, `>Nicht mehr anzeigen</button>`} {
		if !strings.Contains(driverMarkup, contract) {
			t.Errorf("install prompt is missing non-coercive action %q", contract)
		}
	}
	for _, primaryDestination := range []string{`href="/my-route"`, `href="/availability"`} {
		if strings.Contains(driverMore, primaryDestination) {
			t.Errorf("driver mobile More sheet must not duplicate primary destination %s", primaryDestination)
		}
	}

	output.Reset()
	shell.Actor.DriverID = ""
	if err := appHeader(shell).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	withoutProfile := output.String()
	for _, destination := range []string{`href="/my-route"`, `href="/availability"`} {
		if !strings.Contains(withoutProfile, `data-mobile-nav-item `+destination) {
			t.Errorf("driver without a linked profile must keep the role-specific mobile destination %s", destination)
		}
	}
}

func TestAdminDesktopGroupsDispositionWithoutChangingDriverNavigation(t *testing.T) {
	t.Parallel()

	render := func(role auth.Role, activePath string) string {
		t.Helper()
		var output bytes.Buffer
		shell := ShellData{
			Page:       PageData{AppName: "HackWerk"},
			ActivePath: activePath,
			Actor:      auth.Actor{Role: role, DisplayName: "Test"},
		}
		if err := appHeader(shell).Render(context.Background(), &output); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}

	admin := render(auth.RoleAdmin, "/planning/routes")
	desktopEnd := strings.Index(admin, `class="mobile-app-bar"`)
	if desktopEnd < 0 {
		t.Fatal("desktop shell boundary is missing")
	}
	desktop := admin[:desktopEnd]
	menuStart := strings.Index(desktop, `class="nav-menu disposition-nav"`)
	if menuStart < 0 {
		t.Fatal("admin desktop navigation must group disposition destinations")
	}
	menu := desktop[menuStart:]
	for _, contract := range []string{
		`<summary>Disposition</summary>`,
		`href="/calendar"`,
		`href="/waitlist"`,
		`href="/planning"`,
		`>Vorschläge</a>`,
		`href="/planning/routes"`,
		`>Tagesrouten</a>`,
	} {
		if !strings.Contains(menu, contract) {
			t.Errorf("admin disposition navigation is missing %q", contract)
		}
	}
	if strings.Count(desktop, `aria-current="page"`) != 1 {
		t.Fatal("admin desktop navigation must expose exactly one current destination")
	}

	driver := render(auth.RoleDriver, "/calendar")
	driverDesktopEnd := strings.Index(driver, `class="mobile-app-bar"`)
	if driverDesktopEnd < 0 {
		t.Fatal("driver desktop shell boundary is missing")
	}
	driverDesktop := driver[:driverDesktopEnd]
	if strings.Contains(driverDesktop, `class="nav-menu disposition-nav"`) {
		t.Fatal("driver desktop navigation must keep its direct field destinations")
	}
	for _, contract := range []string{`href="/calendar"`, `href="/waitlist"`} {
		if !strings.Contains(driverDesktop, contract) {
			t.Errorf("driver desktop navigation is missing %q", contract)
		}
	}
}

func TestDesktopTransportPartnersNavigationIsGroupedUnderAdministration(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	shell := ShellData{
		Page:       PageData{AppName: "HackWerk"},
		ActivePath: "/transport-partners",
		Actor:      auth.Actor{Role: auth.RoleAdmin, DisplayName: "Test"},
	}
	if err := appHeader(shell).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	primaryStart := strings.Index(markup, `<nav class="primary-nav"`)
	primaryEnd := strings.Index(markup[primaryStart:], `</nav>`)
	if primaryStart < 0 || primaryEnd < 0 {
		t.Fatal("desktop primary navigation boundaries are missing")
	}
	primary := markup[primaryStart : primaryStart+primaryEnd]
	if strings.Contains(primary, `href="/transport-partners"`) {
		t.Fatal("transport partners must not remain in the desktop primary navigation")
	}

	adminStart := strings.Index(markup, `data-admin-menu`)
	adminEnd := strings.Index(markup[adminStart:], `</details>`)
	if adminStart < 0 || adminEnd < 0 {
		t.Fatal("desktop administration menu boundaries are missing")
	}
	adminMenu := markup[adminStart : adminStart+adminEnd]
	if !strings.Contains(adminMenu, `href="/transport-partners" class="nav-link nav-link--active"`) {
		t.Fatal("desktop administration menu must contain the active transport partners destination")
	}
}

func TestDashboardRendersRoleSpecificChronologicalMobileToursWithoutChangingDesktopProjection(t *testing.T) {
	t.Parallel()

	firstStart := time.Date(2026, time.August, 25, 8, 30, 0, 0, time.FixedZone("Europe/Vienna", 2*60*60))
	first := dashboard.Appointment{
		ID: "appointment-1", JobID: "job-1", CustomerID: "customer-1", JobNumber: "HW-101", Lifecycle: "fixed", Confirmation: "accepted",
		JobType: "whole_tree", VolumeM3: "55.00", CustomerName: "Erster Halt", Locality: "Krems",
		Drivers: "Franz Fahrer", Resources: "Hackmaschine 1", Chippers: "Hackmaschine 1", MapsURL: "https://maps.example/first", StartsAt: firstStart, EndsAt: firstStart.Add(2 * time.Hour),
	}
	second := dashboard.Appointment{
		ID: "appointment-2", JobID: "job-2", CustomerID: "customer-2", Lifecycle: "proposed", Confirmation: "pending",
		JobType: "logs", VolumeM3: "18", CustomerName: "Zweiter Halt", Locality: "Melk",
		Drivers: "Franz Fahrer", Resources: "Hackmaschine 1", MapsURL: "https://maps.example/second", StartsAt: firstStart.Add(3 * time.Hour), EndsAt: firstStart.Add(5 * time.Hour),
	}
	render := func(admin bool, driverID string, ownRouteAvailable, ownRouteLookupFailed bool) string {
		t.Helper()
		var output bytes.Buffer
		component := Dashboard(DashboardData{
			Shell:                ShellData{Page: PageData{AppName: "HackWerk"}, ActivePath: "/dashboard", Actor: auth.Actor{Role: auth.RoleDriver, DriverID: driverID, DisplayName: "Test Fahrer"}},
			OwnRouteAvailable:    ownRouteAvailable,
			OwnRouteLookupFailed: ownRouteLookupFailed,
			View: dashboard.View{
				Admin: admin, Date: "2026-08-25", DateLabel: "Dienstag, 25. August", PreviousDate: "2026-08-24", NextDate: "2026-08-26",
				Today:  []dashboard.Appointment{first, second},
				Groups: []dashboard.AppointmentGroup{{ResourceName: "Hackmaschine 1", Appointments: []dashboard.Appointment{second, first}}},
				Counts: dashboard.Counts{Attention: 2, OverdueConfirmations: 1, Appointments: 2, Waitlist: 4, ActiveDrivers: 5, VoiceDrafts: 1},
			},
		})
		if admin {
			component = Dashboard(DashboardData{
				Shell: ShellData{Page: PageData{AppName: "HackWerk"}, ActivePath: "/dashboard", Actor: auth.Actor{Role: auth.RoleAdmin, DisplayName: "Test Admin"}},
				View:  dashboard.View{Admin: true, Date: "2026-08-25", DateLabel: "Dienstag, 25. August", PreviousDate: "2026-08-24", NextDate: "2026-08-26", Today: []dashboard.Appointment{first, second}, Groups: []dashboard.AppointmentGroup{{ResourceName: "Hackmaschine 1", Appointments: []dashboard.Appointment{first, second}}}, Counts: dashboard.Counts{Attention: 1}},
			})
		}
		if err := component.Render(context.Background(), &output); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}

	driverMarkup := render(false, "driver-1", true, false)
	for _, contract := range []string{
		`data-dashboard-role="driver"`,
		`data-dashboard-projection="driver-tour"`,
		`data-dashboard-projection="resource-groups"`,
		`href="/my-route?date=2026-08-25"`,
		`class="driver-tour__quick-actions"`,
		`class="driver-mobile-availability"`,
		`>Meine Tour</h2>`,
		`data-driver-tour-appointment`,
	} {
		if !strings.Contains(driverMarkup, contract) {
			t.Errorf("driver dashboard is missing mobile tour contract %q", contract)
		}
	}
	if strings.Contains(driverMarkup, `data-dashboard-projection="admin-tour"`) {
		t.Fatal("driver dashboard must not render the admin tour projection")
	}
	for _, forbidden := range []string{"Aufmerksamkeit nötig", "offene Kundenantworten", "Bestätigungen überfällig", `class="dashboard-metrics"`, `class="dashboard-primary-action"`} {
		if strings.Contains(driverMarkup, forbidden) {
			t.Errorf("driver dashboard must not render non-actionable office UI %q", forbidden)
		}
	}
	if !strings.Contains(driverMarkup, `>55 m³</strong>`) || strings.Contains(driverMarkup, `55.00 m³`) {
		t.Fatal("driver dashboard must render volume in de-AT notation without redundant decimals")
	}
	if !strings.Contains(driverMarkup, "Einsätze in zeitlicher Reihenfolge") || strings.Contains(driverMarkup, "eigene Stopps zuerst") {
		t.Fatal("driver tour copy must describe the chronology that is actually rendered")
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
	withoutProfile := render(false, "", false, false)
	if strings.Contains(withoutProfile, `class="button button--quiet driver-tour__route"`) {
		t.Fatal("driver without an assigned profile must not receive the own-route action")
	}
	withoutRoute := render(false, "driver-1", false, false)
	if strings.Contains(withoutRoute, `href="/my-route?date=2026-08-25"`) || strings.Contains(withoutRoute, `class="driver-tour__quick-actions"`) || !strings.Contains(withoutRoute, "Navigation direkt beim nächsten Einsatz starten") {
		t.Fatal("driver without an assigned route must get direct-stop guidance instead of a dead-end route action")
	}
	lookupFailed := render(false, "driver-1", false, true)
	if !strings.Contains(lookupFailed, "Routenstatus konnte nicht geprüft werden") || !strings.Contains(lookupFailed, `href="/my-route?date=2026-08-25"`) || strings.Contains(lookupFailed, "Keine gespeicherte Route") {
		t.Fatal("driver must receive a recoverable route status when the lookup fails")
	}

	adminMarkup := render(true, "", false, false)
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
	for _, redundant := range []string{
		`Einsatzübersicht`,
		`class="dashboard-exceptions"`,
		`<p class="eyebrow">Disposition</p>`,
		`<span class="eyebrow">Tag</span>`,
		`<p class="eyebrow">Priorität</p>`,
	} {
		if strings.Contains(adminMarkup, redundant) {
			t.Errorf("admin dashboard still renders redundant context %q", redundant)
		}
	}
	if strings.Contains(driverMarkup, `<p class="eyebrow">Unterwegs</p>`) {
		t.Fatal("driver tour heading must stand without a redundant eyebrow")
	}
	resourceGroupsStart := strings.Index(adminMarkup, `data-dashboard-projection="resource-groups"`)
	attentionStart := strings.Index(adminMarkup, `class="dashboard-attention"`)
	metricsStart := strings.Index(adminMarkup, `class="dashboard-metrics"`)
	if resourceGroupsStart < 0 || attentionStart < 0 || metricsStart < 0 || resourceGroupsStart > attentionStart || attentionStart > metricsStart {
		t.Fatal("dashboard accessibility order must lead with the day schedule, then its exceptions, then supporting metrics")
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
	if introStart := strings.Index(adminMarkup, `class="dashboard-intro"`); introStart < 0 || introStart > adminTourStart {
		t.Fatal("desktop introduction must keep the page h1 before the mobile disposition in the accessibility order")
	}
	for _, contract := range []string{
		`class="admin-tour__date-nav"`,
		`href="/dashboard?date=2026-08-24"`,
		`href="/dashboard?date=2026-08-26"`,
		`href="/dashboard"`,
		`href="/dashboard?date=2026-08-25"`,
		`href="/dashboard?date=2026-08-25&amp;mode=exceptions"`,
		`data-print-page`,
	} {
		if !strings.Contains(adminTourMarkup, contract) {
			t.Errorf("admin mobile disposition is missing self-contained control %q", contract)
		}
	}
	if strings.Count(adminTourMarkup, `aria-current="page"`) != 1 {
		t.Fatal("admin mobile disposition must expose exactly one current mode without relying on color")
	}
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

func TestCalendarKeepsWaitlistBeforeBoardAndMakesItCollapsible(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	component := Calendar(CalendarData{
		Shell:    ShellData{Page: PageData{AppName: "HackWerk"}, ActivePath: "/calendar", Actor: auth.Actor{Role: auth.RoleAdmin, DisplayName: "Test Admin"}},
		Timezone: "Europe/Vienna",
	})
	if err := component.Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	waitlistStart := strings.Index(markup, `<details class="calendar-waitlist"`)
	boardStart := strings.Index(markup, `class="calendar-board"`)
	if waitlistStart < 0 || boardStart < 0 || waitlistStart > boardStart {
		t.Fatal("calendar reading and focus order must keep the left waitlist before the planning board")
	}
	waitlistEnd := strings.Index(markup[waitlistStart:], `</details>`)
	if waitlistEnd < 0 {
		t.Fatal("calendar waitlist must be a native disclosure")
	}
	waitlistMarkup := markup[waitlistStart : waitlistStart+waitlistEnd]
	for _, contract := range []string{`<summary`, `id="calendar-waitlist-title"`, `data-calendar-waitlist`} {
		if !strings.Contains(waitlistMarkup, contract) {
			t.Errorf("calendar waitlist disclosure is missing %q", contract)
		}
	}
	for _, contract := range []string{`Gemeinsamer Plan für alle Fahrer.`, `data-calendar-timezone`} {
		if !strings.Contains(markup, contract) {
			t.Errorf("distilled calendar is missing essential context %q", contract)
		}
	}
	for _, redundant := range []string{
		`Zentrale Einsatzplanung`,
		`calendar-timezone-badge`,
		`<p class="eyebrow">Unverbindlicher Vorschlag</p>`,
		`<p class="eyebrow">Termindetail</p>`,
		`<p class="eyebrow">Nebenwirkungsfreie Vorschau</p>`,
	} {
		if strings.Contains(markup, redundant) {
			t.Errorf("calendar still renders redundant context %q", redundant)
		}
	}
}

func TestAdminCalendarDistillsSecondaryToolsAndShowsPlanningLifecycle(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	data := CalendarData{
		Shell: ShellData{Page: PageData{AppName: "HackWerk"}, ActivePath: "/calendar", Actor: auth.Actor{Role: auth.RoleAdmin}},
	}
	if err := Calendar(data).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	optionsStart := strings.Index(markup, `<details class="calendar-options">`)
	if optionsStart < 0 {
		t.Fatal("calendar secondary tools must use a closed native disclosure")
	}
	optionsEnd := strings.Index(markup[optionsStart:], `</details>`)
	if optionsEnd < 0 {
		t.Fatal("calendar secondary tools disclosure is not closed")
	}
	options := markup[optionsStart : optionsStart+optionsEnd]
	for _, contract := range []string{
		`<summary>Datum &amp; weitere Optionen</summary>`,
		`data-calendar-weekends`,
		`data-calendar-reload`,
		`data-calendar-share`,
		`data-print-page`,
		`calendar-edit-hint`,
	} {
		if !strings.Contains(options, contract) {
			t.Errorf("calendar options disclosure is missing %q", contract)
		}
	}
	for _, step := range []string{"Entwurf", "Vorschlag", "Fixiert", "Nachricht"} {
		if !strings.Contains(markup, step) {
			t.Errorf("calendar planning lifecycle is missing %q", step)
		}
	}
}

func TestWaitlistUsesNamedWorkViewsAndPlainPlanningLanguage(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	data := WaitlistData{
		Shell: ShellData{Page: PageData{AppName: "HackWerk"}, ActivePath: "/waitlist", Actor: auth.Actor{Role: auth.RoleAdmin}},
		Page: customers.Page[customers.WaitlistItem]{
			Items: []customers.WaitlistItem{{JobID: "job-1", WaitlistID: "wait-1", JobNumber: "HW-1", CustomerID: "customer-1"}},
			Total: 1, UnfilteredTotal: 1,
		},
	}
	if err := Waitlist(data).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	for _, contract := range []string{
		`class="waitlist-work-views"`,
		`>Dringend</a>`,
		`>Noch ungeplant</a>`,
		`>Daten prüfen</a>`,
		`<th>Planungsstand / Priorität</th>`,
		`data-label="Planungsstand / Priorität"`,
		`data-waitlist-selection-toolbar hidden`,
	} {
		if !strings.Contains(markup, contract) {
			t.Errorf("distilled waitlist is missing %q", contract)
		}
	}
	if strings.Contains(markup, `<span class="status-badge">Aktive Filter</span>`) {
		t.Fatal("active-filter container must not repeat its accessible label as a badge")
	}
}

func TestPlanningAndRoutesUseOneLifecycleAndAThreeStepRouteFlow(t *testing.T) {
	t.Parallel()

	var planningOutput bytes.Buffer
	if err := Planning(PlanningData{Shell: ShellData{Page: PageData{AppName: "HackWerk"}, Actor: auth.Actor{Role: auth.RoleAdmin}}}).Render(context.Background(), &planningOutput); err != nil {
		t.Fatal(err)
	}
	planningMarkup := planningOutput.String()
	for _, contract := range []string{`<h1>Planungsvorschläge</h1>`, `Vorschläge vergleichen und unverbindlich in den Kalender übernehmen.`, `>Vorschläge</a>`, `>Tagesrouten</a>`} {
		if !strings.Contains(planningMarkup, contract) {
			t.Errorf("planning page is missing %q", contract)
		}
	}
	if strings.Contains(planningMarkup, `>Keine automatische Fixierung</span>`) {
		t.Fatal("planning header must not repeat the lifecycle safety rule")
	}

	var routeOutput bytes.Buffer
	if err := adminRoutePlanner(RoutePageData{}).Render(context.Background(), &routeOutput); err != nil {
		t.Fatal(err)
	}
	routeMarkup := routeOutput.String()
	selectAt := strings.Index(routeMarkup, "Aufträge auswählen")
	configureAt := strings.Index(routeMarkup, "Route einrichten")
	reviewAt := strings.Index(routeMarkup, "Route prüfen")
	if selectAt < 0 || configureAt <= selectAt || reviewAt <= configureAt {
		t.Fatalf("route flow must read select, configure, review: %s", routeMarkup)
	}
	for _, contract := range []string{
		`data-route-has-draft="false"`,
		`class="route-map-legend-disclosure"`,
		`data-route-map-retry hidden`,
	} {
		if !strings.Contains(routeMarkup, contract) {
			t.Errorf("route planner is missing %q", contract)
		}
	}
}

func TestAdminRoutePrimaryActionUsesStickyActionContainer(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if err := adminRoutePlanner(RoutePageData{}).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	button := strings.Index(markup, `>Route berechnen</button>`)
	if button < 0 {
		t.Fatal("admin route planner is missing its primary submit action")
	}
	container := strings.LastIndex(markup[:button], `class="form-actions"`)
	if container < 0 || strings.Contains(markup[container:button], `</div>`) {
		t.Fatal("Route berechnen must be inside the sticky form-actions container")
	}
}

func TestOwnRouteHeadingDoesNotRepeatTheDriverContextAsAnEyebrow(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	data := RoutePageData{
		Shell: ShellData{Page: PageData{AppName: "HackWerk"}, Actor: auth.Actor{Role: auth.RoleDriver, DriverID: "driver-1"}},
		Own:   true,
	}
	if err := Routes(data).Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	markup := output.String()
	mainAt := strings.Index(markup, `<main`)
	if mainAt < 0 {
		t.Fatal("own route main content is missing")
	}
	headingAt := strings.Index(markup[mainAt:], `<h1>Meine Route</h1>`)
	if headingAt < 0 {
		t.Fatal("own route heading is missing")
	}
	if strings.Contains(markup[mainAt:mainAt+headingAt], `class="eyebrow"`) {
		t.Fatal("own route heading must not repeat the driver context as an eyebrow")
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
		{name: "driver subpage", path: "/waitlist", href: "/waitlist", label: "Warteliste", role: auth.RoleDriver, driverID: "driver-1"},
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
