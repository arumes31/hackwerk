//go:build e2e

package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/dashboard"
	"example.invalid/hackplan/internal/geocode"
	"example.invalid/hackplan/internal/web"
	"example.invalid/hackplan/web/assets"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTask02BrowserJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	ctx := t.Context()
	pool, identity, customerService, driverPassword, adminPassword := task02Application(t, ctx, databaseURL)

	cfg := config.Config{
		AppName: "HackWerk", BaseURL: "http://127.0.0.1",
		Database: config.Database{ReadinessTimeout: 2 * time.Second},
		Auth: config.Auth{
			SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf",
			SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour,
		},
		Geocoding: config.Geocoding{RateLimit: 30},
	}
	geocoder := &task02Geocoder{}
	router, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool,
		Build: buildinfo.Info{Version: "e2e"}, Identity: identity, Customers: customerService, Dashboard: e2eDashboard(t, pool), Geocoder: geocoder,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	browserPath := browserExecutable(t)
	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browserPath), chromedp.Headless, chromedp.DisableGPU,
		chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck,
		chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(1280, 900),
	)
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 180*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browserContext) })

	var exceptionLock sync.Mutex
	exceptions := make([]string, 0)
	chromedp.ListenTarget(browserContext, func(event any) {
		if exception, ok := event.(*cdpruntime.EventExceptionThrown); ok {
			exceptionLock.Lock()
			exceptions = append(exceptions, exception.ExceptionDetails.Text)
			exceptionLock.Unlock()
		}
	})

	var rootLocation string
	var transportInitiallyHidden bool
	var nextWeekPreset struct {
		Today string `json:"today"`
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if err := chromedp.Run(browserContext,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
		chromedp.Location(&rootLocation),
	); err != nil {
		t.Fatalf("open application root: %v", err)
	}
	if !strings.HasSuffix(rootLocation, "/login") {
		t.Fatalf("application root location = %q, want /login", rootLocation)
	}
	if err := chromedp.Run(browserContext,
		chromedp.SetValue("#username", "driver-e2e", chromedp.ByQuery),
		chromedp.SetValue("#password", driverPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("driver login: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "open intake",
		chromedp.WaitVisible("a[href='/customers']", chromedp.ByQuery),
		chromedp.Navigate(server.URL+"/customers/new"),
		chromedp.WaitVisible("[data-new-customer-panel] summary", chromedp.ByQuery),
		chromedp.Click("[data-new-customer-panel] summary", chromedp.ByQuery),
		chromedp.WaitVisible("form[action='/customers']", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-transport-field]').hidden`, &transportInitiallyHidden),
		chromedp.Evaluate(`(() => {
			const parts = new Intl.DateTimeFormat('sv-SE', {timeZone:'Europe/Vienna',year:'numeric',month:'2-digit',day:'2-digit'})
				.formatToParts(new Date()).reduce((values, part) => ({...values, [part.type]:part.value}), {});
			document.querySelector('[data-date-range-preset][data-start-offset="7"]').click();
			return {
				today: parts.year+'-'+parts.month+'-'+parts.day,
				start: document.querySelector('[name="preferred_start"]').value,
				end: document.querySelector('[name="preferred_end"]').value,
			};
		})()`, &nextWeekPreset),
	); err != nil {
		t.Fatalf("open customer intake: %s", browserDiagnostics(browserContext, err))
	}
	today, err := time.Parse(time.DateOnly, nextWeekPreset.Today)
	if err != nil {
		t.Fatalf("parse browser date %q: %v", nextWeekPreset.Today, err)
	}
	start, startErr := time.Parse(time.DateOnly, nextWeekPreset.Start)
	end, endErr := time.Parse(time.DateOnly, nextWeekPreset.End)
	if startErr != nil || endErr != nil || start.Sub(today) != 7*24*time.Hour || end.Sub(today) != 14*24*time.Hour {
		t.Fatalf("next-week preset = %+v, parse errors = %v/%v", nextWeekPreset, startErr, endErr)
	}
	var locationFieldsUnchanged bool
	if err := runBrowserStep(browserContext, "search address without taking location",
		chromedp.Evaluate(`document.querySelector('.job-location-disclosure').open=true`, nil),
		chromedp.WaitVisible("[data-location-search-input]", chromedp.ByQuery),
		chromedp.Poll(`Boolean(document.querySelector('[data-job-location-editor]')?.dataset.mapInitialized)`, nil, chromedp.WithPollingTimeout(20*time.Second)),
		chromedp.SetValue("[data-location-search-input]", "Waldstraße 9, Unterneukirchen", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-location-search-submit]').click()`, nil),
		chromedp.WaitVisible("[data-location-search-results] .location-search__result", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-location-search-results] .location-search__result').click()`, nil),
		chromedp.Poll(`document.querySelector('[data-location-search-status]').textContent.includes('Karte auf')`, nil, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Evaluate(`(() => {
			const editor = document.querySelector('[data-job-location-editor]');
			return ['[data-location-latitude]','[data-location-longitude]','[data-location-committed-latitude]','[data-location-committed-longitude]']
				.every((selector) => editor.querySelector(selector).value === '');
		})()`, &locationFieldsUnchanged),
	); err != nil {
		t.Fatalf("search address: %s", browserDiagnostics(browserContext, err))
	}
	if !locationFieldsUnchanged || geocoder.lastQuery() != "Waldstraße 9, Unterneukirchen" {
		t.Fatalf("address search changed location=%v, query=%q", !locationFieldsUnchanged, geocoder.lastQuery())
	}
	var locationCommitted bool
	if err := runBrowserStep(browserContext, "commit pile coordinates",
		chromedp.SetValue("[data-job-location-editor] [data-location-latitude]", "48.216667", chromedp.ByQuery),
		chromedp.SetValue("[data-job-location-editor] [data-location-longitude]", "13.900000", chromedp.ByQuery),
		chromedp.Click("[data-job-location-editor] [data-location-commit]", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const editor = document.querySelector('[data-job-location-editor]');
			return editor.querySelector('[data-location-committed-latitude]').value === '48.216667'
				&& editor.querySelector('[data-location-committed-longitude]').value === '13.900000'
				&& editor.querySelector('[data-location-committed-source]').value === 'coordinates';
		})()`, &locationCommitted),
	); err != nil {
		t.Fatalf("commit pile coordinates: %s", browserDiagnostics(browserContext, err))
	}
	if !locationCommitted {
		t.Fatal("pile coordinates were not committed through the location editor")
	}
	var locationSearchOverflow, locationSearchTargetTooSmall bool
	var locationOverflowElements string
	if err := runBrowserStep(browserContext, "inspect mobile address search",
		chromedp.EmulateViewport(360, 800),
		chromedp.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth + 1`, &locationSearchOverflow),
		chromedp.Evaluate(`Array.from(document.querySelectorAll('body *')).map((element) => {
			const box=element.getBoundingClientRect();
			return {element, box};
		}).filter(({box}) => box.right > window.innerWidth + 1 || box.left < -1)
			.sort((a,b) => b.box.right-a.box.right).slice(0,5)
			.map(({element,box}) => {
				const parent=element.parentElement;
				const parentBox=parent?.getBoundingClientRect();
				return element.tagName+'.'+(element.className || '')+' right='+box.right.toFixed(0)+' width='+box.width.toFixed(0)
					+' parent='+parent?.tagName+'.'+(parent?.className || '')+' parentWidth='+(parentBox?.width || 0).toFixed(0);
			}).join('; ')+'; viewport='+window.innerWidth+' mobile='+matchMedia('(max-width: 760px)').matches`, &locationOverflowElements),
		chromedp.Evaluate(`(() => { const box=document.querySelector('[data-location-search-submit]').getBoundingClientRect(); return box.width < 44 || box.height < 44; })()`, &locationSearchTargetTooSmall),
		chromedp.EmulateViewport(1280, 900),
	); err != nil {
		t.Fatalf("inspect mobile address search: %s", browserDiagnostics(browserContext, err))
	}
	if locationSearchOverflow || locationSearchTargetTooSmall {
		t.Fatalf("mobile address search overflow=%v, touch target too small=%v, elements=%s", locationSearchOverflow, locationSearchTargetTooSmall, locationOverflowElements)
	}
	var invalidSummaryFocused, invalidFieldsAssociated, invalidValuesRetained bool
	if err := runBrowserStep(browserContext, "validate intake errors",
		chromedp.SetValue("[name='volume_m3']", "0", chromedp.ByQuery),
		chromedp.SetValue("[name='hack_duration']", "ungueltig", chromedp.ByQuery),
		chromedp.Click("form[action='/customers'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("[data-error-summary]", chromedp.ByQuery),
		chromedp.Poll(`document.activeElement === document.querySelector('[data-error-summary]')`, nil, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Evaluate(`document.activeElement === document.querySelector('[data-error-summary]')`, &invalidSummaryFocused),
		chromedp.Evaluate(`document.querySelector('#volume_m3').getAttribute('aria-invalid') === 'true' && document.querySelector('#volume_m3').getAttribute('aria-describedby') === 'volume_m3-error'`, &invalidFieldsAssociated),
		chromedp.Evaluate(`document.querySelector('#volume_m3').value === '0' && document.querySelector('#hack_duration').value === 'ungueltig'`, &invalidValuesRetained),
	); err != nil {
		t.Fatalf("validate intake errors: %s", browserDiagnostics(browserContext, err))
	}
	if !invalidSummaryFocused || !invalidFieldsAssociated || !invalidValuesRetained {
		t.Fatalf("invalid form accessibility: focused=%v associated=%v retained=%v", invalidSummaryFocused, invalidFieldsAssociated, invalidValuesRetained)
	}
	if err := runBrowserStep(browserContext, "fill customer",
		chromedp.SetValue("[name='first_name']", "Franz", chromedp.ByQuery),
		chromedp.SetValue("[name='last_name']", "Huber", chromedp.ByQuery),
		chromedp.SetValue("[name='street']", "Unterneukirchen 15", chromedp.ByQuery),
		chromedp.SetValue("[name='postal_code']", "8458", chromedp.ByQuery),
		chromedp.SetValue("[name='locality']", "Unterneukirchen", chromedp.ByQuery),
		chromedp.SetValue("[name='region']", "Unterneukirchen", chromedp.ByQuery),
		chromedp.SetValue("[name='phone']", "0664 1234567", chromedp.ByQuery),
		chromedp.SetValue("[name='email']", "franz.huber@example.test", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("fill customer fields: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "fill job",
		chromedp.SetValue("[name='volume_m3']", "80", chromedp.ByQuery),
		chromedp.SetValue("[name='hack_duration']", "3:00", chromedp.ByQuery),
		chromedp.SetValue("[name='preference_text']", "Anfang September", chromedp.ByQuery),
		chromedp.SetValue("[name='note']", "Hackplatz gut erreichbar", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("fill customer intake: %s", browserDiagnostics(browserContext, err))
	}
	if err := chromedp.Run(browserContext,
		chromedp.Click("form[action='/customers'] button[type='submit']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("submit customer intake: %s", browserDiagnostics(browserContext, err))
	}
	if err := chromedp.Run(browserContext,
		chromedp.WaitVisible("details.compact-job-row", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("customer detail after intake: %s", browserDiagnostics(browserContext, err))
	}
	var compactDossierDesktop, compactDossierMobile bool
	if err := runBrowserStep(browserContext, "inspect compact customer dossier",
		chromedp.Evaluate(`(() => {
			const overview=document.querySelector('.customer-overview-grid');
			const edit=document.querySelector('.customer-edit-card');
			edit.open=true;
			const identity=document.querySelector('.customer-identity-grid');
			const form=document.querySelector('.customer-record-form');
			const visibleTargets=Array.from(form.querySelectorAll('input,select,button,a.button,summary')).filter((element)=>element.getClientRects().length);
			return getComputedStyle(overview).alignItems==='start'
				&& getComputedStyle(identity).gridTemplateColumns.trim().split(/\s+/).length>=3
				&& form.getBoundingClientRect().height<500
				&& !document.querySelector('.customer-address-extra').open
				&& visibleTargets.every((element)=>element.getBoundingClientRect().height>=43.5);
		})()`, &compactDossierDesktop),
		chromedp.EmulateViewport(360, 800),
		chromedp.Evaluate(`(() => {
			const identity=document.querySelector('.customer-identity-grid');
			const targets=Array.from(document.querySelectorAll('.customer-record-form input,.customer-record-form select,.customer-record-form button,.customer-record-form a.button,.customer-record-form summary')).filter((element)=>element.getClientRects().length);
			return document.documentElement.scrollWidth<=document.documentElement.clientWidth+1
				&& getComputedStyle(identity).gridTemplateColumns.trim().split(/\s+/).length===1
				&& targets.every((element)=>element.getBoundingClientRect().height>=43.5);
		})()`, &compactDossierMobile),
		chromedp.EmulateViewport(1280, 900),
		chromedp.Evaluate(`document.querySelector('.customer-edit-card').open=false`, nil),
	); err != nil {
		t.Fatalf("inspect compact customer dossier: %s", browserDiagnostics(browserContext, err))
	}
	if !compactDossierDesktop || !compactDossierMobile {
		t.Fatalf("compact customer dossier desktop/mobile = %v/%v", compactDossierDesktop, compactDossierMobile)
	}
	if !transportInitiallyHidden {
		t.Fatal("transport fields are visible for a chipping-only intake")
	}

	var customerID, firstJobID, firstWaitlistID string
	if err := pool.QueryRow(ctx, `SELECT c.id::text, j.id::text, w.id::text
		FROM customers c JOIN jobs j ON j.customer_id = c.id
		JOIN waitlist_entries w ON w.job_id = j.id WHERE c.last_name = 'Huber'`).Scan(&customerID, &firstJobID, &firstWaitlistID); err != nil {
		t.Fatal(err)
	}
	var jobEditFormIntact bool
	jobEditFormSelector := "#job-" + firstJobID + " form[action='/jobs/" + firstJobID + "']"
	if err := runBrowserStep(browserContext, "job edit form remains intact after enhancement",
		chromedp.Evaluate(fmt.Sprintf(`document.querySelector(%q).open=true`, "#job-"+firstJobID), nil),
		chromedp.Poll(fmt.Sprintf(`Boolean(document.querySelector(%q)?.querySelector('[name="volume_m3"]'))`, jobEditFormSelector), nil, chromedp.WithPollingTimeout(5*time.Second)),
		chromedp.Evaluate(fmt.Sprintf(`Boolean(document.querySelector(%q)?.querySelector('[data-job-location-editor]'))`, jobEditFormSelector), &jobEditFormIntact),
	); err != nil {
		t.Fatalf("inspect job edit form: %s", browserDiagnostics(browserContext, err))
	}
	if !jobEditFormIntact {
		t.Fatal("job edit form lost its location editor during enhancement")
	}
	var mapsURL string
	var transportVisible, externalConfirmationVisible, compactJobFormMobile bool
	var customerAddressDrafted, customerAddressControlMobile bool
	var pickerHorizontalOverflow, pickerTouchTargetTooSmall bool
	if err := chromedp.Run(browserContext,
		chromedp.AttributeValue("a[href^='https://www.google.com/maps/search/']", "href", &mapsURL, nil, chromedp.ByQuery),
		chromedp.EmulateViewport(360, 800),
		chromedp.Navigate(server.URL+"/customers/new"),
		chromedp.WaitVisible("a[href='/customers/"+customerID+"/jobs/new']", chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.scrollWidth > document.documentElement.clientWidth + 1`, &pickerHorizontalOverflow),
		chromedp.Evaluate(`(() => { const target=document.querySelector('[data-existing-customer-job]'); const box=target.getBoundingClientRect(); return box.width < 44 || box.height < 44; })()`, &pickerTouchTargetTooSmall),
		chromedp.Click("a[href='/customers/"+customerID+"/jobs/new']", chromedp.ByQuery),
		chromedp.WaitVisible("[data-job-type]", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('.job-location-disclosure').open=true`, nil),
		chromedp.Click("[data-location-customer]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-location-search-results] .location-search__result", chromedp.ByQuery),
		chromedp.Click("[data-location-search-results] .location-search__result", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const editor=document.querySelector('[data-job-location-editor]');
			return editor.querySelector('[data-location-latitude]').value==='46.710000'
				&& editor.querySelector('[data-location-longitude]').value==='15.570000'
				&& editor.querySelector('[data-location-committed-latitude]').value===''
				&& editor.querySelector('[data-location-committed-source]').value==='';
		})()`, &customerAddressDrafted),
		chromedp.Evaluate(`(() => {
			const button=document.querySelector('[data-location-customer]');
			const box=button.getBoundingClientRect();
			return box.width>=44 && box.height>=44
				&& document.documentElement.scrollWidth<=document.documentElement.clientWidth+1;
		})()`, &customerAddressControlMobile),
		chromedp.Click("[data-location-clear]", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('.job-location-disclosure').open=false`, nil),
		chromedp.Evaluate(`(() => {
			const form=document.querySelector('.customer-job-workbench');
			const targets=Array.from(form.querySelectorAll('input,select,button,summary')).filter((element)=>element.getClientRects().length);
			return form.querySelectorAll('.job-core-section,.job-preference-section').length===2
				&& !form.querySelector('.job-additional-disclosure').open
				&& !form.querySelector('.job-location-disclosure').open
				&& document.documentElement.scrollWidth<=document.documentElement.clientWidth+1
				&& targets.every((element)=>element.getBoundingClientRect().height>=43.5);
		})()`, &compactJobFormMobile),
		chromedp.Evaluate(`(() => { const e=document.querySelector('[data-job-type]'); e.value='chipping_with_transport'; e.dispatchEvent(new Event('change',{bubbles:true})); return !document.querySelector('[data-transport-field]').hidden; })()`, &transportVisible),
		chromedp.Evaluate(`(() => { const e=document.querySelector('[data-transport-mode]'); e.value='external'; e.dispatchEvent(new Event('change',{bubbles:true})); return !document.querySelector('[data-external-confirmation]').hidden; })()`, &externalConfirmationVisible),
		chromedp.SetValue("[name='volume_m3']", "120", chromedp.ByQuery),
		chromedp.SetValue("[name='hack_duration']", "4:00", chromedp.ByQuery),
		chromedp.SetValue("[name='transport_duration']", "1:00", chromedp.ByQuery),
		chromedp.SetValue("[name='transport_trips']", "2", chromedp.ByQuery),
		chromedp.Click("[name='external_confirmed']", chromedp.ByQuery),
		chromedp.SetValue("[name='region']", "Unterneukirchen", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('.job-additional-disclosure').open=true`, nil),
		chromedp.SetValue("[name='preference_text']", "Oktober", chromedp.ByQuery),
		chromedp.Click("form[data-transport-form] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("details.compact-job-row:nth-of-type(2)", chromedp.ByQuery),
		chromedp.EmulateViewport(1280, 900),
	); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(mapsURL, "https://www.google.com/maps/search/") || !transportVisible || !externalConfirmationVisible || !compactJobFormMobile || !customerAddressDrafted || !customerAddressControlMobile || pickerHorizontalOverflow || pickerTouchTargetTooSmall {
		t.Fatalf("maps=%q transport=%v external=%v compact form=%v customer address draft=%v customer address mobile=%v picker overflow=%v touch too small=%v", mapsURL, transportVisible, externalConfirmationVisible, compactJobFormMobile, customerAddressDrafted, customerAddressControlMobile, pickerHorizontalOverflow, pickerTouchTargetTooSmall)
	}

	var jobCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM jobs WHERE customer_id = $1", customerID).Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if jobCount != 2 {
		t.Fatalf("job count = %d, want 2", jobCount)
	}

	var forbiddenStatus int
	requestExpression := fmt.Sprintf(`fetch(%q, {method:'POST', headers:{'Content-Type':'application/x-www-form-urlencoded'}, body:new URLSearchParams({csrf_token:document.querySelector("input[name='csrf_token']").value,version:'1',priority:'99'})}).then(r=>r.status)`, "/waitlist/"+firstWaitlistID+"/priority")
	awaitPromise := func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return params.WithAwaitPromise(true)
	}
	if err := chromedp.Run(browserContext, chromedp.Evaluate(requestExpression, &forbiddenStatus, awaitPromise)); err != nil {
		t.Fatal(err)
	}
	if forbiddenStatus != 403 {
		t.Fatalf("direct driver priority status = %d, want 403", forbiddenStatus)
	}

	var searchLocation, waitlistLocation, firstWaitlistText, detailHref string
	var customerToolbarHeight, waitlistToolbarHeight float64
	var waitlistCopyButtonValid, waitlistCopyFeedbackValid bool
	var horizontalOverflow bool
	var screenshot []byte
	if err := runBrowserStep(browserContext, "logout driver",
		chromedp.Click("[data-account-menu] summary", chromedp.ByQuery),
		chromedp.WaitVisible("form[action='/logout'] button[type='submit']", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("[data-account-menu] form[action='/logout']").requestSubmit()`, nil),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("logout driver: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "login admin",
		chromedp.SetValue("#username", "admin-e2e", chromedp.ByQuery),
		chromedp.SetValue("#password", adminPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("submit admin login: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "search customer",
		chromedp.WaitVisible("a[href='/customers']", chromedp.ByQuery),
		chromedp.Navigate(server.URL+"/customers"),
		chromedp.SetValue("#customer-search", "Huber", chromedp.ByQuery),
		chromedp.Click("form[action='/customers/search'] button[type='submit']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("submit customer search: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "inspect search results",
		chromedp.WaitVisible("form[action='/recent/customers/"+customerID+"']", chromedp.ByQuery),
		chromedp.Location(&searchLocation),
		chromedp.AttributeValue(".customer-name-link", "href", &detailHref, nil, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector(".customer-list-toolbar").getBoundingClientRect().height`, &customerToolbarHeight),
	); err != nil {
		t.Fatalf("inspect customer search: %s", browserDiagnostics(browserContext, err))
	}
	if detailHref != "/customers/"+customerID || customerToolbarHeight > 64 {
		t.Fatalf("customer detail href/toolbar height = %q/%.1f", detailHref, customerToolbarHeight)
	}
	if err := runBrowserStep(browserContext, "sort customer results",
		chromedp.Click(".customer-table thead th:first-child .customer-table-sort", chromedp.ByQuery),
		chromedp.WaitVisible(".customer-table th[aria-sort='ascending']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("customer sort: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "filter customer results",
		chromedp.Click(".customer-filter-menu > summary", chromedp.ByQuery),
		chromedp.WaitVisible(".customer-filter-menu__panel", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("#customer-list-controls [name='job_activity']").value="active"`, nil),
		chromedp.Click(".customer-filter-actions button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible(".customer-filter-menu[open]", chromedp.ByQuery),
		chromedp.WaitVisible(".customer-table tbody tr", chromedp.ByQuery),
		chromedp.Location(&searchLocation),
	); err != nil {
		t.Fatalf("customer filter: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "open customer detail",
		chromedp.Click(".customer-name-link", chromedp.ByQuery),
		chromedp.WaitVisible("details.edit-card summary", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("open customer detail: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "edit customer",
		chromedp.WaitVisible("details.edit-card summary", chromedp.ByQuery),
		chromedp.Click("details.edit-card summary", chromedp.ByQuery),
		chromedp.SetValue("details.edit-card [name='locality']", "Neuer Ort", chromedp.ByQuery),
		chromedp.Click("details.edit-card button[type='submit']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("submit customer edit: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "open compact waitlist",
		chromedp.WaitNotPresent("details.edit-card[open]", chromedp.ByQuery),
		chromedp.Navigate(server.URL+"/waitlist"),
		chromedp.WaitVisible("#waitlist-list-controls", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector(".waitlist-list-toolbar").getBoundingClientRect().height`, &waitlistToolbarHeight),
		chromedp.Evaluate(`(()=>{const button=document.querySelector(".waitlist-copy-button");if(!button)return false;const icon=button.querySelector("svg.copy-icon--default");if(!icon)return false;const buttonBox=button.getBoundingClientRect();const iconBox=icon.getBoundingClientRect();const style=getComputedStyle(button);return button.textContent.trim()===""&&(button.getAttribute("aria-label")||"").endsWith(" kopieren")&&buttonBox.width>=44&&buttonBox.height>=44&&iconBox.width<=16.1&&iconBox.height<=16.1&&parseFloat(style.borderTopWidth)===0&&style.backgroundColor==="rgba(0, 0, 0, 0)"})()`, &waitlistCopyButtonValid),
		chromedp.Click(".waitlist-copy-button", chromedp.ByQuery),
		chromedp.WaitVisible(".waitlist-copy-button.is-copied", chromedp.ByQuery),
		chromedp.Evaluate(`(()=>{const button=document.querySelector(".waitlist-copy-button.is-copied");return !!button&&button.textContent.trim()===""&&button.querySelectorAll("svg").length===2&&(button.getAttribute("aria-label")||"").endsWith(" kopiert")})()`, &waitlistCopyFeedbackValid),
	); err != nil {
		t.Fatalf("open compact waitlist: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "search and sort compact waitlist",
		chromedp.SetValue("#waitlist-search", "Huber", chromedp.ByQuery),
		chromedp.Click("#waitlist-list-controls .customer-list-toolbar__search button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible(".waitlist-table tbody tr", chromedp.ByQuery),
		chromedp.Click(".waitlist-table thead th:nth-child(4) .customer-table-sort", chromedp.ByQuery),
		chromedp.WaitVisible(".waitlist-table th[aria-sort='descending']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("search and sort compact waitlist: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "filter compact waitlist",
		chromedp.Click(".waitlist-filter-menu > summary", chromedp.ByQuery),
		chromedp.WaitVisible(".waitlist-filter-menu__panel", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("#waitlist-list-controls [name='incomplete']").checked=true`, nil),
		chromedp.Click(".waitlist-filter-menu .customer-filter-actions button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible(".waitlist-filter-chips", chromedp.ByQuery),
		chromedp.Location(&waitlistLocation),
	); err != nil {
		t.Fatalf("filter compact waitlist: %s", browserDiagnostics(browserContext, err))
	}
	if strings.Contains(waitlistLocation, "q=") || waitlistToolbarHeight > 64 || !waitlistCopyButtonValid || !waitlistCopyFeedbackValid {
		t.Fatalf("waitlist location/toolbar/copy button = %q/%.1f/%t/%t", waitlistLocation, waitlistToolbarHeight, waitlistCopyButtonValid, waitlistCopyFeedbackValid)
	}
	if err := runBrowserStep(browserContext, "mobile waitlist",
		chromedp.WaitNotPresent("details.edit-card[open]", chromedp.ByQuery),
		chromedp.EmulateViewport(360, 800),
		chromedp.Navigate(server.URL+"/waitlist?sort=volume&direction=desc"),
		chromedp.WaitVisible(".waitlist-table tbody tr", chromedp.ByQuery),
		chromedp.Text(".waitlist-table tbody tr", &firstWaitlistText, chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &horizontalOverflow),
		chromedp.FullScreenshot(&screenshot, 90),
	); err != nil {
		t.Fatalf("inspect mobile waitlist: %s", browserDiagnostics(browserContext, err))
	}
	if strings.Contains(searchLocation, "q=") {
		t.Fatalf("customer search leaked query into URL: %s", searchLocation)
	}
	if !strings.Contains(firstWaitlistText, "120.00 m³") || horizontalOverflow {
		t.Fatalf("mobile waitlist first card = %q, horizontal overflow = %v", firstWaitlistText, horizontalOverflow)
	}
	var locality string
	if err := pool.QueryRow(ctx, "SELECT locality FROM customers WHERE id = $1", customerID).Scan(&locality); err != nil {
		t.Fatal(err)
	}
	if locality != "Neuer Ort" {
		t.Fatalf("admin-edited locality = %q", locality)
	}
	artifact := filepath.Join(t.ArtifactDir(), "task02-mobile-waitlist.png")
	if err := os.WriteFile(artifact, screenshot, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("mobile browser screenshot: %s", artifact)

	exceptionLock.Lock()
	defer exceptionLock.Unlock()
	if len(exceptions) > 0 {
		t.Fatalf("browser JavaScript exceptions: %v", exceptions)
	}
}

func TestTask02LocationSearchWithoutMap(t *testing.T) {
	appJavaScript, err := assets.Files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(response, `<!doctype html><html><body>
<span hidden data-map-assets data-map-script="/missing-map.js" data-map-worker="/missing-worker.js" data-map-css="/missing-map.css" data-map-attribution="Kartendaten"></span>
<form><input type="hidden" name="csrf_token" value="test-csrf">
<section data-job-location-editor>
  <div data-map-canvas tabindex="0"><p data-map-fallback hidden></p></div>
  <span data-location-badge>Fehlt</span>
  <input data-location-latitude><input data-location-longitude>
  <input type="hidden" data-location-committed-latitude><input type="hidden" data-location-committed-longitude><input type="hidden" data-location-committed-source>
  <input type="search" data-location-search-input><button type="button" data-location-search-submit>Suchen</button>
  <p data-location-search-status></p><ul data-location-search-results hidden></ul>
  <p data-location-message></p><button type="button" data-location-commit>Standort übernehmen</button>
</section></form><script src="/assets/app.js"></script></body></html>`)
		case "/assets/app.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(appJavaScript)
		case "/api/v1/geocoding/search":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"results":[{"label":"Waldstraße 9, Unterneukirchen","latitude":46.71,"longitude":15.57,"bounds":[46.70,46.72,15.56,15.58]}]}`)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU,
		chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck,
		chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(1280, 900),
	)
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 60*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browserContext) })

	var draftPrepared, committed bool
	if err := chromedp.Run(browserContext,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("[data-map-fallback]:not([hidden])", chromedp.ByQuery),
		chromedp.SetValue("[data-location-search-input]", "Waldstraße 9, Unterneukirchen", chromedp.ByQuery),
		chromedp.Click("[data-location-search-submit]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-location-search-results] .location-search__result", chromedp.ByQuery),
		chromedp.Click("[data-location-search-results] .location-search__result", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const editor = document.querySelector('[data-job-location-editor]');
			return editor.querySelector('[data-location-latitude]').value === '46.710000'
				&& editor.querySelector('[data-location-longitude]').value === '15.570000'
				&& editor.querySelector('[data-location-committed-latitude]').value === ''
				&& editor.querySelector('[data-location-committed-longitude]').value === ''
				&& editor.querySelector('[data-location-committed-source]').value === '';
		})()`, &draftPrepared),
		chromedp.Click("[data-location-commit]", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const editor = document.querySelector('[data-job-location-editor]');
			return editor.querySelector('[data-location-committed-latitude]').value === '46.710000'
				&& editor.querySelector('[data-location-committed-longitude]').value === '15.570000'
				&& editor.querySelector('[data-location-committed-source]').value === 'coordinates';
		})()`, &committed),
	); err != nil {
		t.Fatalf("location fallback journey: %s", browserDiagnostics(browserContext, err))
	}
	if !draftPrepared || !committed {
		t.Fatalf("fallback draft prepared=%v, committed=%v", draftPrepared, committed)
	}
}

func TestTask02CustomerAddressLocationSelectionWithoutStoredCoordinates(t *testing.T) {
	appJavaScript, err := assets.Files.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(response, `<!doctype html><html><body>
<span hidden data-map-assets data-map-script="/missing-map.js" data-map-worker="/missing-worker.js" data-map-css="/missing-map.css" data-map-attribution="Kartendaten"></span>
<form><input type="hidden" name="csrf_token" value="test-csrf">
<div data-customer-fields>
  <input name="street" value="Bräuerau 5a"><input name="postal_code" value="4162">
  <input name="locality" value="Julbach"><input name="region" value="Rohrbach">
</div>
<section data-job-location-editor>
  <div data-map-canvas tabindex="0"><p data-map-fallback hidden></p></div>
  <span data-location-badge>Fehlt</span>
  <input data-location-latitude><input data-location-longitude>
  <input type="hidden" data-location-committed-latitude><input type="hidden" data-location-committed-longitude><input type="hidden" data-location-committed-source>
  <input type="search" data-location-search-input><button type="button" data-location-search-submit>Suchen</button>
  <p data-location-search-status></p><ul data-location-search-results hidden></ul>
  <button type="button" data-location-customer>Kundenadresse wählen</button>
  <p data-location-message></p><button type="button" data-location-commit>Standort übernehmen</button>
</section></form><script src="/assets/app.js"></script></body></html>`)
		case "/assets/app.js":
			response.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = response.Write(appJavaScript)
		case "/api/v1/geocoding/search":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"results":[{"label":"Bräuerau 5a, 4162 Julbach","latitude":48.658,"longitude":13.866,"bounds":[48.657,48.659,13.865,13.867]}]}`)
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU,
		chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck,
		chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(1280, 900),
	)
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 60*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browserContext) })

	var draftPrepared, committed, unavailableHandled, missingAddressHandled bool
	if err := chromedp.Run(browserContext,
		chromedp.Navigate(server.URL),
		chromedp.WaitVisible("[data-map-fallback]:not([hidden])", chromedp.ByQuery),
		chromedp.Click("[data-location-customer]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-location-search-results] .location-search__result", chromedp.ByQuery),
		chromedp.Click("[data-location-search-results] .location-search__result", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const editor = document.querySelector('[data-job-location-editor]');
			return editor.querySelector('[data-location-latitude]').value === '48.658000'
				&& editor.querySelector('[data-location-longitude]').value === '13.866000'
				&& editor.querySelector('[data-location-committed-latitude]').value === ''
				&& editor.querySelector('[data-location-committed-longitude]').value === ''
				&& editor.querySelector('[data-location-committed-source]').value === '';
		})()`, &draftPrepared),
		chromedp.Click("[data-location-commit]", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const editor = document.querySelector('[data-job-location-editor]');
			return editor.querySelector('[data-location-committed-latitude]').value === '48.658000'
				&& editor.querySelector('[data-location-committed-longitude]').value === '13.866000'
				&& editor.querySelector('[data-location-committed-source]').value === 'customer_address';
		})()`, &committed),
		chromedp.Evaluate(`document.querySelector('[data-location-search-input]').disabled=true`, nil),
		chromedp.Click("[data-location-customer]", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const editor = document.querySelector('[data-job-location-editor]');
			return editor.querySelector('[data-location-message]').textContent.includes('Adresssuche ist nicht konfiguriert')
				&& editor.querySelector('[data-location-committed-source]').value === 'customer_address';
		})()`, &unavailableHandled),
		chromedp.Evaluate(`(() => {
			document.querySelector('[data-location-search-input]').disabled=false;
			document.querySelectorAll('[data-customer-fields] input').forEach((input) => {
				input.value='';
				input.dispatchEvent(new Event('input', {bubbles:true}));
			});
		})()`, nil),
		chromedp.Click("[data-location-customer]", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const editor = document.querySelector('[data-job-location-editor]');
			return editor.querySelector('[data-location-message]').textContent.includes('Bitte zuerst eine Kundenadresse erfassen')
				&& editor.querySelector('[data-location-committed-source]').value === 'customer_address';
		})()`, &missingAddressHandled),
	); err != nil {
		t.Fatalf("customer address location journey: %s", browserDiagnostics(browserContext, err))
	}
	if !draftPrepared || !committed || !unavailableHandled || !missingAddressHandled {
		t.Fatalf("customer address draft prepared=%v, committed=%v, unavailable=%v, missing address=%v", draftPrepared, committed, unavailableHandled, missingAddressHandled)
	}
}

type task02Geocoder struct {
	mu    sync.Mutex
	query string
}

func (geocoder *task02Geocoder) Search(_ context.Context, query string) ([]geocode.Result, error) {
	geocoder.mu.Lock()
	geocoder.query = query
	geocoder.mu.Unlock()
	return []geocode.Result{{
		Label: "Waldstraße 9, Unterneukirchen", Latitude: 46.71, Longitude: 15.57,
		Bounds: [4]float64{46.70, 46.72, 15.56, 15.58},
	}}, nil
}

func (geocoder *task02Geocoder) lastQuery() string {
	geocoder.mu.Lock()
	defer geocoder.mu.Unlock()
	return geocoder.query
}

func e2eDashboard(t *testing.T, pool *pgxpool.Pool) *dashboard.Service {
	t.Helper()
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	service, err := dashboard.New(postgres.NewDashboardStore(pool), dashboard.Config{
		Location: location, HorizonDays: 14, PendingAfter: 15 * time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00",
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func runBrowserStep(ctx context.Context, name string, actions ...chromedp.Action) error {
	stepContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := chromedp.Run(stepContext, actions...); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func browserDiagnostics(ctx context.Context, cause error) string {
	var location, bodyText string
	diagnosticContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = chromedp.Run(diagnosticContext,
		chromedp.Location(&location),
		chromedp.Text("body", &bodyText, chromedp.ByQuery),
	)
	if len(bodyText) > 500 {
		bodyText = bodyText[:500]
	}
	return fmt.Sprintf("%v; location=%q; body=%q", cause, location, bodyText)
}

func browserProfileDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "hackwerk-e2e-browser-*")
	if err != nil {
		t.Fatal(err)
	}
	// Every caller obtains the profile while constructing allocator options and
	// registers its browser/allocator cancellations afterwards. Cleanup is LIFO,
	// so those cancellations are invoked before profile removal starts here.
	t.Cleanup(func() { removeBrowserProfile(t, directory) })
	return directory
}

func removeBrowserProfile(t *testing.T, directory string) {
	t.Helper()
	const (
		removalTimeout = 20 * time.Second
		maximumBackoff = 500 * time.Millisecond
	)
	backoff := 25 * time.Millisecond
	deadline := time.Now().Add(removalTimeout)
	for {
		err := os.RemoveAll(directory)
		if err == nil {
			return
		}
		if !isTransientWindowsProfileLock(err) {
			t.Errorf("remove browser profile %s: %v", directory, err)
			return
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Errorf("remove browser profile %s after %s: %v", directory, removalTimeout, err)
			return
		}
		if backoff > remaining {
			backoff = remaining
		}
		time.Sleep(backoff)
		if backoff < maximumBackoff {
			backoff = min(backoff*2, maximumBackoff)
		}
	}
}

func isTransientWindowsProfileLock(err error) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	const (
		windowsSharingViolation syscall.Errno = 32
		windowsLockViolation    syscall.Errno = 33
	)
	return errors.Is(err, windowsSharingViolation) || errors.Is(err, windowsLockViolation)
}

func TestBrowserProfileRemovalRetriesOnlyWindowsLockErrors(t *testing.T) {
	sharingViolation := &os.PathError{Op: "remove", Path: "browser-profile", Err: syscall.Errno(32)}
	if got, want := isTransientWindowsProfileLock(sharingViolation), runtime.GOOS == "windows"; got != want {
		t.Fatalf("sharing violation retryable=%v want %v", got, want)
	}
	permissionDenied := &os.PathError{Op: "remove", Path: "browser-profile", Err: syscall.Errno(5)}
	if isTransientWindowsProfileLock(permissionDenied) {
		t.Fatal("non-transient permission error must not be retried")
	}
}

func task02Application(t *testing.T, ctx context.Context, databaseURL string) (*pgxpool.Pool, *auth.Service, *customers.Service, string, string) {
	t.Helper()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionDown, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	databaseConfig := config.Database{
		URL: databaseURL, MaxConnections: 10, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second,
	}
	pool, err := postgres.Open(ctx, databaseConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE job_notes, waitlist_entries, jobs, job_number_counters, customers,
		audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(postgres.NewIdentityStore(pool), hasher, time.Now, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	driverPassword := randomE2EPassword(t)
	adminPassword := randomE2EPassword(t)
	system := auth.Actor{Role: auth.RoleAdmin, System: true, DisplayName: "E2E Setup"}
	for _, account := range []auth.CreateUserInput{
		{Username: "driver-e2e", DisplayName: "Franz Fahrer", Role: auth.RoleDriver, Password: driverPassword, RequestID: "e2e-setup"},
		{Username: "admin-e2e", DisplayName: "Anna Admin", Role: auth.RoleAdmin, Password: adminPassword, RequestID: "e2e-setup"},
	} {
		if _, err := identity.CreateUser(ctx, system, account); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET must_change_password = false"); err != nil {
		t.Fatal(err)
	}
	customerService, err := customers.NewService(postgres.NewCustomerStore(pool))
	if err != nil {
		t.Fatal(err)
	}
	return pool, identity, customerService, driverPassword, adminPassword
}

func randomE2EPassword(t *testing.T) string {
	t.Helper()
	token, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	return "E2E-" + token
}

func browserExecutable(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("E2E_BROWSER_PATH"); configured != "" {
		if _, err := os.Stat(configured); err != nil {
			t.Fatalf("E2E_BROWSER_PATH: %v", err)
		}
		return configured
	}
	candidates := []string{"google-chrome", "chromium", "chromium-browser", "msedge"}
	if runtime.GOOS == "windows" {
		candidates = append(candidates,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		)
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Fatal("no Chrome, Chromium, or Edge executable found; set E2E_BROWSER_PATH")
	return ""
}
