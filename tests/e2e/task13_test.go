//go:build e2e

package e2e_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/app"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/calendarfeed"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/notification"
	"example.invalid/hackplan/internal/planning"
	"example.invalid/hackplan/internal/voice"
	"example.invalid/hackplan/internal/web"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type e2ePageAudit struct {
	Path                   string
	TitleMissing           bool
	ErrorPage              bool
	Overflow               bool
	MissingLandmarks       bool
	H1Count                int
	DuplicateIDs           []string
	MissingLabels          []string
	SmallControls          []string
	SmallCheckboxLabels    []string
	BadSelects             []string
	CalendarAssetCount     int
	CalendarAssetsExpected bool
}

func TestTask13AllMainPagesDesktopAndMobileUsability(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, _, _, jobID, _, adminPassword, driverPassword := task04Application(t, databaseURL)
	routeLocations, routeLocationStore := e2eRouteLocations(t, pool)
	customerService, err := app.CustomerService(pool)
	if err != nil {
		t.Fatal(err)
	}
	var customerID string
	if err := pool.QueryRow(t.Context(), "SELECT customer_id::text FROM jobs WHERE id=$1", jobID).Scan(&customerID); err != nil {
		t.Fatal(err)
	}

	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	routerAdapter := planning.NewHaversineRouter(1.3, 55)
	routeService, err := planning.NewRouteService(postgres.NewRouteStore(pool), routerAdapter, routerAdapter, planning.DefaultRouteConfig())
	if err != nil {
		t.Fatal(err)
	}
	planningConfig := planning.DefaultConfig(location)
	planningService, err := planning.New(postgres.NewPlanningStore(pool), e2ePlanningAvailability{service: drivers}, routerAdapter, planningConfig, time.Now, planning.WithDefaultStartProvider(e2eDefaultStart{store: routeLocationStore}))
	if err != nil {
		t.Fatal(err)
	}
	voiceService, err := voice.New(
		postgres.NewVoiceStore(pool),
		voice.FakeTranscriber{Text: "Synthetischer Testauftrag"},
		voice.RuleExtractor{},
		voice.Config{Enabled: true, Retention: time.Hour, RateLimitPerMinute: 10, ConcurrentPerUser: 2, Timezone: location},
		time.Now,
	)
	if err != nil {
		t.Fatal(err)
	}
	notificationStore := postgres.NewNotificationStore(pool)
	confirmations, err := notification.NewConfirmationService(notificationStore, notification.DevelopmentKeyRing(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	notifications, err := notification.NewAdminService(notificationStore, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewUnstartedServer(nil)
	feedService, err := calendarfeed.New(
		postgres.NewCalendarFeedStore(pool),
		calendarfeed.Config{
			BaseURL: "http://" + server.Listener.Addr().String(), UIDDomain: "hackwerk.example",
			CalendarName: "HackWerk Termine", ExportMaxDays: 366, HistoryDays: 90, FutureDays: 366,
		},
		time.Now,
		auth.NewToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		AppName: "HackWerk", BaseURL: "http://" + server.Listener.Addr().String(), Timezone: "Europe/Vienna",
		Database: config.Database{ReadinessTimeout: 2 * time.Second},
		HTTP:     config.HTTP{InternalRateLimit: 10000},
		Auth: config.Auth{
			SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf",
			SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour,
		},
		Confirmation: config.Confirmation{RateLimit: 30},
		CalendarFeed: config.CalendarFeed{RateLimit: 120},
		Planning:     config.Planning{BusinessOpen: "07:00"},
		Map:          config.Map{Attribution: "OpenStreetMap-Mitwirkende"},
		Voice: config.Voice{
			Enabled: true, Transcriber: "fake", MaxDuration: 90 * time.Second, MaxBytes: 15 << 20,
			ProviderTimeout: 5 * time.Second, TempDir: t.TempDir(), ExternalProviderNote: "Synthetischer Testprovider",
		},
	}
	handler, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"},
		Identity: identity, Customers: customerService, Drivers: drivers, Resources: resources, Appointments: appointments,
		Dashboard: e2eDashboard(t, pool), Confirmations: confirmations, Notifications: notifications,
		CalendarFeeds: feedService, Planning: planningService, Routes: routeService, RouteLocations: routeLocations, Voice: voiceService,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(server.Close)

	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox,
		chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(1440, 900),
	)
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browser, cancelBrowser := chromedp.NewContext(allocator)
	t.Cleanup(cancelBrowser)
	browser, cancelTimeout := context.WithTimeout(browser, 10*time.Minute)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browser) })

	var exceptionLock sync.Mutex
	var exceptions []string
	chromedp.ListenTarget(browser, func(event any) {
		value, ok := event.(*cdpruntime.EventExceptionThrown)
		if !ok {
			return
		}
		exceptionLock.Lock()
		exceptions = append(exceptions, value.ExceptionDetails.Text)
		exceptionLock.Unlock()
	})

	if err := e2eLogin(browser, server.URL, "admin-task04", adminPassword); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	adminPages := []string{
		"/dashboard", "/calendar?date=2026-08-25", "/calendar/feeds", "/waitlist", "/customers",
		"/customers/new", "/customers/" + customerID, "/customers/" + customerID + "/jobs/new",
		"/admin/drivers", "/admin/resources", "/planning", "/planning/routes",
		"/admin/notifications", "/admin/users", "/profile", "/password", "/voice",
	}
	viewports := []struct {
		name          string
		width, height int64
	}{
		{name: "desktop-720p", width: 1280, height: 720},
		{name: "desktop-1080p", width: 1920, height: 1080},
		{name: "mobile-360", width: 360, height: 800},
		{name: "mobile-390", width: 390, height: 844},
		{name: "mobile-412", width: 412, height: 915},
	}
	for _, viewport := range viewports {
		auditPagesAtViewport(t, browser, server.URL, "admin-"+viewport.name, viewport.width, viewport.height, adminPages)
	}

	if err := runBrowserStep(browser, "logout admin",
		chromedp.Evaluate(`document.querySelector("header form[action='/logout']").requestSubmit()`, nil),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if err := e2eLogin(browser, server.URL, "driver-task04", driverPassword); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	driverPages := []string{
		"/dashboard", "/calendar?date=2026-08-25", "/calendar/feeds", "/waitlist", "/customers",
		"/customers/new", "/customers/" + customerID, "/customers/" + customerID + "/jobs/new",
		"/availability", "/my-route?date=2026-08-25", "/profile", "/password", "/voice",
	}
	for _, viewport := range viewports {
		auditPagesAtViewport(t, browser, server.URL, "driver-"+viewport.name, viewport.width, viewport.height, driverPages)
	}

	exceptionLock.Lock()
	defer exceptionLock.Unlock()
	if len(exceptions) > 0 {
		t.Fatalf("uncaught JavaScript exceptions: %v", exceptions)
	}
}

