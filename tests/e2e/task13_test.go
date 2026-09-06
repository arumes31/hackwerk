//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	"github.com/chromedp/cdproto/emulation"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type e2ePageAudit struct {
	Path                     string
	TitleMissing             bool
	ErrorPage                bool
	Overflow                 bool
	OverflowElements         []string
	OverflowContainers       []string
	ViewportWidth            float64
	RootScrollWidth          float64
	BodyScrollWidth          float64
	MissingLandmarks         bool
	H1Count                  int
	DuplicateIDs             []string
	MissingLabels            []string
	SmallControls            []string
	SmallCheckboxLabels      []string
	NarrowDateInputs         []string
	MisalignedAdminActions   []string
	BadSelects               []string
	HiddenSortControls       []string
	CalendarAssetCount       int
	CalendarAssetsExpected   bool
	MobileCaptureMissing     bool
	MobileCaptureOverlaps    []string
	MobileStickyOverlaps     []string
	MobileWorkflowNavMissing bool
	MobileCurrentCount       int
	MobileTabCount           int
	MobileAppTitlePresent    bool
	MobileAppBarHeight       float64
	DensityFailures          []string
}

func TestTask13AllMainPagesDesktopAndMobileUsability(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, driverID, _, jobID, _, adminPassword, driverPassword := task04Application(t, databaseURL)
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
	securityKeys, err := auth.NewSecurityKeyRing(map[string]string{
		"e2e-v1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x73}, 32)),
	}, "e2e-v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := identity.ConfigureSecurity(auth.SecurityConfig{
		Keys: securityKeys, AppName: "HackWerk", BaseURL: "http://localhost",
		EmailVerificationTTL: 24 * time.Hour, EmailResendInterval: time.Minute,
		MFAChallengeTTL: 5 * time.Minute, WebAuthnChallengeTTL: 5 * time.Minute, MailMaxAttempts: 6,
	}); err != nil {
		t.Fatal(err)
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
		exceptions = append(exceptions, value.ExceptionDetails.Error())
		exceptionLock.Unlock()
	})

	if err := e2eLogin(browser, server.URL, "admin-task04", adminPassword); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	adminPages := []string{
		"/dashboard", "/calendar?date=2026-08-25", "/calendar/feeds", "/waitlist", "/customers",
		"/customers/new", "/customers/" + customerID, "/customers/" + customerID + "/jobs/new",
		"/calendar/plan?job_id=" + jobID, "/admin/drivers", "/admin/drivers/" + driverID + "/availability", "/admin/resources",
		"/planning", "/planning/routes", "/settings/route-locations", "/admin/notifications", "/admin/voice-recordings",
		"/admin/users", "/profile", "/password", "/voice", "/hilfe/erste-schritte",
	}
	viewports := []struct {
		name          string
		width, height int64
	}{
		{name: "desktop-720p", width: 1280, height: 720},
		{name: "desktop-1080p", width: 1920, height: 1080},
		{name: "mobile-320", width: 320, height: 720},
		{name: "mobile-360", width: 360, height: 800},
		{name: "mobile-390", width: 390, height: 844},
		{name: "mobile-412", width: 412, height: 915},
		{name: "tablet-768", width: 768, height: 1024},
		{name: "tablet-1050", width: 1050, height: 900},
	}
	for _, viewport := range viewports {
		auditPagesAtViewport(t, browser, server.URL, "admin-"+viewport.name, viewport.width, viewport.height, adminPages)
	}
	auditPagesAtViewport(t, browser, server.URL, "admin-short-landscape", 667, 375, []string{
		"/customers/new", "/customers/" + customerID, "/planning", "/planning/routes",
	})
	auditNoJavaScriptMobileSearch(t, browser, server.URL)

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
		"/availability", "/my-route?date=2026-08-25", "/profile", "/password", "/voice", "/hilfe/erste-schritte",
	}
	for _, viewport := range viewports {
		auditPagesAtViewport(t, browser, server.URL, "driver-"+viewport.name, viewport.width, viewport.height, driverPages)
	}
	auditPagesAtViewport(t, browser, server.URL, "driver-short-landscape", 667, 375, []string{
		"/customers/new", "/customers/" + customerID,
	})

	exceptionLock.Lock()
	defer exceptionLock.Unlock()
	if len(exceptions) > 0 {
		t.Fatalf("uncaught JavaScript exceptions: %v", exceptions)
	}
}

