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

type e2eStreetDirections struct{}

func (e2eStreetDirections) Directions(_ context.Context, points []planning.Point) (planning.RouteDirections, error) {
	result := planning.RouteDirections{Source: "osrm", FreshAt: time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC)}
	if len(points) > 0 {
		result.Geometry = append(result.Geometry, points[0])
	}
	for index := 0; index+1 < len(points); index++ {
		from, to := points[index], points[index+1]
		result.Geometry = append(result.Geometry,
			planning.Point{Latitude: (from.Latitude+to.Latitude)/2 + 0.002, Longitude: (from.Longitude + to.Longitude) / 2},
			to,
		)
		leg := planning.RouteLeg{DistanceMeters: 12_000 + index*1_000, Duration: time.Duration(12+index) * time.Minute}
		result.Legs = append(result.Legs, leg)
		result.DistanceMeters += leg.DistanceMeters
		result.Duration += leg.Duration
	}
	return result, nil
}

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
	routes, err := planning.NewRouteService(postgres.NewRouteStore(pool), routerAdapter, e2eStreetDirections{}, planning.DefaultRouteConfig())
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
		Geocoder: &task02Geocoder{},
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
	var routeLocationMapReady, routeLocationWorkerReady, routeLocationConfirmed, routeLocationInvalidated, routeLocationLayout, routeLocationSaved bool
	routeLocationContext, cancelRouteLocation := context.WithTimeout(browser, 45*time.Second)
	if err := chromedp.Run(routeLocationContext,
		chromedp.Navigate(server.URL+"/settings/route-locations"),
		chromedp.WaitVisible("[data-route-location-map]", chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-route-location-map]')?.dataset.mapSelectionEnabled==='true'&&document.querySelector('[data-route-location-map]')?.dataset.mapReady==='true'`, nil),
		chromedp.SetValue("[data-route-location-label]", "Kartenort", chromedp.ByQuery),
		chromedp.SetValue("[data-route-location-address]", "Geprüfte Adresse", chromedp.ByQuery),
		chromedp.Click("[data-route-location-map]", chromedp.ByQuery),
		chromedp.Poll(`Boolean(document.querySelector('[data-route-location-latitude]').value&&document.querySelector('[data-route-location-longitude]').value)`, nil),
		chromedp.Evaluate(`document.querySelector('[data-route-location-map]').dataset.mapSelectionEnabled==='true'`, &routeLocationMapReady),
		chromedp.Evaluate(`typeof window.maplibregl?.setWorkerUrl==='function'&&String(window.maplibregl?.getWorkerUrl?.()||'').startsWith(window.location.origin+'/assets/maplibre-gl-csp-worker.js?')`, &routeLocationWorkerReady),
		chromedp.Click("[data-route-location-confirm]", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-route-location-confirmed]').value==='true'`, &routeLocationConfirmed),
		chromedp.Evaluate(`(() => { const input=document.querySelector('[data-route-location-latitude]'); input.value=String(Number(input.value)+0.001); input.dispatchEvent(new Event('input',{bubbles:true})); return document.querySelector('[data-route-location-confirmed]').value===''; })()`, &routeLocationInvalidated),
		chromedp.Evaluate(`(() => { const checks=Array.from(document.querySelectorAll('.route-location-defaults__choices input[type="checkbox"]')); return document.documentElement.scrollWidth<=window.innerWidth&&checks.length===2&&checks.every(input=>{const box=input.getBoundingClientRect();const label=input.closest('label').getBoundingClientRect();return box.width<=24&&box.height<=24&&label.height>=44;}); })()`, &routeLocationLayout),
	); err != nil {
		cancelRouteLocation()
		t.Fatal(browserDiagnostics(browser, err))
	}
	cancelRouteLocation()
	if _, err := chromedp.RunResponse(browser, chromedp.Click("form[data-route-location-editor] button[type='submit']", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if err := runBrowserStep(browser, "read saved route location",
		chromedp.Poll(`Array.from(document.querySelectorAll('.route-location-card input[name="name"]')).some(input=>input.value==='Kartenort')`, nil),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('.route-location-card input[name="name"]')).some(input=>input.value==='Kartenort')`, &routeLocationSaved),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if !routeLocationMapReady || !routeLocationWorkerReady || !routeLocationConfirmed || !routeLocationInvalidated || !routeLocationLayout || !routeLocationSaved {
		t.Fatalf("route-location map/worker/confirm/invalidate/layout/save=%v/%v/%v/%v/%v/%v", routeLocationMapReady, routeLocationWorkerReady, routeLocationConfirmed, routeLocationInvalidated, routeLocationLayout, routeLocationSaved)
	}

	var nativeCustomRoute bool
	if err := runBrowserStep(browser, "plan custom endpoints without JavaScript",
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(true).Do(ctx) }),
		chromedp.Navigate(server.URL+"/planning/routes"),
		chromedp.WaitVisible("form[action='/planning/routes']", chromedp.ByQuery),
		chromedp.Click("input[name='start_selection'][value='custom']", chromedp.ByQuery),
		chromedp.Click("input[name='end_selection'][value='custom']", chromedp.ByQuery),
		chromedp.SetValue("input[name='start_custom_label']", "Hof ohne JavaScript", chromedp.ByQuery),
		chromedp.SetValue("input[name='start_custom_address']", "Hofstraße 1", chromedp.ByQuery),
		chromedp.SetValue("input[name='start_latitude']", "48.200000", chromedp.ByQuery),
		chromedp.SetValue("input[name='start_longitude']", "14.200000", chromedp.ByQuery),
		chromedp.Click("input[name='start_custom_confirmed_native']", chromedp.ByQuery),
		chromedp.SetValue("input[name='end_custom_label']", "Lager ohne JavaScript", chromedp.ByQuery),
		chromedp.SetValue("input[name='end_custom_address']", "Lagerweg 2", chromedp.ByQuery),
		chromedp.SetValue("input[name='end_latitude']", "48.260000", chromedp.ByQuery),
		chromedp.SetValue("input[name='end_longitude']", "14.260000", chromedp.ByQuery),
		chromedp.Click("input[name='end_custom_confirmed_native']", chromedp.ByQuery),
		chromedp.Click("input[name='job_id'][value='"+jobID+"']", chromedp.ByQuery),
		chromedp.SetValue("select[name='driver_id']", driverID, chromedp.ByQuery),
		chromedp.SetValue("select[name='chipper_resource_id']", chipperID, chromedp.ByQuery),
		chromedp.SetValue("input[name='departure_date']", "2026-09-01", chromedp.ByQuery),
		chromedp.SetValue("input[name='departure_time']", "06:30", chromedp.ByQuery),
		chromedp.Click("form[action='/planning/routes'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible(".route-summary", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('.route-summary')?.textContent.includes('Hof ohne JavaScript')&&document.querySelector('.route-summary')?.textContent.includes('Lager ohne JavaScript')`, &nativeCustomRoute),
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(false).Do(ctx) }),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if !nativeCustomRoute {
		t.Fatal("custom route endpoints are not usable without JavaScript")
	}

	var compactDesktopOverflow, compactMobileOverflow bool
	var waitlistLayout struct {
		SelectionWidth, NextStepWidth, ActionsWidth, MaxRowHeight                  float64
		CheckboxWidth, CheckboxHeight, SelectionTargetWidth, SelectionTargetHeight float64
		BreaksInsideWords                                                          bool
	}
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
		chromedp.Evaluate(`(() => {
			const table = document.querySelector('.waitlist-table');
			const cell = label => table.querySelector('tbody td[data-label="' + label + '"]');
			const rows = Array.from(table.tBodies[0]?.rows || []);
			const nextStep = cell('Nächster Schritt');
			const checkbox = table.querySelector('[data-waitlist-select]');
			const selectionTarget = checkbox.closest('.waitlist-select-control');
			const style = getComputedStyle(nextStep);
			return {
				SelectionWidth: cell('Auswahl').getBoundingClientRect().width,
				NextStepWidth: nextStep.getBoundingClientRect().width,
				ActionsWidth: cell('Aktionen').getBoundingClientRect().width,
				MaxRowHeight: Math.max(0, ...rows.map(row => row.getBoundingClientRect().height)),
				CheckboxWidth: checkbox.getBoundingClientRect().width,
				CheckboxHeight: checkbox.getBoundingClientRect().height,
				SelectionTargetWidth: selectionTarget.getBoundingClientRect().width,
				SelectionTargetHeight: selectionTarget.getBoundingClientRect().height,
				BreaksInsideWords: style.overflowWrap === 'anywhere' || style.wordBreak === 'break-all'
			};
		})()`, &waitlistLayout),
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
	if waitlistLayout.SelectionWidth > 90 || waitlistLayout.NextStepWidth < 180 || waitlistLayout.ActionsWidth < 100 || waitlistLayout.MaxRowHeight > 280 || waitlistLayout.CheckboxWidth > 20 || waitlistLayout.CheckboxHeight > 20 || waitlistLayout.SelectionTargetWidth < 44 || waitlistLayout.SelectionTargetHeight < 44 || waitlistLayout.BreaksInsideWords {
		t.Fatalf("waitlist desktop layout selection/next/actions/row/checkbox/target/breaks=%.0f/%.0f/%.0f/%.0f/%.0fx%.0f/%.0fx%.0f/%v", waitlistLayout.SelectionWidth, waitlistLayout.NextStepWidth, waitlistLayout.ActionsWidth, waitlistLayout.MaxRowHeight, waitlistLayout.CheckboxWidth, waitlistLayout.CheckboxHeight, waitlistLayout.SelectionTargetWidth, waitlistLayout.SelectionTargetHeight, waitlistLayout.BreaksInsideWords)
	}

	var desktopOverflow, smallDesktopTarget, selectedByMap, selectedByCard, selectedMarkerState, deselectedByKeyboard, candidateSemantics, mapReady bool
	var selectionFeedbackSpecific bool
	var routeGeometryPresent, routeStreetSource, routeLineDrawn, routeLineRendered, routeLineAnnounced bool
	var routeLineState, mapError string
	var markerPresentation struct {
		TouchTarget, CompactHead, Pointed bool
		Scale                             string
	}
	var desktopLayout struct {
		TwoColumns, MapAboveFold, CompactCandidate, BuilderWide, EndpointCardsReadable bool
	}
	var adminRouteContext, mapToolbar, routeDatePresets bool
	var routePresetAudit struct {
		Actual, Expected []string
	}
	var candidateMarkerCount, startMarkerCount int
	var smallDesktopTargets []string
	desktopRouteContext, cancelDesktopRoute := context.WithTimeout(browser, 60*time.Second)
	if err := chromedp.Run(desktopRouteContext,
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
			const marker=workspace.querySelector('.route-map-marker--candidate');
			const target=marker.getBoundingClientRect();
			const head=getComputedStyle(marker,'::before');
			const tip=getComputedStyle(marker,'::after');
			return {TouchTarget:target.width>=44&&target.height>=44,
				CompactHead:parseFloat(head.width)<=28&&parseFloat(head.height)<=28,
				Pointed:parseFloat(tip.borderTopWidth)>=8,
				Scale:workspace.dataset.routeMarkerScale||''};
		})()`, &markerPresentation),
		chromedp.Evaluate(`(() => {
			const workspace=document.querySelector('[data-route-context][data-route-admin="true"]');
			const builder=workspace.querySelector('.route-builder').getBoundingClientRect();
			const panel=workspace.querySelector('.route-map-panel').getBoundingClientRect();
			const checkbox=workspace.querySelector('.route-candidate__select > input[type="checkbox"]').getBoundingClientRect();
			const endpointCards=Array.from(workspace.querySelectorAll('.route-location-picker')).map(card=>card.getBoundingClientRect());
			return {TwoColumns:builder.right<panel.left&&Math.abs(builder.top-panel.top)<4,
				MapAboveFold:panel.top<window.innerHeight&&panel.height>300,
				CompactCandidate:checkbox.width<=24&&checkbox.height<=24,
				BuilderWide:builder.width>=560,
				EndpointCardsReadable:endpointCards.length===2&&endpointCards.every(card=>card.width>=250)};
		})()`, &desktopLayout),
		chromedp.Evaluate(`(() => { const rows=Array.from(document.querySelectorAll('[data-route-candidate]')); return rows.length>0&&rows.every(row=>!row.hasAttribute('tabindex')&&row.querySelector('.route-candidate__select')?.getBoundingClientRect().height>=44&&row.querySelector('.route-order-actions label')?.getBoundingClientRect().height>=44); })()`, &candidateSemantics),
	); err != nil {
		cancelDesktopRoute()
		t.Fatalf("desktop route map setup: %s", browserDiagnostics(browser, err))
	}
	cancelDesktopRoute()
	desktopRouteSelectionContext, cancelDesktopRouteSelection := context.WithTimeout(browser, 30*time.Second)
	if err := chromedp.Run(desktopRouteSelectionContext,
		chromedp.Click(".route-candidate[data-job-id='"+jobID+"'] .route-candidate__select span", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("input[name='job_id'][value='`+jobID+`']").checked`, &selectedByCard),
		chromedp.Evaluate(`(() => { const feedback=document.querySelector('[data-route-form-feedback]'); return !feedback.hidden&&feedback.textContent.includes('Fahrer auswählen')&&feedback.textContent.includes('Hackmaschine auswählen'); })()`, &selectionFeedbackSpecific),
		chromedp.Evaluate(`document.querySelector(".route-map-marker--candidate[data-job-id='`+jobID+`']").classList.contains('route-map-marker--selected')`, &selectedMarkerState),
		chromedp.Focus("input[name='job_id'][value='"+jobID+"']", chromedp.ByQuery),
		chromedp.KeyEvent(" "),
		chromedp.Evaluate(`!document.querySelector("input[name='job_id'][value='`+jobID+`']").checked`, &deselectedByKeyboard),
	); err != nil {
		cancelDesktopRouteSelection()
		t.Fatalf("desktop route card selection: %s", browserDiagnostics(browser, err))
	}
	cancelDesktopRouteSelection()
	desktopRouteMapSelectionContext, cancelDesktopRouteMapSelection := context.WithTimeout(browser, 30*time.Second)
	if err := chromedp.Run(desktopRouteMapSelectionContext,
		chromedp.Evaluate(`document.querySelector(".route-map-marker--candidate[data-job-id='`+jobID+`']").click()`, nil),
		chromedp.Poll(`Boolean(document.querySelector('.route-map-popup__action'))`, nil),
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
	); err != nil {
		cancelDesktopRouteMapSelection()
		t.Fatalf("desktop route map selection: %s", browserDiagnostics(browser, err))
	}
	cancelDesktopRouteMapSelection()
	var routeFormReady bool
	var routeFormState string
	if err := chromedp.Run(browser,
		chromedp.Evaluate(`document.querySelector('[data-route-form-feedback]')?.classList.contains('route-form-feedback--complete')`, &routeFormReady),
		chromedp.Evaluate(`JSON.stringify((()=>{const form=document.querySelector("form[action='/planning/routes']");return {feedback:form.querySelector('[data-route-form-feedback]')?.textContent||'',start:form.querySelector("input[name='start_selection']:checked")?.value||'',end:form.querySelector("input[name='end_selection']:checked")?.value||'',jobs:Array.from(form.querySelectorAll("input[name='job_id']:checked"),input=>input.value),driver:form.elements.driver_id.value,chipper:form.elements.chipper_resource_id.value,date:form.elements.departure_date.value,time:form.elements.departure_time.value}})())`, &routeFormState),
	); err != nil {
		t.Fatal(err)
	}
	if !routeFormReady {
		t.Fatalf("desktop route form not ready: %s", routeFormState)
	}
	desktopRouteResultContext, cancelDesktopRouteResult := context.WithTimeout(browser, 60*time.Second)
	if err := chromedp.Run(desktopRouteResultContext,
		chromedp.Click("form[action='/planning/routes'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible(".route-summary", chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('[data-route-map]')?.dataset.routeLineState==='drawn'`, nil),
		chromedp.Evaluate(`Boolean(document.querySelector('[data-route-map]')?.dataset.routeGeometry)`, &routeGeometryPresent),
		chromedp.Evaluate(`document.querySelector('[data-route-map]')?.dataset.routeSource==='osrm'`, &routeStreetSource),
		chromedp.Evaluate(`document.querySelector('[data-route-map]')?.dataset.routeLineState==='drawn'`, &routeLineDrawn),
		chromedp.Evaluate(`(() => { const canvas=document.querySelector('[data-route-line-overlay]'); if (!canvas) return false; const pixels=canvas.getContext('2d').getImageData(0,0,canvas.width,canvas.height).data; for(let index=3;index<pixels.length;index+=4){if(pixels[index]>0)return true} return false })()`, &routeLineRendered),
		chromedp.Evaluate(`document.querySelector('[data-route-map]')?.dataset.routeLineState||''`, &routeLineState),
		chromedp.Evaluate(`document.querySelector('[data-route-map]')?.dataset.mapError||''`, &mapError),
		chromedp.Evaluate(`document.querySelector('[data-route-map-notice]')?.textContent.includes('Straßenroute')`, &routeLineAnnounced),
	); err != nil {
		cancelDesktopRouteResult()
		t.Fatal(browserDiagnostics(browser, err))
	}
	cancelDesktopRouteResult()
	if !mapReady || !adminRouteContext || !mapToolbar || !routeDatePresets || strings.Join(routePresetAudit.Actual, ",") != strings.Join(routePresetAudit.Expected, ",") || candidateMarkerCount != 2 || startMarkerCount != 1 || !markerPresentation.TouchTarget || !markerPresentation.CompactHead || !markerPresentation.Pointed || markerPresentation.Scale == "" || !selectedByCard || !selectionFeedbackSpecific || !selectedMarkerState || !deselectedByKeyboard || !candidateSemantics || !selectedByMap || desktopOverflow || smallDesktopTarget || !desktopLayout.TwoColumns || !desktopLayout.MapAboveFold || !desktopLayout.CompactCandidate || !desktopLayout.BuilderWide || !desktopLayout.EndpointCardsReadable || !routeGeometryPresent || !routeStreetSource || !routeLineDrawn || !routeLineRendered || !routeLineAnnounced {
		t.Fatalf("desktop route map-ready/admin/toolbar/presets/candidates/start/marker/card/selected/keyboard/semantics/map/overflow/small-target/layout/geometry/street/line/rendered/notice=%v/%v/%v/%v/%d/%d/%+v/%v/%v/%v/%v/%v/%v/%v/%+v/%v/%v/%v/%v/%v state=%q map-error=%q targets=%v", mapReady, adminRouteContext, mapToolbar, routeDatePresets, candidateMarkerCount, startMarkerCount, markerPresentation, selectedByCard, selectedMarkerState, deselectedByKeyboard, candidateSemantics, selectedByMap, desktopOverflow, smallDesktopTarget, desktopLayout, routeGeometryPresent, routeStreetSource, routeLineDrawn, routeLineRendered, routeLineAnnounced, routeLineState, mapError, smallDesktopTargets)
	}

	var routeText string
	if err := chromedp.Run(browser, chromedp.Text("main", &routeText, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(routeText, "4 Routenpunkte") || !strings.Contains(routeText, "Straßenrouting") || !strings.Contains(routeText, "keine Nachricht") {
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
	if err := runBrowserStep(browser, "return to admin for route geocoding",
		chromedp.Evaluate(`document.querySelector("header form[action='/logout']").requestSubmit()`, nil),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
		chromedp.SetValue("#username", "admin-task04", chromedp.ByQuery),
		chromedp.SetValue("#password", adminPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	assertRouteGeocodingJourney(t, browser, server.URL)
	assertCommandPaletteJourney(t, browser)
}

func assertCommandPaletteJourney(t *testing.T, browser context.Context) {
	t.Helper()
	var commandResults int
	var commandPrivate, commandFocusRestored bool
	if err := runBrowserStep(browser, "command palette search",
		chromedp.EmulateViewport(1440, 900),
		chromedp.Focus("[data-command-open]", chromedp.ByQuery),
		chromedp.Evaluate(`document.dispatchEvent(new KeyboardEvent('keydown',{key:'k',ctrlKey:true,bubbles:true}))`, nil),
		chromedp.WaitVisible("[data-command-palette]", chromedp.ByQuery),
		chromedp.Poll(`document.activeElement===document.querySelector('[data-global-search-input]')`, nil),
		chromedp.SetValue("[data-global-search-input]", "Franz", chromedp.ByQuery),
		chromedp.Click("[data-global-search-form] button[type='submit']", chromedp.ByQuery),
		chromedp.Poll(`document.querySelectorAll('[data-global-search-results] .search-result').length>0`, nil),
		chromedp.Evaluate(`document.querySelectorAll('[data-global-search-results] .search-result').length`, &commandResults),
		chromedp.Evaluate(`!location.href.includes('Franz')&&![...Object.values(localStorage),...Object.values(sessionStorage)].some(value=>String(value).includes('Franz'))`, &commandPrivate),
		chromedp.Click("[data-command-close]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-command-palette]", chromedp.ByQuery),
		chromedp.Evaluate(`document.activeElement===document.querySelector('[data-command-open]')`, &commandFocusRestored),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if commandResults == 0 || !commandPrivate || !commandFocusRestored {
		t.Fatalf("command results/private/focus=%d/%v/%v", commandResults, commandPrivate, commandFocusRestored)
	}
	var shortcutsVisible, shortcutsFocusRestored bool
	if err := runBrowserStep(browser, "shortcut help",
		chromedp.Click("[data-command-open]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-command-palette]", chromedp.ByQuery),
		chromedp.Click("[data-shortcuts-open]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-shortcuts-dialog]", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-shortcuts-dialog]').open`, &shortcutsVisible),
		chromedp.Click("[data-shortcuts-close]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-shortcuts-dialog]", chromedp.ByQuery),
		chromedp.Evaluate(`document.activeElement===document.querySelector('[data-command-open]')`, &shortcutsFocusRestored),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if !shortcutsVisible || !shortcutsFocusRestored {
		t.Fatalf("shortcuts/focus=%v/%v", shortcutsVisible, shortcutsFocusRestored)
	}
}

func assertRouteGeocodingJourney(t *testing.T, browser context.Context, serverURL string) {
	t.Helper()
	if err := runBrowserStep(browser, "open custom route location",
		chromedp.Navigate(serverURL+"/planning/routes"),
		chromedp.WaitVisible("form[action='/planning/routes']", chromedp.ByQuery),
		chromedp.Poll(`document.documentElement.dataset.routeLocationsReady==='true'`, nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	var geocodingStatus string
	var geocodingResultCount int
	if err := runBrowserStep(browser, "search custom route location",
		chromedp.Evaluate(`document.querySelector("input[name='start_selection'][value='custom']").click()`, nil),
		chromedp.SetValue("#start-route-location-search", "Waldstraße 9", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("[data-route-location-prefix='start'] [data-route-location-search-submit]").click()`, nil),
		chromedp.Sleep(750*time.Millisecond),
		chromedp.Text("[data-route-location-prefix='start'] [data-route-location-search-status]", &geocodingStatus, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('[data-route-location-prefix="start"] .location-search__result').length`, &geocodingResultCount),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if geocodingResultCount != 1 {
		t.Fatalf("route geocoding results=%d status=%q", geocodingResultCount, geocodingStatus)
	}
	var geocodingDraftVisible, geocodingNeedsLabel, customLocationConfirmed bool
	if err := runBrowserStep(browser, "select searched custom route location",
		chromedp.Click("[data-route-location-prefix='start'] button.location-search__result", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const picker=document.querySelector('[data-route-location-prefix="start"]');
			return !picker.querySelector('[data-route-location-custom]').hidden
				&&!picker.querySelector('[data-route-location-search-results]').hidden
				&&Boolean(picker.querySelector('[data-route-location-selected-result]'))
				&&picker.querySelector('[data-route-location-confirmed]').value==='';
		})()`, &geocodingDraftVisible),
		chromedp.Evaluate(`document.querySelector('[data-route-location-prefix="start"] [data-route-location-error]')?.textContent.includes('Bezeichnung')`, &geocodingNeedsLabel),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if err := runBrowserStep(browser, "confirm searched custom route location",
		chromedp.SetValue("input[name='start_custom_label']", "Waldlager", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("[data-route-location-prefix='start'] [data-route-location-confirm]").click()`, nil),
		chromedp.Evaluate(`(() => {
			const picker=document.querySelector('[data-route-location-prefix="start"]');
			return picker.querySelector('[data-route-location-confirmed]').value==='true'
				&&!picker.querySelector('[data-route-location-custom]').hidden
				&&picker.querySelector('[data-route-location-selected-state]')?.textContent.includes('übernommen');
		})()`, &customLocationConfirmed),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if geocodingDraftVisible && geocodingNeedsLabel && customLocationConfirmed {
		return
	}
	var state string
	_ = chromedp.Run(browser, chromedp.Evaluate(`JSON.stringify((()=>{const picker=document.querySelector('[data-route-location-prefix="start"]');return {result:picker.querySelector('.location-search__result')?.outerHTML||'',address:picker.querySelector('[data-route-location-address]')?.value||'',latitude:picker.querySelector('[data-route-location-latitude]')?.value||'',confirmed:picker.querySelector('[data-route-location-confirmed]')?.value||'',error:picker.querySelector('[data-route-location-error]')?.textContent||'',status:picker.querySelector('[data-route-location-search-status]')?.textContent||''}})())`, &state))
	t.Fatalf("route geocoding draft/label/confirmed=%v/%v/%v state=%s", geocodingDraftVisible, geocodingNeedsLabel, customLocationConfirmed, state)
}