func e2eLogin(ctx context.Context, baseURL, username, password string) error {
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(1440, 900),
		chromedp.Navigate(baseURL+"/login"),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
	); err != nil {
		return err
	}
	return runBrowserStep(ctx, "login "+username,
		chromedp.SetValue("#username", username, chromedp.ByQuery),
		chromedp.SetValue("#password", password, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery),
	)
}

func auditPagesAtViewport(t *testing.T, ctx context.Context, baseURL, name string, width, height int64, paths []string) {
	t.Helper()
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(width, height)); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		var audit e2ePageAudit
		if err := runBrowserStep(ctx, name+" "+path,
			chromedp.Navigate(baseURL+path),
			chromedp.WaitReady("main", chromedp.ByQuery),
			chromedp.Evaluate(pageAuditScript, &audit),
		); err != nil {
			t.Errorf("%s: %s", name, browserDiagnostics(ctx, err))
			continue
		}
		expectedPath := strings.SplitN(path, "?", 2)[0]
		calendarExpected := strings.HasPrefix(path, "/calendar?")
		audit.CalendarAssetsExpected = calendarExpected
		if audit.Path != expectedPath || audit.TitleMissing || audit.ErrorPage || audit.Overflow || audit.MissingLandmarks || audit.H1Count != 1 ||
			len(audit.DuplicateIDs) > 0 || len(audit.MissingLabels) > 0 || len(audit.SmallControls) > 0 ||
			len(audit.SmallCheckboxLabels) > 0 || len(audit.BadSelects) > 0 ||
			(calendarExpected && audit.CalendarAssetCount != 5) || (!calendarExpected && audit.CalendarAssetCount != 0) {
			t.Errorf("%s %s usability audit: %+v", name, path, audit)
		}
	}
}

