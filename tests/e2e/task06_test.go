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
	var noteHref string
	if err := chromedp.Run(browserContext, chromedp.AttributeValue(".dashboard-actions a[href*='#notes-']", "href", &noteHref, nil, chromedp.ByQuery)); err != nil || !strings.Contains(noteHref, "/customers/") {
		t.Fatalf("dashboard note action = %q: %s", noteHref, browserDiagnostics(browserContext, err))
	}
	var quickMenuAudit struct {
		OutsideViewport, RawLink, SmallTarget, VisibleText bool
		Columns, AccessibleName, Title                     string
		IconCount                                          int
	}
	if err := chromedp.Run(browserContext,
		chromedp.Click("[data-quick-menu] summary", chromedp.ByQuery),
		chromedp.WaitVisible("[data-quick-menu] .nav-menu__panel", chromedp.ByQuery),
		chromedp.Evaluate(`(() => {
			const trigger=document.querySelector('[data-quick-menu] summary');
			const panel=document.querySelector('[data-quick-menu] .nav-menu__panel');
			const rect=panel.getBoundingClientRect();
			const actions=[...panel.querySelectorAll(':scope > a,:scope > button')];
			return {OutsideViewport:rect.left<0||rect.right>window.innerWidth,
				RawLink:actions.some(node=>getComputedStyle(node).textDecorationLine!=='none'),
				SmallTarget:actions.some(node=>node.getBoundingClientRect().height<44),
				VisibleText:[...trigger.childNodes].some(node=>node.nodeType===Node.TEXT_NODE&&node.textContent.trim()!==''),
				Columns:getComputedStyle(panel).gridTemplateColumns,
				AccessibleName:trigger.getAttribute('aria-label')||'',Title:trigger.getAttribute('title')||'',
				IconCount:trigger.querySelectorAll('svg.nav-menu__icon[aria-hidden=true]').length};
		})()`, &quickMenuAudit),
		chromedp.Click("[data-quick-menu] summary", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if quickMenuAudit.OutsideViewport || quickMenuAudit.RawLink || quickMenuAudit.SmallTarget || quickMenuAudit.VisibleText ||
		quickMenuAudit.AccessibleName != "Schnellzugriff" || quickMenuAudit.Title != "Schnellzugriff" || quickMenuAudit.IconCount != 1 ||
		!strings.Contains(quickMenuAudit.Columns, " ") {
		t.Fatalf("quick menu CSS audit = %+v", quickMenuAudit)
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
		MaxCardHeight              float64
	} {
		t.Helper()
		var audit struct {
			BodyOverflow, RailOverflow bool
			Count, Rows, SmallTargets  int
			MaxCardHeight              float64
		}
		if err := chromedp.Run(browserContext,
			chromedp.EmulateViewport(width, height),
			chromedp.Evaluate(`(() => {
				const rail=document.querySelector('.dashboard-metrics');
				const cards=[...rail.querySelectorAll('.metric-card')];
				const rects=cards.map(card=>card.getBoundingClientRect());
				return {BodyOverflow:document.documentElement.scrollWidth>window.innerWidth,
					RailOverflow:rail.scrollWidth>rail.clientWidth+1,Count:cards.length,
					Rows:new Set(rects.map(rect=>Math.round(rect.top))).size,
					SmallTargets:rects.filter(rect=>rect.height<44).length,
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
		if audit.BodyOverflow || audit.RailOverflow || audit.Count != 7 || audit.Rows != 1 || audit.SmallTargets != 0 || audit.MaxCardHeight > 64 {
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
		Overflow, MenuOpen, FocusReturned bool
		H1Count, SmallTargets             int
		MissingNames, UnlabelledFields    int
		MissingLandmarks                  bool
	}
	if err := chromedp.Run(browserContext,
		chromedp.EmulateViewport(360, 800), chromedp.WaitVisible(".mobile-bottom-nav", chromedp.ByQuery),
		chromedp.Click("[data-mobile-menu] summary", chromedp.ByQuery),
		chromedp.KeyEvent("\x1b"),
		chromedp.Evaluate(`(() => {
			const summary=document.querySelector('[data-mobile-menu] summary');
			const isVisible=node=>{const style=getComputedStyle(node),rect=node.getBoundingClientRect();return style.display!=='none'&&style.visibility!=='hidden'&&rect.width>0&&rect.height>0};
			const touchTargets=[...document.querySelectorAll('.mobile-bottom-nav a,.mobile-bottom-nav summary')].filter(isVisible);
			const controls=[...document.querySelectorAll('a,button,summary,input:not([type=hidden]),select,textarea')].filter(isVisible);
			const named=node=>((node.getAttribute('aria-label')||node.getAttribute('title')||node.textContent||node.value||'').trim()!==''||
				document.querySelector('label[for="'+CSS.escape(node.id)+'"]'));
			return {Overflow:document.documentElement.scrollWidth>window.innerWidth,MenuOpen:summary.parentElement.open,
				FocusReturned:document.activeElement===summary,H1Count:document.querySelectorAll('main h1').length,
				SmallTargets:touchTargets.filter(node=>{const r=node.getBoundingClientRect();return r.width<44||r.height<44}).length,
				MissingNames:controls.filter(node=>!named(node)).length,
				UnlabelledFields:controls.filter(node=>node.matches('input,select,textarea')&&!named(node)).length,
				MissingLandmarks:!document.querySelector('.skip-link')||!document.querySelector('header')||!document.querySelector('main')||!document.querySelector('footer')||document.documentElement.lang!=='de-AT'};
		})()`, &mobileAudit),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if mobileAudit.Overflow || mobileAudit.MenuOpen || !mobileAudit.FocusReturned || mobileAudit.H1Count != 1 || mobileAudit.SmallTargets != 0 ||
		mobileAudit.MissingNames != 0 || mobileAudit.UnlabelledFields != 0 || mobileAudit.MissingLandmarks {
		t.Fatalf("mobile accessibility audit = %+v", mobileAudit)
	}
	mobileMetrics := metricAudit(360, 800)
	if mobileMetrics.BodyOverflow || !mobileMetrics.RailOverflow || mobileMetrics.Count != 7 || mobileMetrics.Rows != 1 ||
		mobileMetrics.SmallTargets != 0 || mobileMetrics.MaxCardHeight > 64 {
		t.Fatalf("mobile dashboard metric audit = %+v", mobileMetrics)
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
}