func auditNoJavaScriptMobileSearch(t *testing.T, ctx context.Context, baseURL string) {
	t.Helper()
	var geometry struct {
		Left       float64
		Right      float64
		InnerWidth float64
	}
	var location string
	if err := runBrowserStep(ctx, "mobile search fallback without JavaScript",
		chromedp.EmulateViewport(320, 720),
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(true).Do(ctx) }),
		chromedp.Navigate(baseURL+"/dashboard"),
		chromedp.WaitVisible(".command-search-fallback--mobile > summary", chromedp.ByQuery),
		chromedp.Click(".command-search-fallback--mobile > summary", chromedp.ByQuery),
		chromedp.WaitVisible(".command-search-fallback--mobile .command-search-fallback__panel", chromedp.ByQuery),
		chromedp.Evaluate(`(()=>{const rect=document.querySelector('.command-search-fallback--mobile .command-search-fallback__panel').getBoundingClientRect();return {Left:rect.left,Right:rect.right,InnerWidth:innerWidth}})()`, &geometry),
		chromedp.SetValue(".command-search-fallback--mobile input[name='q']", "Franz", chromedp.ByQuery),
		chromedp.Click(".command-search-fallback--mobile button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("main .search-results", chromedp.ByQuery),
		chromedp.Location(&location),
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetScriptExecutionDisabled(false).Do(ctx) }),
	); err != nil {
		t.Fatal(browserDiagnostics(ctx, err))
	}
	if geometry.Left < 0 || geometry.Right > geometry.InnerWidth || location != baseURL+"/search" {
		t.Fatalf("mobile no-JavaScript search fallback geometry/location=%+v/%q", geometry, location)
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
		var navigationAudit struct {
			CaptureMissing, SecondaryNavigationPresent bool
			AdminMoreMissing                           bool
			AdminMoreMissingDestinations               int
			CurrentCount, TabCount                     int
		}
		if err := runBrowserStep(ctx, name+" "+path,
			chromedp.Navigate(baseURL+path),
			chromedp.WaitReady("main", chromedp.ByQuery),
			chromedp.Evaluate(mobileNavigationAuditScript, &navigationAudit),
			chromedp.Evaluate(`(()=>{if(innerWidth>760)return;const panel=document.querySelector('.intake-new-customer,.customer-edit-card');if(panel)panel.open=true})()`, nil),
			chromedp.Evaluate(pageAuditScript, &audit),
		); err != nil {
			t.Errorf("%s: %s", name, browserDiagnostics(ctx, err))
			continue
		}
		audit.MobileCaptureMissing = navigationAudit.CaptureMissing
		audit.MobileCurrentCount = navigationAudit.CurrentCount
		audit.MobileTabCount = navigationAudit.TabCount
		expectedPath := strings.SplitN(path, "?", 2)[0]
		expectAdminMore := strings.HasPrefix(name, "admin-") && width <= 1050
		calendarExpected := strings.HasPrefix(path, "/calendar?")
		audit.CalendarAssetsExpected = calendarExpected
		if audit.Path != expectedPath || audit.TitleMissing || audit.ErrorPage || audit.Overflow || audit.MissingLandmarks || audit.H1Count != 1 ||
			len(audit.DuplicateIDs) > 0 || len(audit.MissingLabels) > 0 || len(audit.SmallControls) > 0 ||
			len(audit.SmallCheckboxLabels) > 0 || len(audit.NarrowDateInputs) > 0 || len(audit.MisalignedAdminActions) > 0 || len(audit.BadSelects) > 0 || len(audit.HiddenSortControls) > 0 || audit.MobileCaptureMissing ||
			len(audit.MobileCaptureOverlaps) > 0 || len(audit.MobileStickyOverlaps) > 0 || audit.MobileWorkflowNavMissing || audit.MobileCurrentCount > 1 ||
			audit.MobileAppTitlePresent || (width <= 1050 && audit.MobileAppBarHeight > 56) || len(audit.DensityFailures) > 0 ||
			(width <= 1050 && audit.MobileTabCount != 5) ||
			(width <= 1050 && navigationAudit.SecondaryNavigationPresent) ||
			(expectAdminMore && (navigationAudit.AdminMoreMissing || navigationAudit.AdminMoreMissingDestinations != 0)) ||
			(calendarExpected && audit.CalendarAssetCount != 5) || (!calendarExpected && audit.CalendarAssetCount != 0) {
			t.Errorf("%s %s usability audit: %+v", name, path, audit)
		}
		if screenshotDir := os.Getenv("E2E_SCREENSHOT_DIR"); screenshotDir != "" {
			filename := map[string]map[string]string{
				"admin-mobile-360": {
					"/calendar?date=2026-08-25": "task13-mobile-calendar-top.png",
					"/waitlist":                 "task13-mobile-waitlist-top.png",
					"/customers":                "task13-mobile-customers-top.png",
				},
				"admin-desktop-1080p": {
					"/calendar?date=2026-08-25": "task13-desktop-calendar.png",
					"/waitlist":                 "task13-desktop-waitlist.png",
					"/planning":                 "task13-desktop-planning.png",
					"/planning/routes":          "task13-desktop-routes.png",
				},
			}[name][path]
			if filename != "" {
				var screenshot []byte
				if err := chromedp.Run(ctx, chromedp.CaptureScreenshot(&screenshot)); err != nil {
					t.Errorf("%s %s screenshot: %v", name, path, err)
					continue
				}
				if err := os.MkdirAll(screenshotDir, 0o700); err != nil {
					t.Errorf("%s %s screenshot directory: %v", name, path, err)
					continue
				}
				if err := os.WriteFile(filepath.Join(screenshotDir, filename), screenshot, 0o600); err != nil {
					t.Errorf("%s %s screenshot write: %v", name, path, err)
				}
			}
		}
	}
}