const pageAuditScript = `(() => {
	const visible = node => {
		const style=getComputedStyle(node), rect=node.getBoundingClientRect();
		return style.display!=='none' && style.visibility!=='hidden' && rect.width>0 && rect.height>0;
	};
	const describe = node => {
		const text=(node.getAttribute('aria-label')||node.getAttribute('name')||node.id||node.textContent||node.tagName).trim();
		return node.tagName.toLowerCase()+(text ? ':'+text.slice(0,60) : '');
	};
	const labelled = node => {
		if ((node.getAttribute('aria-label')||'').trim() || (node.getAttribute('aria-labelledby')||'').trim()) return true;
		if (node.closest('label')) return true;
		return Boolean(node.id && document.querySelector('label[for="'+CSS.escape(node.id)+'"]'));
	};
	const controls=[...document.querySelectorAll('input:not([type=hidden]):not([type=button]):not([type=submit]),select,textarea')].filter(visible);
	const touchControls=[...document.querySelectorAll('button,a.button,summary,[role=button],.mobile-bottom-nav a,.nav-menu__panel a')].filter(visible);
	const checkboxLabels=[...document.querySelectorAll('input[type=checkbox],input[type=radio]')].filter(visible).map(input => input.closest('label') || (input.id && document.querySelector('label[for="'+CSS.escape(input.id)+'"]'))).filter(Boolean);
	const ids=[...document.querySelectorAll('[id]')].map(node=>node.id).filter(Boolean);
	const duplicateIDs=[...new Set(ids.filter((id,index)=>ids.indexOf(id)!==index))];
	const badSelects=[...document.querySelectorAll('select')].filter(visible).filter(node=>{
		const style=getComputedStyle(node), color=style.backgroundColor.replace(/\s/g,'');
		return !style.colorScheme.includes('light') || color==='rgb(0,0,0)' || color==='rgba(0,0,0,1)';
	}).map(describe);
	return {
		Path:location.pathname, TitleMissing:document.title.trim()==='', ErrorPage:Boolean(document.querySelector('.error-page')),
		Overflow:document.documentElement.scrollWidth>window.innerWidth+1,
		MissingLandmarks:!document.querySelector('.skip-link')||!document.querySelector('header')||!document.querySelector('main')||!document.querySelector('footer')||document.documentElement.lang!=='de-AT',
		H1Count:document.querySelectorAll('main h1').length, DuplicateIDs:duplicateIDs,
		MissingLabels:controls.filter(node=>!labelled(node)).map(describe),
		SmallControls:innerWidth<=760 ? touchControls.filter(node=>{const rect=node.getBoundingClientRect();return rect.width<43.5||rect.height<43.5}).map(node=>{const rect=node.getBoundingClientRect();return describe(node)+'@'+Math.round(rect.width)+'x'+Math.round(rect.height)+'.'+node.className}) : [],
		SmallCheckboxLabels:innerWidth<=760 ? checkboxLabels.filter(node=>node.getBoundingClientRect().height<43.5).map(describe) : [],
		BadSelects:badSelects,
		CalendarAssetCount:[...document.querySelectorAll('link[href],script[src]')].filter(node=>(node.href||node.src).includes('fullcalendar')).length
	};
})()`
