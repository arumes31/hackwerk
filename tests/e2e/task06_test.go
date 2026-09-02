//go:build e2e

package e2e_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/dashboard"
	"example.invalid/hackplan/internal/web"
	"github.com/chromedp/chromedp"
)

func TestTask06ResponsiveDashboardForAdminAndDriver(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, driverID, chipperID, jobID, _, adminPassword, driverPassword := task04Application(t, databaseURL)
	admin := auth.Actor{Role: auth.RoleAdmin, DisplayName: "Anna Admin"}
	if err := pool.QueryRow(t.Context(), "SELECT id::text FROM users WHERE username='admin-task04'").Scan(&admin.UserID); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC)
	draft, err := appointments.CreateDraftFromWaitlist(t.Context(), admin, appointment.CreateDraftInput{
		JobID: jobID, RequestID: "task06-draft", Time: appointment.TimeInput{StartsAt: start, EndsAt: start.Add(3 * time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := appointments.AssignDriversAndResources(t.Context(), admin, appointment.AssignInput{
		MutateInput: appointment.MutateInput{ID: draft.ID, ExpectedVersion: draft.Version, RequestID: "task06-assign"},
		Assignments: appointment.AssignmentInput{DriverIDs: []string{driverID}, PrimaryDriverID: driverID, Resources: []appointment.ResourceAssignment{{ID: chipperID, Purpose: appointment.PurposeChipping}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appointments.ProposeAppointment(t.Context(), admin, appointment.MutateInput{ID: assigned.ID, ExpectedVersion: assigned.Version, RequestID: "task06-propose"}, ""); err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	dashboardService, err := dashboard.New(postgres.NewDashboardStore(pool), dashboard.Config{
		Location: location, HorizonDays: 14, PendingAfter: 15 * time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00",
	}, func() time.Time { return time.Date(2026, 8, 25, 8, 30, 0, 0, location) })
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewUnstartedServer(nil)
	cfg := config.Config{
		AppName: "HackWerk", BaseURL: "http://" + server.Listener.Addr().String(), Database: config.Database{ReadinessTimeout: 2 * time.Second},
		Auth: config.Auth{SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf", SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour},
	}
	router, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"},
		Identity: identity, Drivers: drivers, Resources: resources, Appointments: appointments, Dashboard: dashboardService,
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = router
	server.Start()
	t.Cleanup(server.Close)

	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(1280, 900))
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 180*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browserContext) })

	var adminText string
	if err := chromedp.Run(browserContext,
		chromedp.Navigate(server.URL+"/login"), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
		chromedp.SetValue("#username", "admin-task04", chromedp.ByQuery), chromedp.SetValue("#password", adminPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery),
		chromedp.EmulateViewport(1280, 720),
		chromedp.Navigate(server.URL+"/dashboard?date=2026-08-25"), chromedp.WaitVisible(".dashboard-appointment", chromedp.ByQuery),
		chromedp.Text("main", &adminText, chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	for _, wanted := range []string{"Franz Huber", "Hackmaschine 1", "Fahrer heute", "Freie Hackkapazität", "Dringende Aufträge"} {
		if !strings.Contains(adminText, wanted) {
			t.Fatalf("admin dashboard missing %q: %s", wanted, adminText)
		}
	}
	var compactLayoutAudit struct {
		ButtonRows, ControlRows, SmallTargets int
		ControlsHeight, IntroButtonGap        float64
		ControlSectionGap, ControlMetricGap   float64
		Overlap                               bool
	}
	if err := chromedp.Run(browserContext, chromedp.Evaluate(`(() => {
		const intro=document.querySelector('.dashboard-intro');
		const introButtons=[...intro.querySelectorAll('.button')].map(node=>node.getBoundingClientRect());
		const controls=document.querySelector('.dashboard-control-bar');
		const controlSections=[...controls.children].map(node=>node.getBoundingClientRect());
		const controlButtons=[...controls.querySelectorAll('.button')].map(node=>node.getBoundingClientRect());
		const metrics=document.querySelector('.dashboard-metrics').getBoundingClientRect();
		const controlBottom=Math.max(...controlSections.map(rect=>rect.bottom));
		return {
			ButtonRows:new Set(introButtons.map(rect=>Math.round(rect.top))).size,
			ControlRows:new Set(controlSections.map(rect=>Math.round(rect.top))).size,
			SmallTargets:controlButtons.filter(rect=>rect.width<44||rect.height<44).length,
			ControlsHeight:controls.getBoundingClientRect().height,
			IntroButtonGap:introButtons[1].left-introButtons[0].right,
			ControlSectionGap:controlSections[1].left-controlSections[0].right,
			ControlMetricGap:metrics.top-controlBottom,
			Overlap:controlSections.some(rect=>rect.bottom>metrics.top||rect.right>window.innerWidth)
		};
	})()`, &compactLayoutAudit)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if compactLayoutAudit.ButtonRows != 1 || compactLayoutAudit.ControlRows != 1 || compactLayoutAudit.SmallTargets != 0 ||
		compactLayoutAudit.ControlsHeight > 64 || compactLayoutAudit.IntroButtonGap < 8 || compactLayoutAudit.ControlSectionGap < 8 ||
		compactLayoutAudit.ControlMetricGap < 8 || compactLayoutAudit.Overlap {
		t.Fatalf("compact dashboard layout audit = %+v", compactLayoutAudit)
	}
	var noteHref string
	if err := chromedp.Run(browserContext, chromedp.AttributeValue(".dashboard-actions a[href*='#notes-']", "href", &noteHref, nil, chromedp.ByQuery)); err != nil || !strings.Contains(noteHref, "/customers/") {
		t.Fatalf("dashboard note action = %q: %s", noteHref, browserDiagnostics(browserContext, err))
	}
	var commandMenuAudit struct {
		OutsideViewport, RawLink, SmallTarget, MissingAction bool
		QuickMenuCount, SectionCount                         int
		Columns, AccessibleName, Title                       string
		TriggerWidth, TriggerHeight                          float64
	}
	var presentationToggleAudit struct {
		Comfortable, Outdoor, DensityPressed, OutdoorPressed bool
		DensityStored, OutdoorStored                         string
	}
	if err := chromedp.Run(browserContext,
		chromedp.Click("[data-command-open]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-command-palette]", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const trigger=document.querySelector('[data-command-open]');
			const palette=document.querySelector('[data-command-palette]');
			const rect=palette.getBoundingClientRect();
			const actions=[...palette.querySelectorAll('[data-command-actions] a,[data-command-actions] button')];
			const hrefs=[...palette.querySelectorAll('[data-command-actions] a')].map(node=>node.getAttribute('href'));
			const expected=['/customers/new','/voice','/calendar','/planning/routes','/dashboard','/waitlist?incomplete=1','/planning','/admin/notifications'];
			const triggerRect=trigger.getBoundingClientRect();
			return {OutsideViewport:rect.left<0||rect.right>window.innerWidth,
				RawLink:actions.some(node=>getComputedStyle(node).textDecorationLine!=='none'),
				SmallTarget:actions.some(node=>node.getBoundingClientRect().height<44),
				MissingAction:expected.some(href=>!hrefs.includes(href)),
				QuickMenuCount:document.querySelectorAll('[data-quick-menu],.quick-menu').length,
				SectionCount:palette.querySelectorAll('.command-section').length,
				Columns:getComputedStyle(palette.querySelector('.command-grid')).gridTemplateColumns,
				AccessibleName:trigger.getAttribute('aria-label')||'',Title:trigger.getAttribute('title')||'',
				TriggerWidth:triggerRect.width,TriggerHeight:triggerRect.height};
		})()`, &commandMenuAudit),
		chromedp.Click("[data-command-palette] [data-density-toggle]", chromedp.ByQuery),
		chromedp.Click("[data-command-palette] [data-outdoor-toggle]", chromedp.ByQuery),
		chromedp.Evaluate(`(() => ({
			Comfortable:document.documentElement.classList.contains('density-comfortable'),
			Outdoor:document.documentElement.classList.contains('outdoor-contrast'),
			DensityPressed:document.querySelector('[data-command-palette] [data-density-toggle]').getAttribute('aria-pressed')==='true',
			OutdoorPressed:document.querySelector('[data-command-palette] [data-outdoor-toggle]').getAttribute('aria-pressed')==='true',
			DensityStored:localStorage.getItem('hackwerk:density')||'',
			OutdoorStored:localStorage.getItem('hackwerk:outdoor')||''
		}))()`, &presentationToggleAudit),
		chromedp.Click("[data-command-palette] [data-density-toggle]", chromedp.ByQuery),
		chromedp.Click("[data-command-palette] [data-outdoor-toggle]", chromedp.ByQuery),
		chromedp.Click("[data-command-close]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-command-palette]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if commandMenuAudit.OutsideViewport || commandMenuAudit.RawLink || commandMenuAudit.SmallTarget || commandMenuAudit.MissingAction ||
		commandMenuAudit.QuickMenuCount != 0 || commandMenuAudit.SectionCount != 3 ||
		commandMenuAudit.AccessibleName != "Globale Suche und Kommandos öffnen" || commandMenuAudit.Title != "Kommandos (Strg/⌘ K)" ||
		commandMenuAudit.TriggerWidth < 44 || commandMenuAudit.TriggerHeight < 44 || !strings.Contains(commandMenuAudit.Columns, " ") {
		t.Fatalf("command menu CSS audit = %+v", commandMenuAudit)
	}
	if !presentationToggleAudit.Comfortable || !presentationToggleAudit.Outdoor || !presentationToggleAudit.DensityPressed ||
		!presentationToggleAudit.OutdoorPressed || presentationToggleAudit.DensityStored != "comfortable" || presentationToggleAudit.OutdoorStored != "true" {
		t.Fatalf("presentation toggle audit = %+v", presentationToggleAudit)
	}
	var adminMenuAudit struct {
		VisibleText           bool
		AccessibleName, Title string
		IconCount             int
		Width, Height         float64
	}
	if err := chromedp.Run(browserContext, chromedp.Evaluate(`(() => {
		const trigger=document.querySelector('[data-admin-menu] summary');
		const rect=trigger.getBoundingClientRect();
		return {VisibleText:[...trigger.childNodes].some(node=>node.nodeType===Node.TEXT_NODE&&node.textContent.trim()!==''),
			AccessibleName:trigger.getAttribute('aria-label')||'',Title:trigger.getAttribute('title')||'',
			IconCount:trigger.querySelectorAll('svg.nav-menu__icon[aria-hidden=true]').length,Width:rect.width,Height:rect.height};
	})()`, &adminMenuAudit)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if adminMenuAudit.VisibleText || adminMenuAudit.AccessibleName != "Verwaltung" || adminMenuAudit.Title != "Verwaltung" ||
		adminMenuAudit.IconCount != 1 || adminMenuAudit.Width < 44 || adminMenuAudit.Height < 44 {
		t.Fatalf("admin menu icon audit = %+v", adminMenuAudit)
	}
	metricAudit := func(width, height int64) struct {
		BodyOverflow, RailOverflow bool
		Count, Rows, SmallTargets  int
		VisibleCards, IconCount    int
		MaxCardHeight              float64
	} {
		t.Helper()
		var audit struct {
			BodyOverflow, RailOverflow bool
			Count, Rows, SmallTargets  int
			VisibleCards, IconCount    int
			MaxCardHeight              float64
		}
		if err := chromedp.Run(browserContext,
			chromedp.EmulateViewport(width, height),
			chromedp.Evaluate(`(() => {
				const rail=document.querySelector('.dashboard-metrics');
				const cards=[...rail.querySelectorAll('.metric-card')];
				const railRect=rail.getBoundingClientRect();
				const rects=cards.map(card=>card.getBoundingClientRect());
				return {BodyOverflow:document.documentElement.scrollWidth>window.innerWidth,
					RailOverflow:rail.scrollWidth>rail.clientWidth+1,Count:cards.length,
					Rows:new Set(rects.map(rect=>Math.round(rect.top))).size,
					SmallTargets:rects.filter(rect=>rect.height<44).length,
					VisibleCards:rects.filter(rect=>rect.right>railRect.left&&rect.left<railRect.right).length,
					IconCount:rail.querySelectorAll('.metric-card > span[aria-hidden="true"]').length,
					MaxCardHeight:Math.max(...rects.map(rect=>rect.height))};
			})()`, &audit),
		); err != nil {
			t.Fatal(browserDiagnostics(browserContext, err))
		}
		return audit
	}
	for _, viewport := range []struct {
		name          string
		width, height int64
	}{{"720p", 1280, 720}, {"1080p", 1920, 1080}} {
		audit := metricAudit(viewport.width, viewport.height)
		if audit.BodyOverflow || audit.RailOverflow || audit.Count != 8 || audit.Rows != 1 || audit.SmallTargets != 0 ||
			audit.VisibleCards != 8 || audit.IconCount != 0 || audit.MaxCardHeight > 64 {
			t.Fatalf("%s dashboard metric audit = %+v", viewport.name, audit)
		}
	}

	var tabletAudit struct {
		Overflow, PrimaryVisible, BottomHidden bool
	}
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(768, 1024), chromedp.WaitVisible(".mobile-bottom-nav", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const visible=node=>{const style=getComputedStyle(node),rect=node.getBoundingClientRect();return style.display!=='none'&&rect.width>0&&rect.height>0};
			return {Overflow:document.documentElement.scrollWidth>window.innerWidth,
				PrimaryVisible:visible(document.querySelector('.primary-nav')),
				BottomHidden:!visible(document.querySelector('.mobile-bottom-nav'))};
		})()`, &tabletAudit),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if tabletAudit.Overflow || tabletAudit.PrimaryVisible || tabletAudit.BottomHidden {
		t.Fatalf("tablet shell CSS audit = %+v", tabletAudit)
	}

	var mobileAudit struct {
		Overflow, MenuOpen, FocusReturned, AppBarVisible bool
		AppBarTitlePresent                               bool
		H1Count, SmallTargets, TabCount                  int
		MissingNames, UnlabelledFields                   int
		MissingLandmarks                                 bool
		CaptureVisible, CaptureInsideNavigation          bool
		CaptureName, CaptureHref                         string
		CaptureWidth, CaptureHeight, AppBarHeight        float64
	}
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(360, 800), chromedp.WaitVisible(".mobile-bottom-nav", chromedp.ByQuery),
		chromedp.Click("[data-mobile-menu-open]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-mobile-menu]", chromedp.ByQuery),
		chromedp.KeyEvent("\x1b"),
		chromedp.Evaluate(`(() => {
			const trigger=document.querySelector('[data-mobile-menu-open]');
			const dialog=document.querySelector('[data-mobile-menu]');
			const capture=document.querySelector('.mobile-primary-action');
			const appBar=document.querySelector('.mobile-app-bar');
			const captureRect=capture?capture.getBoundingClientRect():{width:0,height:0};
			const navigation=document.querySelector('.mobile-bottom-nav');
			const isVisible=node=>{const style=getComputedStyle(node),rect=node.getBoundingClientRect();return style.display!=='none'&&style.visibility!=='hidden'&&rect.width>0&&rect.height>0};
			const touchTargets=[...document.querySelectorAll('.mobile-bottom-nav a,.mobile-bottom-nav button,.mobile-bottom-nav summary')].filter(isVisible);
			const controls=[...document.querySelectorAll('a,button,summary,input:not([type=hidden]),select,textarea')].filter(isVisible);
			const named=node=>((node.getAttribute('aria-label')||node.getAttribute('title')||node.textContent||node.value||'').trim()!==''||
				document.querySelector('label[for="'+CSS.escape(node.id)+'"]'));
			return {Overflow:document.documentElement.scrollWidth>window.innerWidth,MenuOpen:dialog.open,
				FocusReturned:document.activeElement===trigger,H1Count:document.querySelectorAll('main h1').length,
				SmallTargets:touchTargets.filter(node=>{const r=node.getBoundingClientRect();return r.width<44||r.height<44}).length,
				MissingNames:controls.filter(node=>!named(node)).length,
				UnlabelledFields:controls.filter(node=>node.matches('input,select,textarea')&&!named(node)).length,
				CaptureVisible:!!capture&&isVisible(capture),CaptureInsideNavigation:!!capture&&capture.closest('.mobile-bottom-nav')===navigation,
				TabCount:navigation.querySelectorAll('[data-mobile-nav-item]').length,
				CaptureName:capture?(capture.textContent||'').trim():'',CaptureHref:capture?capture.getAttribute('href')||'':'',
				AppBarVisible:!!appBar&&isVisible(appBar),AppBarTitlePresent:Boolean(appBar?.querySelector('.mobile-app-bar__title')),
				CaptureWidth:captureRect.width,CaptureHeight:captureRect.height,AppBarHeight:appBar?.getBoundingClientRect().height||0,
				MissingLandmarks:!document.querySelector('.skip-link')||!document.querySelector('header')||!document.querySelector('main')||!document.querySelector('footer')||document.documentElement.lang!=='de-AT'};
		})()`, &mobileAudit),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if mobileAudit.Overflow || mobileAudit.MenuOpen || !mobileAudit.FocusReturned || mobileAudit.H1Count != 1 || mobileAudit.SmallTargets != 0 ||
		mobileAudit.MissingNames != 0 || mobileAudit.UnlabelledFields != 0 || mobileAudit.MissingLandmarks ||
		!mobileAudit.AppBarVisible || mobileAudit.AppBarTitlePresent || mobileAudit.AppBarHeight > 56 || !mobileAudit.CaptureVisible || mobileAudit.CaptureInsideNavigation || mobileAudit.TabCount != 5 || mobileAudit.CaptureName != "Neuer Auftrag" ||
		mobileAudit.CaptureHref != "/customers/new" || mobileAudit.CaptureWidth < 44 || mobileAudit.CaptureHeight < 44 {
		t.Fatalf("mobile accessibility audit = %+v", mobileAudit)
	}

	var initialSheetFocusInside bool
	var landscapeSheetAudit struct {
		Open, TabFocusInside, FitsViewport bool
		Top, Bottom, NavigationTop         float64
	}
	var backdropAudit struct {
		Closed, FocusReturned bool
	}
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(667, 375),
		chromedp.Click("[data-mobile-menu-open]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-mobile-menu]", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const dialog=document.querySelector('[data-mobile-menu]');
			const focusables=[...dialog.querySelectorAll('a,button,input:not([type="hidden"]),select,textarea')]
				.filter(node=>!node.disabled&&getComputedStyle(node).display!=='none');
			const initiallyInside=dialog.contains(document.activeElement);
			focusables.at(-1)?.focus();
			return initiallyInside;
		})()`, &initialSheetFocusInside),
		chromedp.KeyEvent("\t"),
		chromedp.Evaluate(`(() => {
			const dialog=document.querySelector('[data-mobile-menu]');
			const navigation=document.querySelector('.mobile-bottom-nav');
			const rect=dialog.getBoundingClientRect();
			const navRect=navigation.getBoundingClientRect();
			return {Open:dialog.open,TabFocusInside:dialog.contains(document.activeElement),FitsViewport:rect.top>=0&&rect.bottom<=navRect.top+.5,
				Top:rect.top,Bottom:rect.bottom,NavigationTop:navRect.top};
		})()`, &landscapeSheetAudit),
		chromedp.MouseClickXY(4, 4),
		chromedp.Poll(`!document.querySelector('[data-mobile-menu]').open`, nil),
		chromedp.Evaluate(`(() => ({Closed:!document.querySelector('[data-mobile-menu]').open,
			FocusReturned:document.activeElement===document.querySelector('[data-mobile-menu-open]')}))()`, &backdropAudit),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !initialSheetFocusInside || !landscapeSheetAudit.Open || !landscapeSheetAudit.TabFocusInside || !landscapeSheetAudit.FitsViewport ||
		!backdropAudit.Closed || !backdropAudit.FocusReturned {
		t.Fatalf("mobile landscape More sheet audit = focus=%t sheet=%+v backdrop=%+v", initialSheetFocusInside, landscapeSheetAudit, backdropAudit)
	}

	var narrowActionAudit struct {
		Overflow, WaitlistLabelFits bool
		LabelWidth, ActionWidth     float64
	}
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(320, 700),
		chromedp.Evaluate(`(() => {
			const action=document.querySelector('.mobile-primary-action');
			const label=action.querySelector('span');
			const waitlistLabel=document.querySelector('.mobile-bottom-nav a[href="/waitlist"] .mobile-nav-item__label');
			return {Overflow:document.documentElement.scrollWidth>window.innerWidth,
				WaitlistLabelFits:waitlistLabel.scrollWidth<=waitlistLabel.clientWidth+.5,
				LabelWidth:label.getBoundingClientRect().width,ActionWidth:action.getBoundingClientRect().width};
		})()`, &narrowActionAudit),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if narrowActionAudit.Overflow || !narrowActionAudit.WaitlistLabelFits || narrowActionAudit.LabelWidth <= 1 || narrowActionAudit.ActionWidth < 44 {
		t.Fatalf("320px primary mobile action audit = %+v", narrowActionAudit)
	}
	mobileMetrics := metricAudit(360, 800)
	if mobileMetrics.BodyOverflow || !mobileMetrics.RailOverflow || mobileMetrics.Count != 8 || mobileMetrics.Rows != 1 ||
		mobileMetrics.SmallTargets != 0 || mobileMetrics.VisibleCards < 1 || mobileMetrics.IconCount != 0 || mobileMetrics.MaxCardHeight > 64 {
		t.Fatalf("mobile dashboard metric audit = %+v", mobileMetrics)
	}

	var adminMobileTourAudit struct {
		Overflow, TourVisible, GroupedHidden, DriverTourAbsent                 bool
		Chronological, SpineVisible, ConnectorVisible                          bool
		StatusText, PrimaryFullRow                                             bool
		FirstWorkSurface, IntroHidden, DesktopControlsHidden, TourAboveMetrics bool
		AccessibleH1                                                           bool
		FirstAppointmentInViewport, BottomNavigationVisible                    bool
		AdminNavigationAbsent                                                  bool
		AppointmentCount, SmallTargets                                         int
		CalendarHref, DetailHref                                               string
	}
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(459, 790),
		chromedp.WaitVisible(`[data-dashboard-projection="admin-tour"]`, chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const visible=node=>{if(!node)return false;const style=getComputedStyle(node),rect=node.getBoundingClientRect();return style.display!=='none'&&style.visibility!=='hidden'&&rect.width>0&&rect.height>0};
			const tour=document.querySelector('[data-dashboard-projection="admin-tour"]');
			const grouped=document.querySelector('[data-dashboard-projection="resource-groups"]');
			const intro=document.querySelector('.dashboard-intro');
			const desktopControls=document.querySelector('.dashboard-control-bar');
			const metrics=document.querySelector('.dashboard-metrics');
			const main=document.querySelector('main.dashboard-page');
			const list=tour.querySelector('.admin-tour__list');
			const items=[...tour.querySelectorAll('[data-admin-tour-appointment]')];
			const controls=[...tour.querySelectorAll('a,button,summary')].filter(visible);
			const starts=items.map(item=>Date.parse(item.querySelector('time').dateTime));
			const actions=tour.querySelector('.admin-tour__actions');
			const primary=tour.querySelector('.admin-tour__appointment-link');
			const actionRect=actions.getBoundingClientRect(),primaryRect=primary.getBoundingClientRect();
			const firstVisibleChild=[...main.children].find(visible);
			const firstAppointmentRect=items[0]?.getBoundingClientRect();
			const bottomNavigationRect=document.querySelector('.mobile-bottom-nav').getBoundingClientRect();
			const pageHeading=main.querySelector('h1');
			return {Overflow:document.documentElement.scrollWidth>window.innerWidth,
				TourVisible:visible(tour),GroupedHidden:!visible(grouped),DriverTourAbsent:!document.querySelector('[data-dashboard-projection="driver-tour"]'),
				Chronological:starts.every((value,index)=>index===0||starts[index-1]<=value),
				SpineVisible:getComputedStyle(list,'::before').content!=='none'&&parseFloat(getComputedStyle(list,'::before').width)>=6,
				ConnectorVisible:items.every(item=>getComputedStyle(item,'::before').content!=='none'&&parseFloat(getComputedStyle(item,'::before').width)>=12),
				StatusText:items.every(item=>(item.querySelector('.appointment-badge')?.textContent||'').trim()!==''),PrimaryFullRow:Math.abs(primaryRect.left-actionRect.left)<1&&Math.abs(primaryRect.right-actionRect.right)<1,
				FirstWorkSurface:firstVisibleChild===tour,IntroHidden:!visible(intro),DesktopControlsHidden:!visible(desktopControls),TourAboveMetrics:tour.getBoundingClientRect().top<metrics.getBoundingClientRect().top,
				AccessibleH1:Boolean(pageHeading&&getComputedStyle(pageHeading).display!=='none'&&pageHeading.getClientRects().length),
				FirstAppointmentInViewport:Boolean(firstAppointmentRect&&firstAppointmentRect.top<bottomNavigationRect.top),BottomNavigationVisible:visible(document.querySelector('.mobile-bottom-nav')),
				AdminNavigationAbsent:!document.querySelector('.mobile-admin-nav'),
				AppointmentCount:items.length,SmallTargets:controls.filter(node=>{const rect=node.getBoundingClientRect();return rect.width<44||rect.height<44}).length,
				CalendarHref:tour.querySelector('.admin-tour__calendar')?.getAttribute('href')||'',DetailHref:tour.querySelector('.admin-tour__appointment-link')?.getAttribute('href')||''};
		})()`, &adminMobileTourAudit),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if adminMobileTourAudit.Overflow || !adminMobileTourAudit.TourVisible || !adminMobileTourAudit.GroupedHidden || !adminMobileTourAudit.DriverTourAbsent ||
		!adminMobileTourAudit.Chronological || !adminMobileTourAudit.SpineVisible || !adminMobileTourAudit.ConnectorVisible ||
		!adminMobileTourAudit.StatusText || !adminMobileTourAudit.PrimaryFullRow || adminMobileTourAudit.AppointmentCount != 1 || adminMobileTourAudit.SmallTargets != 0 ||
		!adminMobileTourAudit.FirstWorkSurface || !adminMobileTourAudit.IntroHidden || !adminMobileTourAudit.DesktopControlsHidden || !adminMobileTourAudit.TourAboveMetrics ||
		!adminMobileTourAudit.AccessibleH1 ||
		!adminMobileTourAudit.FirstAppointmentInViewport || !adminMobileTourAudit.BottomNavigationVisible || !adminMobileTourAudit.AdminNavigationAbsent ||
		adminMobileTourAudit.CalendarHref != "/calendar?date=2026-08-25" || !strings.HasPrefix(adminMobileTourAudit.DetailHref, "/calendar/appointments/") {
		t.Fatalf("admin mobile Touren-Kamm audit = %+v", adminMobileTourAudit)
	}

	var adminDesktopProjectionAudit struct {
		TourHidden, GroupedVisible, AdminNavigationAbsent bool
		AppointmentColumns                                string
	}
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(1280, 720),
		chromedp.Evaluate(`(() => {
			const visible=node=>{const style=getComputedStyle(node),rect=node.getBoundingClientRect();return style.display!=='none'&&style.visibility!=='hidden'&&rect.width>0&&rect.height>0};
			const tour=document.querySelector('[data-dashboard-projection="admin-tour"]');
			const grouped=document.querySelector('[data-dashboard-projection="resource-groups"]');
			return {TourHidden:!visible(tour),GroupedVisible:visible(grouped),AdminNavigationAbsent:!document.querySelector('.mobile-admin-nav'),
				AppointmentColumns:getComputedStyle(grouped.querySelector('.dashboard-appointment')).gridTemplateColumns};
		})()`, &adminDesktopProjectionAudit),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !adminDesktopProjectionAudit.TourHidden || !adminDesktopProjectionAudit.GroupedVisible || !adminDesktopProjectionAudit.AdminNavigationAbsent || !strings.Contains(adminDesktopProjectionAudit.AppointmentColumns, " ") {
		t.Fatalf("admin desktop projection changed = %+v", adminDesktopProjectionAudit)
	}

	var mobileDashboardScreenshot, desktopDashboardScreenshot []byte
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(459, 790),
		chromedp.FullScreenshot(&mobileDashboardScreenshot, 90),
		chromedp.EmulateViewport(1280, 720),
		chromedp.FullScreenshot(&desktopDashboardScreenshot, 90),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	for name, screenshot := range map[string][]byte{
		"task06-mobile-dashboard.png":  mobileDashboardScreenshot,
		"task06-desktop-dashboard.png": desktopDashboardScreenshot,
	} {
		artifact := filepath.Join(t.ArtifactDir(), name)
		if err := os.WriteFile(artifact, screenshot, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("dashboard screenshot: %s", artifact)
		if screenshotDir := os.Getenv("E2E_SCREENSHOT_DIR"); screenshotDir != "" {
			if err := os.MkdirAll(screenshotDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(screenshotDir, name), screenshot, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}

	var driverText string
	if err := chromedp.Run(browserContext,
		chromedp.Evaluate(`document.querySelector("header form[action='/logout']").requestSubmit()`, nil),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
		chromedp.SetValue("#username", "driver-task04", chromedp.ByQuery), chromedp.SetValue("#password", driverPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery),
		chromedp.Navigate(server.URL+"/dashboard?date=2026-08-25"), chromedp.WaitVisible(".dashboard-appointment", chromedp.ByQuery),
		chromedp.Text("main", &driverText, chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !strings.Contains(driverText, "Franz Huber") || !strings.Contains(driverText, "Meine Verfügbarkeit") {
		t.Fatalf("driver did not receive shared appointment/own availability: %s", driverText)
	}
	for _, forbidden := range []string{"Versandprobleme", "Overrides", "Freie Hackkapazität", "Dringende Aufträge", "Fahrer heute"} {
		if strings.Contains(driverText, forbidden) {
			t.Fatalf("driver dashboard exposed admin-only %q: %s", forbidden, driverText)
		}
	}

	var driverMobileTourAudit struct {
		Overflow, TourVisible, GroupedHidden, Chronological bool
		SpineVisible, ConnectorVisible, StatusText          bool
		AppointmentCount, SmallTargets                      int
		RouteHref                                           string
		ScrollWidth, InnerWidth                             float64
		Overflowing                                         string
	}
	var driverTourScreenshot []byte
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(360, 800),
		chromedp.WaitVisible(`[data-dashboard-projection="driver-tour"]`, chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const visible=node=>{const style=getComputedStyle(node),rect=node.getBoundingClientRect();return style.display!=='none'&&style.visibility!=='hidden'&&rect.width>0&&rect.height>0};
			const tour=document.querySelector('[data-dashboard-projection="driver-tour"]');
			const grouped=document.querySelector('[data-dashboard-projection="resource-groups"]');
			const list=tour.querySelector('.driver-tour__list');
			const items=[...tour.querySelectorAll('[data-driver-tour-appointment]')];
			const controls=[...tour.querySelectorAll('a,button,summary')].filter(visible);
			const starts=items.map(item=>Date.parse(item.querySelector('time').dateTime));
			const overflowing=[...document.body.querySelectorAll('*')].filter(visible).filter(node=>{const rect=node.getBoundingClientRect();return rect.left<-.5||rect.right>window.innerWidth+.5}).slice(0,8).map(node=>node.tagName.toLowerCase()+'.'+String(node.className)+':'+node.getBoundingClientRect().left.toFixed(1)+'..'+node.getBoundingClientRect().right.toFixed(1));
			return {Overflow:document.documentElement.scrollWidth>window.innerWidth,ScrollWidth:document.documentElement.scrollWidth,InnerWidth:window.innerWidth,Overflowing:overflowing.join('|'),
				TourVisible:visible(tour),GroupedHidden:!visible(grouped),
				Chronological:starts.every((value,index)=>index===0||starts[index-1]<=value),
				SpineVisible:getComputedStyle(list,'::before').content!=='none'&&parseFloat(getComputedStyle(list,'::before').width)>=6,
				ConnectorVisible:items.every(item=>getComputedStyle(item,'::before').content!=='none'&&parseFloat(getComputedStyle(item,'::before').width)>=12),
				StatusText:items.every(item=>(item.querySelector('.appointment-badge')?.textContent||'').trim()!==''),
				AppointmentCount:items.length,SmallTargets:controls.filter(node=>{const rect=node.getBoundingClientRect();return rect.width<44||rect.height<44}).length,
				RouteHref:tour.querySelector('.driver-tour__route')?.getAttribute('href')||''};
		})()`, &driverMobileTourAudit),
		chromedp.FullScreenshot(&driverTourScreenshot, 90),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if driverMobileTourAudit.Overflow || !driverMobileTourAudit.TourVisible || !driverMobileTourAudit.GroupedHidden ||
		!driverMobileTourAudit.Chronological || !driverMobileTourAudit.SpineVisible || !driverMobileTourAudit.ConnectorVisible ||
		!driverMobileTourAudit.StatusText || driverMobileTourAudit.AppointmentCount != 1 || driverMobileTourAudit.SmallTargets != 0 || driverMobileTourAudit.RouteHref != "/my-route?date=2026-08-25" {
		t.Fatalf("driver mobile Touren-Kamm audit = %+v", driverMobileTourAudit)
	}

	var driverDesktopProjectionAudit struct {
		TourHidden, GroupedVisible bool
		AppointmentColumns         string
	}
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(1280, 720),
		chromedp.Evaluate(`(() => {
			const visible=node=>{const style=getComputedStyle(node),rect=node.getBoundingClientRect();return style.display!=='none'&&style.visibility!=='hidden'&&rect.width>0&&rect.height>0};
			const tour=document.querySelector('[data-dashboard-projection="driver-tour"]');
			const grouped=document.querySelector('[data-dashboard-projection="resource-groups"]');
			return {TourHidden:!visible(tour),GroupedVisible:visible(grouped),
				AppointmentColumns:getComputedStyle(grouped.querySelector('.dashboard-appointment')).gridTemplateColumns};
		})()`, &driverDesktopProjectionAudit),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !driverDesktopProjectionAudit.TourHidden || !driverDesktopProjectionAudit.GroupedVisible || !strings.Contains(driverDesktopProjectionAudit.AppointmentColumns, " ") {
		t.Fatalf("driver desktop projection changed = %+v", driverDesktopProjectionAudit)
	}

	driverTourArtifact := filepath.Join(t.ArtifactDir(), "task06-driver-tour-mobile.png")
	if err := os.WriteFile(driverTourArtifact, driverTourScreenshot, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("driver Touren-Kamm screenshot: %s", driverTourArtifact)
	if screenshotDir := os.Getenv("E2E_SCREENSHOT_DIR"); screenshotDir != "" {
		if err := os.MkdirAll(screenshotDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(screenshotDir, "task06-driver-tour-mobile.png"), driverTourScreenshot, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
