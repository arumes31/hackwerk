//go:build e2e

package e2e_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/app"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/planning"
	"example.invalid/hackplan/internal/web"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

type e2ePlanningAvailability struct{ service *driver.Service }

func (a e2ePlanningAvailability) Resolve(ctx context.Context, actor auth.Actor, driverID string, from, to time.Time) ([]planning.Interval, error) {
	values, err := a.service.ResolveAvailability(ctx, actor, driverID, from, to)
	if err != nil {
		return nil, err
	}
	result := make([]planning.Interval, 0, len(values))
	for _, value := range values {
		result = append(result, planning.Interval{StartsAt: value.StartsAt, EndsAt: value.EndsAt, Status: string(value.Status)})
	}
	return result, nil
}

func TestTask08PlanningSuggestionsMobileJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, _, _, jobID, _, adminPassword, _ := task04Application(t, databaseURL)
	if _, err := pool.Exec(t.Context(), "UPDATE jobs SET pile_latitude=48.210000,pile_longitude=14.210000,pile_location_source='coordinates',pile_location_updated_at=now() WHERE id=$1", jobID); err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("Europe/Vienna")
	planningConfig := planning.DefaultConfig(location)
	planningConfig.HorizonDays = 56
	planningConfig.CandidateLimit = 2000
	planningService, err := planning.New(postgres.NewPlanningStore(pool), e2ePlanningAvailability{service: drivers}, planning.NewHaversineRouter(1.3, 55), planningConfig, func() time.Time { return time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	routeAdapter := planning.NewHaversineRouter(1.3, 55)
	routeService, err := planning.NewRouteService(postgres.NewRouteStore(pool), routeAdapter, routeAdapter, planning.DefaultRouteConfig())
	if err != nil {
		t.Fatal(err)
	}
	customerService, err := app.CustomerService(pool)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	cfg := config.Config{AppName: "HackWerk", BaseURL: "http://" + server.Listener.Addr().String(), Timezone: "Europe/Vienna", Database: config.Database{ReadinessTimeout: 2 * time.Second}, Auth: config.Auth{SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf", SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour}, Planning: config.Planning{BusinessOpen: "07:00", DepotLatitude: 48.2, DepotLongitude: 14.2}}
	router, err := web.NewRouter(web.Dependencies{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"}, Identity: identity, Customers: customerService, Drivers: drivers, Resources: resources, Appointments: appointments, Dashboard: e2eDashboard(t, pool), Planning: planningService, Routes: routeService})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = router
	server.Start()
	t.Cleanup(server.Close)
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(360, 800))
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browser, cancelBrowser := chromedp.NewContext(allocator)
	t.Cleanup(cancelBrowser)
	browser, cancelTimeout := context.WithTimeout(browser, 300*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browser) })
	var text string
	var overflow, smallTarget bool
	if err := chromedp.Run(browser, chromedp.Navigate(server.URL+"/login"), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery), chromedp.SetValue("#username", "admin-task04", chromedp.ByQuery), chromedp.SetValue("#password", adminPassword, chromedp.ByQuery), chromedp.Click("form[action='/login'] button", chromedp.ByQuery), chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	var fallbackSourceSelected, fallbackSelected bool
	var fallbackLocation string
	if err := runBrowserStep(browser, "select route without JavaScript",
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(true).Do(ctx) }),
		chromedp.Navigate(server.URL+"/planning"),
		chromedp.WaitVisible("input[form='planning-route-selection'][value='"+jobID+"']", chromedp.ByQuery),
		chromedp.Focus("input[form='planning-route-selection'][value='"+jobID+"']", chromedp.ByQuery),
		chromedp.KeyEvent(" "),
		chromedp.Poll(`document.querySelector("input[form='planning-route-selection'][value='`+jobID+`']").checked`, nil),
		chromedp.Evaluate(`document.querySelector("input[form='planning-route-selection'][value='`+jobID+`']").checked`, &fallbackSourceSelected),
		chromedp.Focus("[data-planning-route]", chromedp.ByQuery),
		chromedp.KeyEvent("\r"),
		chromedp.WaitVisible("main.route-page [data-route-context][data-route-admin='true']", chromedp.ByQuery),
		chromedp.WaitVisible("form.route-builder[action='/planning/routes'] input[name='job_id'][value='"+jobID+"']", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("form.route-builder[action='/planning/routes'] input[name='job_id'][value='`+jobID+`']").checked`, &fallbackSelected),
		chromedp.Location(&fallbackLocation),
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(false).Do(ctx) }),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if !fallbackSourceSelected || !fallbackSelected || !strings.Contains(fallbackLocation, "/planning/routes?job_id="+jobID) {
		t.Fatalf("no-JavaScript route selection source/target/location=%v/%v/%q", fallbackSourceSelected, fallbackSelected, fallbackLocation)
	}
	planningLink := "a[href^='/planning?job_id=" + jobID + "']"
	if err := runBrowserStep(browser, "open planning", chromedp.Navigate(server.URL+"/waitlist"), chromedp.WaitVisible("tr[data-job-id='"+jobID+"'] .row-actions > summary", chromedp.ByQuery), chromedp.Click("tr[data-job-id='"+jobID+"'] .row-actions > summary", chromedp.ByQuery), chromedp.WaitVisible(planningLink, chromedp.ByQuery), chromedp.Click(planningLink, chromedp.ByQuery), chromedp.WaitVisible("form[action='/planning/suggestions']", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if err := runBrowserStep(browser, "calculate suggestions", chromedp.Click("form[action='/planning/suggestions'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible(".suggestion-card", chromedp.ByQuery), chromedp.Text("main", &text, chromedp.ByQuery), chromedp.Evaluate(`document.documentElement.scrollWidth>window.innerWidth`, &overflow), chromedp.Evaluate(`Array.from(document.querySelectorAll('.suggestion-card .button')).some(e=>e.getBoundingClientRect().height<44||e.getBoundingClientRect().width<44)`, &smallTarget)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if overflow || smallTarget || !strings.Contains(text, "Beste berechnete Option") || !strings.Contains(text, "Fahrer") || !strings.Contains(text, "Ressourcen") || !strings.Contains(text, "Luftlinie / Schätzung") {
		t.Fatalf("planning mobile overflow/small=%v/%v text=%s", overflow, smallTarget, text)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE jobs SET version=version+1 WHERE id=$1", jobID); err != nil {
		t.Fatal(err)
	}
	var staleText, staleJobID string
	var staleDisabled bool
	if err := runBrowserStep(
		browser,
		"reject stale proposal",
		chromedp.Click(".suggestion-card:first-child form[action$='/adopt'] button", chromedp.ByQuery),
		chromedp.WaitVisible(".form-alert", chromedp.ByQuery),
		chromedp.Text("main", &staleText, chromedp.ByQuery),
		chromedp.Value(".module-card form[action='/planning/suggestions'] input[name='job_id']", &staleJobID, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('.suggestion-card:first-child form[action$="/adopt"] button').disabled`, &staleDisabled),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if staleJobID != jobID || !staleDisabled || !strings.Contains(staleText, "veraltet") || !strings.Contains(staleText, "Neu berechnen") {
		t.Fatalf("stale context job/disabled/text=%q/%v/%s", staleJobID, staleDisabled, staleText)
	}
	if err := runBrowserStep(
		browser,
		"recalculate stale proposal",
		chromedp.Click(".module-card form[action='/planning/suggestions'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible(".suggestion-card", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if err := runBrowserStep(
		browser,
		"adopt and focus proposal",
		chromedp.Click(".suggestion-card:first-child form[action$='/adopt'] button", chromedp.ByQuery),
		chromedp.WaitVisible("[data-calendar]", chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-calendar]')?.dataset.focusedAppointment`, nil),
		chromedp.WaitVisible("[data-appointment-dialog][open]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	var appointmentID, lifecycle, appointmentDate string
	var outbox int
	if err := pool.QueryRow(t.Context(), "SELECT id::text,lifecycle_status,to_char(starts_at AT TIME ZONE 'Europe/Vienna','YYYY-MM-DD') FROM appointments WHERE job_id=$1", jobID).Scan(&appointmentID, &lifecycle, &appointmentDate); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM outbox_events WHERE aggregate_id IN (SELECT id FROM appointments WHERE job_id=$1)", jobID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	var focusedAppointment, calendarDate string
	if err := chromedp.Run(
		browser,
		chromedp.Evaluate(`document.querySelector('[data-calendar]').dataset.focusedAppointment`, &focusedAppointment),
		chromedp.Evaluate(`(() => { const values = Object.fromEntries(new Intl.DateTimeFormat('de-AT', {timeZone:'Europe/Vienna',year:'numeric',month:'2-digit',day:'2-digit'}).formatToParts(window.hackWerkCalendar.getDate()).map(part => [part.type, part.value])); return values.year+'-'+values.month+'-'+values.day; })()`, &calendarDate),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if lifecycle != "proposal" || outbox != 0 || focusedAppointment != appointmentID || calendarDate != appointmentDate {
		t.Fatalf("adopted lifecycle/outbox/focus/date=%s/%d/%q/%q want focus/date=%q/%q", lifecycle, outbox, focusedAppointment, calendarDate, appointmentID, appointmentDate)
	}
}
