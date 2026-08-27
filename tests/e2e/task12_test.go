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
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/planning"
	"example.invalid/hackplan/internal/web"
	"github.com/chromedp/chromedp"
)

func TestTask12RoutePlannerDesktopAndDriverMobileJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, driverID, chipperID, jobID, secondJobID, adminPassword, driverPassword := task04Application(t, databaseURL)
	if _, err := pool.Exec(t.Context(), `UPDATE jobs
		SET pile_latitude=CASE id WHEN $1 THEN 48.210000 ELSE 48.245000 END,
		    pile_longitude=CASE id WHEN $1 THEN 14.210000 ELSE 14.265000 END,
		    pile_location_source='map_pin', pile_location_updated_at=now()
		WHERE id=ANY($2::uuid[])`, jobID, []string{jobID, secondJobID}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE customers
		SET phone_raw='+43660123456'
		WHERE id=(SELECT customer_id FROM jobs WHERE id=$1)`, jobID); err != nil {
		t.Fatal(err)
	}

	routerAdapter := planning.NewHaversineRouter(1.3, 55)
	routes, err := planning.NewRouteService(postgres.NewRouteStore(pool), routerAdapter, routerAdapter, planning.DefaultRouteConfig())
	if err != nil {
		t.Fatal(err)
	}
	customerService, err := app.CustomerService(pool)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		AppName: "HackWerk", BaseURL: "http://127.0.0.1:18533", Timezone: "Europe/Vienna",
		Database: config.Database{ReadinessTimeout: 2 * time.Second},
		Auth:     config.Auth{SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf", SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour},
		Planning: config.Planning{BusinessOpen: "07:00", DepotLatitude: 48.2, DepotLongitude: 14.2},
		Map:      config.Map{Attribution: "OpenStreetMap-Mitwirkende"},
	}
	handler, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"},
		Identity: identity, Customers: customerService, Drivers: drivers, Resources: resources, Appointments: appointments, Dashboard: e2eDashboard(t, pool), Routes: routes,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(1440, 900))
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browser, cancelBrowser := chromedp.NewContext(allocator)
	t.Cleanup(cancelBrowser)
	browser, cancelTimeout := context.WithTimeout(browser, 180*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browser) })

	if err := chromedp.Run(browser, chromedp.Navigate(server.URL+"/login"), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if err := runBrowserStep(browser, "admin login",
		chromedp.SetValue("#username", "admin-task04", chromedp.ByQuery), chromedp.SetValue("#password", adminPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}

	var compactDesktopOverflow, compactMobileOverflow bool
	var driverCreateOpen bool
	if err := runBrowserStep(browser, "compact driver list",
		chromedp.Navigate(server.URL+"/admin/drivers"), chromedp.WaitVisible(".driver-compact-row", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('.compact-create-panel').open`, &driverCreateOpen),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if err := runBrowserStep(browser, "compact customer list",
		chromedp.Navigate(server.URL+"/customers"), chromedp.WaitVisible(".customer-table", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if err := runBrowserStep(browser, "compact waitlist desktop",
		chromedp.Navigate(server.URL+"/waitlist"), chromedp.WaitVisible(".waitlist-table", chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &compactDesktopOverflow),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if err := runBrowserStep(browser, "compact waitlist mobile",
		chromedp.EmulateViewport(390, 844), chromedp.Reload(), chromedp.WaitVisible(".waitlist-table", chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &compactMobileOverflow),
		chromedp.EmulateViewport(1440, 900),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if driverCreateOpen || compactDesktopOverflow || compactMobileOverflow {
		t.Fatalf("compact default/create/desktop-overflow/mobile-overflow=%v/%v/%v", driverCreateOpen, compactDesktopOverflow, compactMobileOverflow)
	}

	var desktopOverflow, smallDesktopTarget, selectedByMap, mapReady bool
	var desktopLayout struct {
		TwoColumns, MapAboveFold, CompactCandidate bool
	}
	var adminRouteContext, mapToolbar, routeDatePresets bool
	var routePresetAudit struct {
		Actual, Expected []string
	}
	var candidateMarkerCount, depotMarkerCount int
	var smallDesktopTargets []string
	if err := runBrowserStep(browser, "plan desktop route",
		chromedp.Navigate(server.URL+"/planning/routes"), chromedp.WaitVisible("form[action='/planning/routes']", chromedp.ByQuery),
		chromedp.WaitVisible(".route-map-marker--candidate[data-job-id='"+jobID+"']", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-route-map]').dataset.mapReady === 'true'`, &mapReady),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-route-context][data-route-admin="true"]'))`, &adminRouteContext),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-route-map-toolbar]'))`, &mapToolbar),
		chromedp.Evaluate(`document.querySelectorAll('[data-route-date-preset]').length === 3`, &routeDatePresets),
		chromedp.Evaluate(`(() => {
			const input=document.querySelector('[data-route-day-filter]');
			const format=date=>[date.getFullYear(),String(date.getMonth()+1).padStart(2,'0'),String(date.getDate()).padStart(2,'0')].join('-');
			const today=new Date(), tomorrow=new Date(today), business=new Date(today);
			tomorrow.setDate(tomorrow.getDate()+1);
			do { business.setDate(business.getDate()+1); } while ([0,6].includes(business.getDay()));
			const actual=['today','tomorrow','business-day'].map(preset=>{document.querySelector('[data-route-date-preset="'+preset+'"]').click();return input.value});
			return {Actual:actual,Expected:[format(today),format(tomorrow),format(business)]};
		})()`, &routePresetAudit),
		chromedp.Evaluate(`document.querySelectorAll('.route-map-marker--candidate').length`, &candidateMarkerCount),
		chromedp.Evaluate(`document.querySelectorAll('.route-map-marker--depot').length`, &depotMarkerCount),
		chromedp.Evaluate(`(() => {
			const workspace=document.querySelector('[data-route-context][data-route-admin="true"]');
			const builder=workspace.querySelector('.route-builder').getBoundingClientRect();
			const panel=workspace.querySelector('.route-map-panel').getBoundingClientRect();
			const checkbox=workspace.querySelector('.route-candidate > input[type="checkbox"]').getBoundingClientRect();
			return {TwoColumns:builder.right<panel.left&&Math.abs(builder.top-panel.top)<4,
				MapAboveFold:panel.top<window.innerHeight&&panel.height>300,
				CompactCandidate:checkbox.width<=24&&checkbox.height<=24};
		})()`, &desktopLayout),
		chromedp.Click(".route-map-marker--candidate[data-job-id='"+jobID+"']", chromedp.ByQuery),
		chromedp.WaitVisible(".route-map-popup__action", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('.route-map-popup__action').click()`, nil),
		chromedp.Evaluate(`document.querySelector("input[name='job_id'][value='`+jobID+`']").checked`, &selectedByMap),
		chromedp.Click("input[name='job_id'][value='"+secondJobID+"']", chromedp.ByQuery),
		chromedp.SetValue("select[name='driver_id']", driverID, chromedp.ByQuery),
		chromedp.SetValue("select[name='chipper_resource_id']", chipperID, chromedp.ByQuery),
		chromedp.SetValue("input[name='departure_date']", "2026-09-01", chromedp.ByQuery),
		chromedp.SetValue("input[name='departure_time']", "07:00", chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &desktopOverflow),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('button,a.button')).some(e=>e.getBoundingClientRect().height>0&&e.getBoundingClientRect().height<44)`, &smallDesktopTarget),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('button,a.button')).filter(e=>e.getBoundingClientRect().height>0&&e.getBoundingClientRect().height<44).map(e=>e.textContent.trim()+':'+Math.round(e.getBoundingClientRect().height))`, &smallDesktopTargets),
		chromedp.Click("form[action='/planning/routes'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible(".route-summary", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if !mapReady || !adminRouteContext || !mapToolbar || !routeDatePresets || strings.Join(routePresetAudit.Actual, ",") != strings.Join(routePresetAudit.Expected, ",") || candidateMarkerCount != 2 || depotMarkerCount != 1 || !selectedByMap || desktopOverflow || smallDesktopTarget || !desktopLayout.TwoColumns || !desktopLayout.MapAboveFold || !desktopLayout.CompactCandidate {
		t.Fatalf("desktop route map-ready/admin/toolbar/presets/candidates/depot/selected/overflow/small-target/layout=%v/%v/%v/%v/%d/%d/%v/%v/%v/%+v targets=%v", mapReady, adminRouteContext, mapToolbar, routeDatePresets, candidateMarkerCount, depotMarkerCount, selectedByMap, desktopOverflow, smallDesktopTarget, desktopLayout, smallDesktopTargets)
	}

	var routeText string
	if err := chromedp.Run(browser, chromedp.Text("main", &routeText, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(routeText, "2 Stopps") || !strings.Contains(routeText, "Schätzung") || !strings.Contains(routeText, "keine Nachricht") {
		t.Fatalf("route summary misses safety information: %s", routeText)
	}

	var routeID, routeStatus string
	var routeVersion int
	if err := pool.QueryRow(t.Context(), "SELECT id::text,status,version FROM route_drafts ORDER BY created_at DESC LIMIT 1").Scan(&routeID, &routeStatus, &routeVersion); err != nil {
		t.Fatal(err)
	}
	if routeStatus != "draft" || routeVersion != 1 {
		t.Fatalf("planned route state=%s/%d", routeStatus, routeVersion)
	}
	var assignedSummaryCount, assignedFormCount int
	if err := runBrowserStep(browser, "assign proposals",
		chromedp.Evaluate(`document.documentElement.dataset.e2eNavigationMarker='pending'`, nil),
		chromedp.Evaluate(`document.querySelector("form[action='/planning/routes/`+routeID+`/assign']").requestSubmit()`, nil),
		chromedp.WaitNotPresent("html[data-e2e-navigation-marker='pending']", chromedp.ByQuery),
		chromedp.Navigate(server.URL+"/planning/routes?route_id="+routeID),
		chromedp.WaitReady("main", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('.route-summary').length`, &assignedSummaryCount),
		chromedp.Evaluate(`document.querySelectorAll("form[action$='/assign'] button[type='submit']").length`, &assignedFormCount),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if assignedSummaryCount != 1 || assignedFormCount != 0 {
		t.Fatalf("assigned route summary/form=%d/%d", assignedSummaryCount, assignedFormCount)
	}

	appointmentTimes := map[string][2]time.Time{}
	rows, err := pool.Query(t.Context(), `SELECT id::text,starts_at,ends_at FROM appointments
		WHERE job_id=ANY($1::uuid[]) ORDER BY starts_at`, []string{jobID, secondJobID})
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		var startsAt, endsAt time.Time
		if err := rows.Scan(&id, &startsAt, &endsAt); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		appointmentTimes[id] = [2]time.Time{startsAt, endsAt}
	}
	rows.Close()
	if len(appointmentTimes) != 2 {
		t.Fatalf("assigned proposal count=%d", len(appointmentTimes))
	}
	var assignedStatus string
	var proposalCount, outboxCount int
	if err := pool.QueryRow(t.Context(), "SELECT status FROM route_drafts WHERE id=$1", routeID).Scan(&assignedStatus); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM appointments WHERE job_id=ANY($1::uuid[]) AND lifecycle_status='proposal'", []string{jobID, secondJobID}).Scan(&proposalCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM outbox_events WHERE aggregate_id IN (SELECT id FROM appointments WHERE job_id=ANY($1::uuid[]))", []string{jobID, secondJobID}).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if assignedStatus != "assigned" || proposalCount != 2 || outboxCount != 0 {
		t.Fatalf("assignment status/proposals/outbox=%s/%d/%d", assignedStatus, proposalCount, outboxCount)
	}

	if err := runBrowserStep(browser, "driver mobile route",
		chromedp.Evaluate(`document.querySelector("header form[action='/logout']").requestSubmit()`, nil),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
		chromedp.SetValue("#username", "driver-task04", chromedp.ByQuery), chromedp.SetValue("#password", driverPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery),
		chromedp.EmulateViewport(390, 844), chromedp.Navigate(server.URL+"/my-route?date=2026-09-01"),
		chromedp.WaitVisible("[data-route-order-item]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}

	var orderBefore, orderAfter []string
	var mobileOverflow, smallMobileTarget bool
	var ownRouteContext, wakeLock, navigationLink, callLink, printButton bool
	var liveText string
	if err := runBrowserStep(browser, "reorder own route with buttons",
		chromedp.Evaluate(`Array.from(document.querySelectorAll("[data-route-order-item] input[name='stop_id']"), e=>e.value)`, &orderBefore),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-route-context][data-route-own="true"]'))`, &ownRouteContext),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-wake-lock]'))`, &wakeLock),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-route-navigation]'))`, &navigationLink),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-route-call]'))`, &callLink),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-print]'))`, &printButton),
		chromedp.Click("[data-route-order-item]:nth-child(2) [data-route-move='up']", chromedp.ByQuery),
		chromedp.Evaluate(`Array.from(document.querySelectorAll("[data-route-order-item] input[name='stop_id']"), e=>e.value)`, &orderAfter),
		chromedp.Text("[data-route-order-live]", &liveText, chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &mobileOverflow),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('[data-route-move],form[data-route-order] button[type=submit]')).some(e=>{const r=e.getBoundingClientRect();return r.height>0&&(r.height<44||r.width<44)})`, &smallMobileTarget),
		chromedp.Evaluate(`document.querySelector('form[data-route-order]').requestSubmit()`, nil),
		chromedp.Sleep(750*time.Millisecond),
		chromedp.Navigate(server.URL+"/my-route?date=2026-09-01"),
		chromedp.WaitVisible("[data-route-order-item]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if !ownRouteContext || !wakeLock || !navigationLink || !callLink || !printButton || len(orderBefore) != 2 || len(orderAfter) != 2 || orderAfter[0] != orderBefore[1] || liveText == "" || mobileOverflow || smallMobileTarget {
		t.Fatalf("mobile hooks/reorder before/after/live/overflow/small=%v/%v/%v/%v/%v/%v/%v/%q/%v/%v", ownRouteContext, wakeLock, navigationLink, callLink, printButton, orderBefore, orderAfter, liveText, mobileOverflow, smallMobileTarget)
	}

	var firstStopID string
	if err := pool.QueryRow(t.Context(), "SELECT id::text FROM route_stops WHERE route_draft_id=$1 ORDER BY position LIMIT 1", routeID).Scan(&firstStopID); err != nil {
		t.Fatal(err)
	}
	if firstStopID != orderBefore[1] {
		t.Fatalf("stored first stop=%s want %s", firstStopID, orderBefore[1])
	}
	for appointmentID, expected := range appointmentTimes {
		var startsAt, endsAt time.Time
		if err := pool.QueryRow(t.Context(), "SELECT starts_at,ends_at FROM appointments WHERE id=$1", appointmentID).Scan(&startsAt, &endsAt); err != nil {
			t.Fatal(err)
		}
		if !startsAt.Equal(expected[0]) || !endsAt.Equal(expected[1]) {
			t.Fatalf("driver reorder changed appointment %s times to %s-%s", appointmentID, startsAt, endsAt)
		}
	}
}
