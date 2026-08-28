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
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

func TestTask12RoutePlannerDesktopAndDriverMobileJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, driverID, chipperID, jobID, secondJobID, adminPassword, driverPassword := task04Application(t, databaseURL)
	routeLocations, _ := e2eRouteLocations(t, pool)
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
		Planning: config.Planning{BusinessOpen: "07:00"},
		Map:      config.Map{Attribution: "OpenStreetMap-Mitwirkende"},
	}
	handler, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"},
		Identity: identity, Customers: customerService, Drivers: drivers, Resources: resources, Appointments: appointments, Dashboard: e2eDashboard(t, pool), Routes: routes, RouteLocations: routeLocations,
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
	browser, cancelTimeout := context.WithTimeout(browser, 300*time.Second)
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

	var routeLocationMapReady, routeLocationConfirmed, routeLocationInvalidated, routeLocationLayout bool
	if err := runBrowserStep(browser, "select route location on map",
		chromedp.Navigate(server.URL+"/settings/route-locations"),
		chromedp.WaitVisible("[data-route-location-map]", chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-route-location-map]')?.dataset.mapSelectionEnabled==='true'&&document.querySelector('[data-route-location-map]')?.dataset.mapReady==='true'`, nil),
		chromedp.SetValue("[data-route-location-label]", "Kartenort", chromedp.ByQuery),
		chromedp.SetValue("[data-route-location-address]", "Geprüfte Adresse", chromedp.ByQuery),
		chromedp.Click("[data-route-location-map]", chromedp.ByQuery),
		chromedp.Poll(`Boolean(document.querySelector('[data-route-location-latitude]').value&&document.querySelector('[data-route-location-longitude]').value)`, nil),
		chromedp.Evaluate(`document.querySelector('[data-route-location-map]').dataset.mapSelectionEnabled==='true'`, &routeLocationMapReady),
		chromedp.Click("[data-route-location-confirm]", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-route-location-confirmed]').value==='true'`, &routeLocationConfirmed),
		chromedp.Evaluate(`(() => { const input=document.querySelector('[data-route-location-latitude]'); input.value=String(Number(input.value)+0.001); input.dispatchEvent(new Event('input',{bubbles:true})); return document.querySelector('[data-route-location-confirmed]').value===''; })()`, &routeLocationInvalidated),
		chromedp.Evaluate(`(() => { const checks=Array.from(document.querySelectorAll('.route-location-defaults__choices input[type="checkbox"]')); return document.documentElement.scrollWidth<=window.innerWidth&&checks.length===2&&checks.every(input=>{const box=input.getBoundingClientRect();const label=input.closest('label').getBoundingClientRect();return box.width<=24&&box.height<=24&&label.height>=44;}); })()`, &routeLocationLayout),
		chromedp.Evaluate(`document.querySelector('form[data-route-location-editor]').reset()`, nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if !routeLocationMapReady || !routeLocationConfirmed || !routeLocationInvalidated || !routeLocationLayout {
		t.Fatalf("route-location map/confirm/invalidate/layout=%v/%v/%v/%v", routeLocationMapReady, routeLocationConfirmed, routeLocationInvalidated, routeLocationLayout)
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
	var routeGeometryPresent, routeLineDrawn, routeLineRendered, routeLineAnnounced bool
	var routeLineState, mapError string
	var desktopLayout struct {
		TwoColumns, MapAboveFold, CompactCandidate, BuilderWide, EndpointCardsReadable bool
	}
	var adminRouteContext, mapToolbar, routeDatePresets bool
	var routePresetAudit struct {
		Actual, Expected []string
	}
	var candidateMarkerCount, startMarkerCount int
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
		chromedp.Evaluate(`document.querySelectorAll('.route-map-marker--start').length`, &startMarkerCount),
		chromedp.Evaluate(`(() => {
			const workspace=document.querySelector('[data-route-context][data-route-admin="true"]');
			const builder=workspace.querySelector('.route-builder').getBoundingClientRect();
			const panel=workspace.querySelector('.route-map-panel').getBoundingClientRect();
			const checkbox=workspace.querySelector('.route-candidate > input[type="checkbox"]').getBoundingClientRect();
			const endpointCards=Array.from(workspace.querySelectorAll('.route-location-picker')).map(card=>card.getBoundingClientRect());
			return {TwoColumns:builder.right<panel.left&&Math.abs(builder.top-panel.top)<4,
				MapAboveFold:panel.top<window.innerHeight&&panel.height>300,
				CompactCandidate:checkbox.width<=24&&checkbox.height<=24,
				BuilderWide:builder.width>=560,
				EndpointCardsReadable:endpointCards.length===2&&endpointCards.every(card=>card.width>=250)};
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
		chromedp.Poll(`document.querySelector('[data-route-map]')?.dataset.routeLineState==='drawn'`, nil),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-route-map]')?.dataset.routeGeometry)`, &routeGeometryPresent),
		chromedp.Evaluate(`document.querySelector('[data-route-map]')?.dataset.routeLineState==='drawn'`, &routeLineDrawn),
		chromedp.Evaluate(`(() => { const canvas=document.querySelector('[data-route-line-overlay]'); if (!canvas) return false; const pixels=canvas.getContext('2d').getImageData(0,0,canvas.width,canvas.height).data; for(let index=3;index<pixels.length;index+=4){if(pixels[index]>0)return true} return false })()`, &routeLineRendered),
		chromedp.Evaluate(`document.querySelector('[data-route-map]')?.dataset.routeLineState||''`, &routeLineState),
		chromedp.Evaluate(`document.querySelector('[data-route-map]')?.dataset.mapError||''`, &mapError),
		chromedp.Evaluate(`document.querySelector('[data-route-map-notice]')?.textContent.includes('Routenlinie')`, &routeLineAnnounced),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if !mapReady || !adminRouteContext || !mapToolbar || !routeDatePresets || strings.Join(routePresetAudit.Actual, ",") != strings.Join(routePresetAudit.Expected, ",") || candidateMarkerCount != 2 || startMarkerCount != 1 || !selectedByMap || desktopOverflow || smallDesktopTarget || !desktopLayout.TwoColumns || !desktopLayout.MapAboveFold || !desktopLayout.CompactCandidate || !routeGeometryPresent || !routeLineDrawn || !routeLineRendered || !routeLineAnnounced {
		t.Fatalf("desktop route map-ready/admin/toolbar/presets/candidates/start/selected/overflow/small-target/layout/geometry/line/rendered/notice=%v/%v/%v/%v/%d/%d/%v/%v/%v/%+v/%v/%v/%v/%v state=%q map-error=%q targets=%v", mapReady, adminRouteContext, mapToolbar, routeDatePresets, candidateMarkerCount, startMarkerCount, selectedByMap, desktopOverflow, smallDesktopTarget, desktopLayout, routeGeometryPresent, routeLineDrawn, routeLineRendered, routeLineAnnounced, routeLineState, mapError, smallDesktopTargets)
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

	var initialOrder, enhancedDOMOrder, enhancedStoredOrder []string
	var mobileOverflow, smallMobileTarget bool
	var ownRouteContext, wakeLock, navigationLink, callLink, printButton bool
	var routeOrderEnhanced, reorderFocused bool
	var liveText string
	if err := runBrowserStep(browser, "reorder own route with buttons",
		chromedp.Poll(`(() => {
			const buttons=Array.from(document.querySelectorAll("form[data-route-order] [data-route-move]"));
			const stops=document.querySelectorAll("form[data-route-order] [data-route-order-item]");
			return stops.length===2&&buttons.length===stops.length*2&&buttons.every(button=>button.type==="button")&&Boolean(document.querySelector("[data-route-order-live]"));
		})()`, nil),
		chromedp.Evaluate(`Array.from(document.querySelectorAll("form[data-route-order] [data-route-move]")).every(button=>button.type==="button")`, &routeOrderEnhanced),
		chromedp.Evaluate(`Array.from(document.querySelectorAll("[data-route-order-item] input[name='stop_id']"), e=>e.value)`, &initialOrder),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-route-context][data-route-own="true"]'))`, &ownRouteContext),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-wake-lock]'))`, &wakeLock),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-route-navigation]'))`, &navigationLink),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-route-call]'))`, &callLink),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-print]'))`, &printButton),
		chromedp.Click("[data-route-order-item]:nth-child(2) [data-route-move='up']", chromedp.ByQuery),
		chromedp.Poll(`(() => {
			const moved=document.querySelector("[data-route-order-item]:first-child");
			const focusTarget=moved?.querySelector("[data-route-move='down']");
			return Boolean(document.querySelector("[data-route-order-live]")?.textContent.trim())&&document.activeElement===focusTarget;
		})()`, nil),
		chromedp.Evaluate(`Array.from(document.querySelectorAll("[data-route-order-item] input[name='stop_id']"), e=>e.value)`, &enhancedDOMOrder),
		chromedp.Text("[data-route-order-live]", &liveText, chromedp.ByQuery),
		chromedp.Evaluate(`document.activeElement===document.querySelector("[data-route-order-item]:first-child [data-route-move='down']")`, &reorderFocused),
		chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &mobileOverflow),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('[data-route-move],form[data-route-order] button[type=submit]')).some(e=>{const r=e.getBoundingClientRect();return r.height>0&&(r.height<44||r.width<44)})`, &smallMobileTarget),
		chromedp.Evaluate(`document.querySelector('form[data-route-order]').requestSubmit()`, nil),
		chromedp.Sleep(750*time.Millisecond),
		chromedp.Navigate(server.URL+"/my-route?date=2026-09-01"),
		chromedp.WaitVisible("[data-route-order-item]", chromedp.ByQuery),
		chromedp.Evaluate(`Array.from(document.querySelectorAll("[data-route-order-item] input[name='stop_id']"), e=>e.value)`, &enhancedStoredOrder),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	enhancedPersisted := len(initialOrder) == 2 && len(enhancedDOMOrder) == 2 && len(enhancedStoredOrder) == 2 &&
		enhancedDOMOrder[0] == initialOrder[1] && enhancedDOMOrder[1] == initialOrder[0] &&
		enhancedStoredOrder[0] == enhancedDOMOrder[0] && enhancedStoredOrder[1] == enhancedDOMOrder[1]
	if !routeOrderEnhanced || !reorderFocused || !ownRouteContext || !wakeLock || !navigationLink || !callLink || !printButton || !enhancedPersisted || liveText == "" || mobileOverflow || smallMobileTarget {
		t.Fatalf("mobile enhanced/focused/hooks/persisted/orders initial/dom/stored/live/overflow/small=%v/%v/%v/%v/%v/%v/%v/%v/%v/%v/%v/%q/%v/%v", routeOrderEnhanced, reorderFocused, ownRouteContext, wakeLock, navigationLink, callLink, printButton, enhancedPersisted, initialOrder, enhancedDOMOrder, enhancedStoredOrder, liveText, mobileOverflow, smallMobileTarget)
	}
	var nativeStartOrder, nativeStoredOrder []string
	if err := runBrowserStep(browser, "reorder own route without JavaScript",
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(true).Do(ctx) }),
		chromedp.Reload(),
		chromedp.WaitVisible("[data-route-order-item]:first-child button[name='move_down']", chromedp.ByQuery),
		chromedp.Evaluate(`Array.from(document.querySelectorAll("[data-route-order-item] input[name='stop_id']"), e=>e.value)`, &nativeStartOrder),
		chromedp.Evaluate(`document.documentElement.dataset.e2eNavigationMarker='pending'`, nil),
		chromedp.Focus("[data-route-order-item]:first-child button[name='move_down']", chromedp.ByQuery),
		chromedp.KeyEvent("\r"),
		chromedp.WaitNotPresent("html[data-e2e-navigation-marker='pending']", chromedp.ByQuery),
		chromedp.WaitVisible("[data-route-order-item]", chromedp.ByQuery),
		chromedp.Evaluate(`Array.from(document.querySelectorAll("[data-route-order-item] input[name='stop_id']"), e=>e.value)`, &nativeStoredOrder),
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(false).Do(ctx) }),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	nativePersisted := len(nativeStartOrder) == 2 && len(nativeStoredOrder) == 2 && len(enhancedStoredOrder) == 2 &&
		nativeStartOrder[0] == enhancedStoredOrder[0] && nativeStartOrder[1] == enhancedStoredOrder[1] &&
		nativeStoredOrder[0] == nativeStartOrder[1] && nativeStoredOrder[1] == nativeStartOrder[0]
	if !nativePersisted {
		t.Fatalf("no-JavaScript reorder persisted=%v enhanced/start/stored=%v/%v/%v", nativePersisted, enhancedStoredOrder, nativeStartOrder, nativeStoredOrder)
	}

	rows, err = pool.Query(t.Context(), "SELECT id::text FROM route_stops WHERE route_draft_id=$1 ORDER BY position", routeID)
	if err != nil {
		t.Fatal(err)
	}
	var databaseOrder []string
	for rows.Next() {
		var stopID string
		if err = rows.Scan(&stopID); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		databaseOrder = append(databaseOrder, stopID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(databaseOrder, "|") != strings.Join(nativeStoredOrder, "|") {
		t.Fatalf("database/native route order=%v/%v", databaseOrder, nativeStoredOrder)
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