const mobileNavigationAuditScript = `(() => {
	const visible=node=>{const style=getComputedStyle(node),rect=node.getBoundingClientRect();return style.display!=='none'&&style.visibility!=='hidden'&&rect.width>0&&rect.height>0&&(!node.checkVisibility||node.checkVisibility({checkOpacity:true,checkVisibilityCSS:true}))};
	const navigation=document.querySelector('.mobile-bottom-nav');
	if (!navigation||!visible(navigation)) return {CaptureMissing:false,SecondaryNavigationPresent:false,AdminMoreMissing:false,AdminMoreMissingDestinations:0,CurrentCount:0,TabCount:0};
	const capture=document.querySelector('.mobile-primary-action');
	const more=document.querySelector('#mobile-more-sheet');
	const adminDestinations=['/planning','/planning/routes','/admin/drivers','/admin/resources','/settings/route-locations','/admin/notifications','/admin/voice-recordings','/admin/users'];
	const currentCount=[...navigation.querySelectorAll('[aria-current="page"]')].filter(visible).length;
	const tabCount=navigation.querySelectorAll('[data-mobile-nav-item]').length;
	return {CaptureMissing:!capture||!visible(capture)||capture.closest('.mobile-bottom-nav')===navigation,
		SecondaryNavigationPresent:Boolean(document.querySelector('.mobile-admin-nav')),
		AdminMoreMissing:!more,
		AdminMoreMissingDestinations:more?adminDestinations.filter(href=>!more.querySelector('a[href="'+href+'"]')).length:adminDestinations.length,
		CurrentCount:currentCount,TabCount:tabCount};
})()`

const pageAuditScript = `(() => {
	const visible = node => {
		const style=getComputedStyle(node), rect=node.getBoundingClientRect();
		return style.display!=='none' && style.visibility!=='hidden' && rect.width>0 && rect.height>0 &&
			(!node.checkVisibility || node.checkVisibility({checkOpacity:true,checkVisibilityCSS:true}));
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
	const touchControls=[...document.querySelectorAll('button,a.button,.customer-name-link,summary,[role=button],.mobile-bottom-nav a,.nav-menu__panel a,[data-route-navigation],[data-route-call]')].filter(visible);
	const checkboxLabels=[...document.querySelectorAll('input[type=checkbox],input[type=radio]')].filter(visible).map(input => input.closest('label') || (input.id && document.querySelector('label[for="'+CSS.escape(input.id)+'"]'))).filter(Boolean);
	const ids=[...document.querySelectorAll('[id]')].map(node=>node.id).filter(Boolean);
	const duplicateIDs=[...new Set(ids.filter((id,index)=>ids.indexOf(id)!==index))];
	const badSelects=[...document.querySelectorAll('select')].filter(visible).filter(node=>{
		const style=getComputedStyle(node), color=style.backgroundColor.replace(/\s/g,'');
		return !style.colorScheme.includes('light') || color==='rgb(0,0,0)' || color==='rgba(0,0,0,1)';
	}).map(describe);
	const narrowDateInputs=innerWidth<=1050?[...document.querySelectorAll('input[type="date"]')].filter(visible).filter(node=>node.getBoundingClientRect().width<120).map(describe):[];
	const misalignedAdminActions=innerWidth<=1050?[...document.querySelectorAll('.notifications-page .notification-actions')].filter(visible).flatMap(group=>{
		const groupRect=group.getBoundingClientRect();
		return [...group.querySelectorAll('.button')].filter(visible).filter(node=>{
			const rect=node.getBoundingClientRect();
			return Math.abs(rect.left-groupRect.left)>1||Math.abs(rect.right-groupRect.right)>1;
		}).map(describe);
	}):[];
	const mobileNavigation=document.querySelector('.mobile-bottom-nav');
	const mobileNavigationVisible=Boolean(mobileNavigation&&visible(mobileNavigation));
	const mobileCapture=document.querySelector('.mobile-primary-action');
	const mobileCaptureVisible=Boolean(mobileCapture&&visible(mobileCapture));
	const overlaps=(first,second)=>first.left<second.right&&first.right>second.left&&first.top<second.bottom&&first.bottom>second.top;
	const violatesNavigationClearance=(node,navigation)=>node.left<navigation.right&&node.right>navigation.left&&node.top<navigation.bottom&&node.bottom>navigation.top-7.5;
	const rectOf=node=>{const rect=node.getBoundingClientRect();return {left:rect.left,right:rect.right,top:rect.top,bottom:rect.bottom,width:rect.width,height:rect.height}};
	const stickyControls=[...document.querySelectorAll('.planning-selection,form[data-sticky-actions] > .form-actions,form[data-sticky-actions] > * > .form-actions:last-child,.route-sticky-navigation')].filter(visible);
	const overflowElements=[...document.querySelectorAll('body *')].filter(visible).filter(node=>{const rect=node.getBoundingClientRect();return rect.left < -1 || rect.right > innerWidth + 1}).slice(0,20).map(node=>{const rect=node.getBoundingClientRect();return describe(node)+'@'+Math.round(rect.left)+'..'+Math.round(rect.right)});
	const overflowContainers=[...document.querySelectorAll('body *')].filter(visible).filter(node=>node.scrollWidth > node.clientWidth + 1).slice(0,20).map(node=>describe(node)+'@'+node.clientWidth+'..'+node.scrollWidth+'/'+getComputedStyle(node).overflowX);
	const appBar=document.querySelector('.mobile-app-bar');
	const densityFailures=[];
	const tooTall=(selector,max)=>{const node=document.querySelector(selector);if(node&&visible(node)&&node.getBoundingClientRect().height>max)densityFailures.push(selector+'='+Math.round(node.getBoundingClientRect().height)+'>'+max)};
	if(innerWidth<=520){
		tooTall('.calendar-page .page-heading',145);
		tooTall('.calendar-page .calendar-quick-controls',175);
		tooTall('.waitlist-page .page-heading',145);
		tooTall('.waitlist-page .customer-list-toolbar',250);
		tooTall('.customer-page .page-heading',145);
		tooTall('.customer-page .customer-list-toolbar',190);
		tooTall('.waitlist-page .selection-toolbar',112);
		tooTall('.compact-filter-panel:not([open])',52);
		tooTall('.admin-tour__date-nav',72);
		const calendar=document.querySelector('.calendar-page [data-calendar]');
		if(calendar&&visible(calendar)&&calendar.getBoundingClientRect().top>640)densityFailures.push('[data-calendar].top='+Math.round(calendar.getBoundingClientRect().top)+'>640');
		for(const card of document.querySelectorAll('.dashboard-metrics .metric-card')){
			const value=card.querySelector('strong');if(!value)continue;const cardRect=card.getBoundingClientRect(),valueRect=value.getBoundingClientRect();
			if(value.scrollWidth>value.clientWidth+.5||valueRect.left<cardRect.left-.5||valueRect.right>cardRect.right+.5)densityFailures.push('metric-value-clipped');
		}
	}
	const geometry=node=>{const rect=node.getBoundingClientRect(),style=getComputedStyle(node),details=node.closest('details');return describe(node)+'@'+Math.round(rect.top)+'-'+Math.round(rect.bottom)+' position='+style.position+' bottom='+style.bottom+' details='+(details?details.className+'/'+details.open:'none')};
	const mobileCaptureOverlaps=[];
	const mobileStickyOverlaps=[];
	if (mobileNavigationVisible) {
		for (const node of stickyControls) {
			node.scrollIntoView({block:'end'});
			const rect=rectOf(node);
			const mobileCaptureRect=mobileCaptureVisible?rectOf(mobileCapture):null;
			const navigationRect=rectOf(mobileNavigation);
			if (mobileCaptureRect&&overlaps(mobileCaptureRect,rect)) mobileCaptureOverlaps.push(geometry(node));
			if (violatesNavigationClearance(rect,navigationRect)) mobileStickyOverlaps.push(geometry(node));
		}
		window.scrollTo(0,0);
	}
	return {
		Path:location.pathname, TitleMissing:document.title.trim()==='', ErrorPage:Boolean(document.querySelector('.error-page')),
		OverflowElements:overflowElements,
		OverflowContainers:overflowContainers, ViewportWidth:innerWidth, RootScrollWidth:document.documentElement.scrollWidth, BodyScrollWidth:document.body.scrollWidth,
		Overflow:document.documentElement.scrollWidth>window.innerWidth+1,
		MissingLandmarks:!document.querySelector('.skip-link')||!document.querySelector('header')||!document.querySelector('main')||!document.querySelector('footer')||document.documentElement.lang!=='de-AT',
		H1Count:document.querySelectorAll('main h1').length, DuplicateIDs:duplicateIDs,
		MissingLabels:controls.filter(node=>!labelled(node)).map(describe),
		SmallControls:innerWidth<=1050 ? touchControls.filter(node=>{const rect=node.getBoundingClientRect();return rect.width<43.5||rect.height<43.5}).map(node=>{const rect=node.getBoundingClientRect();return describe(node)+'@'+Math.round(rect.width)+'x'+Math.round(rect.height)+'.'+node.className}) : [],
		SmallCheckboxLabels:innerWidth<=1050 ? checkboxLabels.filter(node=>node.getBoundingClientRect().height<43.5).map(describe) : [],
		NarrowDateInputs:narrowDateInputs,
		MisalignedAdminActions:misalignedAdminActions,
		BadSelects:badSelects,
		HiddenSortControls:[...document.querySelectorAll('.list-sort-controls')].filter(node=>!visible(node)).map(describe),
		MobileCaptureOverlaps:mobileCaptureOverlaps,
		MobileStickyOverlaps:mobileStickyOverlaps,
		MobileWorkflowNavMissing:innerWidth<=1050&&!mobileNavigationVisible&&stickyControls.length>0,
		MobileAppTitlePresent:Boolean(appBar?.querySelector('.mobile-app-bar__title')),
		MobileAppBarHeight:appBar&&visible(appBar)?appBar.getBoundingClientRect().height:0,
		DensityFailures:densityFailures,
		CalendarAssetCount:[...document.querySelectorAll('link[href],script[src]')].filter(node=>(node.href||node.src).includes('fullcalendar')).length
	};
})()`
