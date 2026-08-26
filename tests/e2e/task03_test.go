//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
	"example.invalid/hackplan/internal/web"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTask03BrowserJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	ctx := t.Context()
	pool, identity, driverService, resourceService, ownerID, otherID, driverPassword, adminPassword := task03Application(t, ctx, databaseURL)
	cfg := config.Config{AppName: "HackWerk", BaseURL: "http://127.0.0.1", Database: config.Database{ReadinessTimeout: 2 * time.Second}, Auth: config.Auth{SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf", SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour}}
	router, err := web.NewRouter(web.Dependencies{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"}, Identity: identity, Drivers: driverService, Resources: resourceService, Dashboard: e2eDashboard(t, pool)})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(1280, 900))
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 180*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browserContext) })

	if err := chromedp.Run(browserContext, chromedp.Navigate(server.URL+"/login"), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := chromedp.Run(browserContext, chromedp.SetValue("#username", "driver-task03", chromedp.ByQuery), chromedp.SetValue("#password", driverPassword, chromedp.ByQuery), chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "create own week", chromedp.WaitVisible("[data-account-menu] summary", chromedp.ByQuery), chromedp.Click("[data-account-menu] summary", chromedp.ByQuery), chromedp.WaitVisible("a[href='/availability']", chromedp.ByQuery), chromedp.Navigate(server.URL+"/availability"), chromedp.Click(".operation-create > summary", chromedp.ByQuery), chromedp.WaitVisible("form[action='/availability/rules']", chromedp.ByQuery), chromedp.SetValue("form[action='/availability/rules'] [name='start_time']", "08:00", chromedp.ByQuery), chromedp.SetValue("form[action='/availability/rules'] [name='end_time']", "17:00", chromedp.ByQuery), chromedp.SetValue("form[action='/availability/rules'] [name='valid_from']", "2026-01-01", chromedp.ByQuery), chromedp.Click("form[action='/availability/rules'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible("details.edit-card", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var retainedStart, overlapError string
	if err := runBrowserStep(browserContext, "availability error retains values",
		chromedp.Click(".operation-create > summary", chromedp.ByQuery),
		chromedp.WaitVisible("form[action='/availability/rules']", chromedp.ByQuery),
		chromedp.SetValue("form[action='/availability/rules'] [name='start_time']", "09:00", chromedp.ByQuery),
		chromedp.SetValue("form[action='/availability/rules'] [name='end_time']", "16:00", chromedp.ByQuery),
		chromedp.SetValue("form[action='/availability/rules'] [name='valid_from']", "2026-01-01", chromedp.ByQuery),
		chromedp.Click("form[action='/availability/rules'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("form[action='/availability/rules'] [data-operation-error]", chromedp.ByQuery),
		chromedp.Value("form[action='/availability/rules'] [name='start_time']", &retainedStart, chromedp.ByQuery),
		chromedp.Text("form[action='/availability/rules'] [data-operation-error]", &overlapError, chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if retainedStart != "09:00" || overlapError == "" {
		t.Fatalf("availability validation value/error = %q/%q", retainedStart, overlapError)
	}

	foreignRulePath := "/admin/drivers/" + otherID + "/availability/rules"
	var forbiddenInline struct {
		Text            string  `json:"text"`
		Markup          string  `json:"markup"`
		Start           string  `json:"start"`
		GridColumnStart string  `json:"gridColumnStart"`
		GridColumnEnd   string  `json:"gridColumnEnd"`
		AlertWidth      float64 `json:"alertWidth"`
		FormWidth       float64 `json:"formWidth"`
	}
	if err := runBrowserStep(browserContext, "forbidden operation stays inline",
		chromedp.SetValue("form[data-availability-rule-form] [name='start_time']", "10:00", chromedp.ByQuery),
		chromedp.SetValue("form[data-availability-rule-form] [name='end_time']", "11:00", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('form[data-availability-rule-form]').setAttribute('action',`+quoteJS(foreignRulePath)+`)`, nil),
		chromedp.Click("form[data-availability-rule-form] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("form[data-availability-rule-form] [data-operation-error]", chromedp.ByQuery),
		chromedp.Evaluate(`(()=>{const form=document.querySelector('form[data-availability-rule-form]');const alert=form.querySelector('[data-operation-error]');const style=getComputedStyle(alert),formStyle=getComputedStyle(form);return {text:alert.textContent.trim(),markup:alert.innerHTML,start:form.querySelector("[name='start_time']").value,gridColumnStart:style.gridColumnStart,gridColumnEnd:style.gridColumnEnd,alertWidth:alert.getBoundingClientRect().width,formWidth:form.clientWidth-parseFloat(formStyle.paddingLeft)-parseFloat(formStyle.paddingRight)}})()`, &forbiddenInline),
	); err != nil {
		t.Fatalf("forbidden operation inline response: %s", browserDiagnostics(browserContext, err))
	}
	markupLower := strings.ToLower(forbiddenInline.Markup)
	if !strings.Contains(forbiddenInline.Text, "Berechtigung") || strings.Contains(markupLower, "&lt;") || strings.Contains(markupLower, "<html") || strings.Contains(markupLower, "doctype") || forbiddenInline.Start != "10:00" || forbiddenInline.GridColumnStart != "1" || forbiddenInline.GridColumnEnd != "-1" || forbiddenInline.AlertWidth < forbiddenInline.FormWidth-1 {
		t.Fatalf("forbidden inline text/markup/start/grid/width = %q/%q/%q/%s:%s/%.1f:%.1f", forbiddenInline.Text, forbiddenInline.Markup, forbiddenInline.Start, forbiddenInline.GridColumnStart, forbiddenInline.GridColumnEnd, forbiddenInline.AlertWidth, forbiddenInline.FormWidth)
	}

	partialExceptionForm := "form[action='/availability/exceptions']:has([name='starts_at'])"
	var localTimeError, retainedExceptionStart, retainedExceptionNote string
	if err := runBrowserStep(browserContext, "invalid DST exception retains values",
		chromedp.Evaluate(`document.querySelector(`+quoteJS(partialExceptionForm)+`).closest('details').open=true`, nil),
		chromedp.SetValue(partialExceptionForm+" [name='starts_at']", "2026-03-29T02:30", chromedp.ByQuery),
		chromedp.SetValue(partialExceptionForm+" [name='ends_at']", "2026-03-29T03:30", chromedp.ByQuery),
		chromedp.SetValue(partialExceptionForm+" [name='internal_note']", "Diese Eingabe bleibt erhalten", chromedp.ByQuery),
		chromedp.Click(partialExceptionForm+" button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible(partialExceptionForm+" [data-operation-error]", chromedp.ByQuery),
		chromedp.Text(partialExceptionForm+" [data-operation-error]", &localTimeError, chromedp.ByQuery),
		chromedp.Value(partialExceptionForm+" [name='starts_at']", &retainedExceptionStart, chromedp.ByQuery),
		chromedp.Value(partialExceptionForm+" [name='internal_note']", &retainedExceptionNote, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("invalid DST exception: %s", browserDiagnostics(browserContext, err))
	}
	if !strings.Contains(localTimeError, "Zeitumstellung") || retainedExceptionStart != "2026-03-29T02:30" || retainedExceptionNote != "Diese Eingabe bleibt erhalten" {
		t.Fatalf("DST exception error/start/note = %q/%q/%q", localTimeError, retainedExceptionStart, retainedExceptionNote)
	}

	allDayExceptionForm := "form[action='/availability/exceptions']:has([name='local_date'])"
	if err := runBrowserStep(browserContext, "create all-day availability exception",
		chromedp.Evaluate(`document.querySelector(`+quoteJS(allDayExceptionForm)+`).closest('details').open=true`, nil),
		chromedp.SetValue(allDayExceptionForm+" [name='type']", "available_override", chromedp.ByQuery),
		chromedp.SetValue(allDayExceptionForm+" [name='local_date']", "2026-10-12", chromedp.ByQuery),
		chromedp.SetValue(allDayExceptionForm+" [name='internal_note']", "E2E kurzfristig verfügbar", chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.dataset.e2eNavigationMarker='pending'`, nil),
		chromedp.Click(allDayExceptionForm+" button[type='submit']", chromedp.ByQuery),
		chromedp.WaitNotPresent("html[data-e2e-navigation-marker='pending']", chromedp.ByQuery),
		chromedp.WaitVisible("main[data-operation-page]", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("create all-day exception: %s", browserDiagnostics(browserContext, err))
	}
	var overrideExceptionID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM availability_exceptions WHERE driver_id=$1 AND exception_type='available_override' AND local_date='2026-10-12'`, ownerID).Scan(&overrideExceptionID); err != nil {
		t.Fatal(err)
	}

	vacationForm := "form[action='/availability/exceptions/vacation-preset']"
	if err := runBrowserStep(browserContext, "create five-day vacation",
		chromedp.Evaluate(`document.querySelector(`+quoteJS(vacationForm)+`).closest('details').open=true`, nil),
		chromedp.SetValue(vacationForm+" [name='local_date']", "2026-09-04", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector(`+quoteJS(vacationForm+" [name='workweek']")+`).checked=true`, nil),
		chromedp.SetValue(vacationForm+" [name='internal_note']", "E2E Urlaub", chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.dataset.e2eNavigationMarker='pending'`, nil),
		chromedp.Click(vacationForm+" button[type='submit']", chromedp.ByQuery),
		chromedp.WaitNotPresent("html[data-e2e-navigation-marker='pending']", chromedp.ByQuery),
		chromedp.WaitVisible("main[data-operation-page]", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("create vacation preset: %s", browserDiagnostics(browserContext, err))
	}

	deleteOverrideForm := "form[action='/availability/exceptions/" + overrideExceptionID + "/delete']"
	var deleteCancelConfirmCalls int
	if err := runBrowserStep(browserContext, "cancel exception deletion",
		chromedp.Evaluate(`document.querySelector(`+quoteJS(deleteOverrideForm)+`).closest('details').open=true`, nil),
		chromedp.Evaluate(`window.__e2eConfirmCalls=0;window.confirm=()=>{window.__e2eConfirmCalls++;return false}`, nil),
		chromedp.Click(deleteOverrideForm+" button[type='submit']", chromedp.ByQuery),
		chromedp.Evaluate(`window.__e2eConfirmCalls`, &deleteCancelConfirmCalls),
	); err != nil {
		t.Fatalf("cancel exception deletion: %s", browserDiagnostics(browserContext, err))
	}
	var overrideExceptionsAfterCancel int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM availability_exceptions WHERE id=$1`, overrideExceptionID).Scan(&overrideExceptionsAfterCancel); err != nil {
		t.Fatal(err)
	}
	if deleteCancelConfirmCalls != 1 || overrideExceptionsAfterCancel != 1 {
		t.Fatalf("cancelled exception delete confirmations/rows = %d/%d", deleteCancelConfirmCalls, overrideExceptionsAfterCancel)
	}

	var foreignStatus int
	expression := fmt.Sprintf(`fetch(%q,{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf_token:document.querySelector("input[name='csrf_token']").value,weekday:'2',local_start:'08:00',local_end:'17:00',valid_from:'2026-01-01',status:'available'})}).then(r=>r.status)`, "/admin/drivers/"+otherID+"/availability/rules")
	awaitPromise := func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return params.WithAwaitPromise(true)
	}
	if err := chromedp.Run(browserContext, chromedp.Evaluate(expression, &foreignStatus, awaitPromise)); err != nil {
		t.Fatal(err)
	}
	if foreignStatus != 403 {
		t.Fatalf("foreign availability status = %d, want 403", foreignStatus)
	}
	if _, err := pool.Exec(ctx, "UPDATE drivers SET user_id=NULL WHERE id=$1", ownerID); err != nil {
		t.Fatal(err)
	}
	var desktopAvailabilityLinks, mobileAvailabilityLinks int
	var dashboardText string
	if err := runBrowserStep(browserContext, "driver without linked profile",
		chromedp.Navigate(server.URL+"/dashboard"),
		chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll(".shell-tools a[href='/availability']").length`, &desktopAvailabilityLinks),
		chromedp.EmulateViewport(360, 800),
		chromedp.Click("[data-mobile-menu] summary", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll(".mobile-more__panel a[href='/availability']").length`, &mobileAvailabilityLinks),
		chromedp.Navigate(server.URL+"/availability"),
		chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery),
		chromedp.Text("main", &dashboardText, chromedp.ByQuery),
		chromedp.EmulateViewport(1280, 900),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if desktopAvailabilityLinks != 0 || mobileAvailabilityLinks != 0 || !strings.Contains(dashboardText, "Kein Fahrerprofil ist diesem Zugang zugeordnet.") {
		t.Fatalf("unlinked driver desktop/mobile links and dashboard = %d/%d/%q", desktopAvailabilityLinks, mobileAvailabilityLinks, dashboardText)
	}

	if err := runBrowserStep(browserContext, "admin login", chromedp.Click("[data-account-menu] summary", chromedp.ByQuery), chromedp.WaitVisible("form[action='/logout'] button[type='submit']", chromedp.ByQuery), chromedp.Evaluate(`document.querySelector("[data-account-menu] form[action='/logout']").requestSubmit()`, nil), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery), chromedp.SetValue("#username", "admin-task03", chromedp.ByQuery), chromedp.SetValue("#password", adminPassword, chromedp.ByQuery), chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible("[data-admin-menu] summary", chromedp.ByQuery), chromedp.Click("[data-admin-menu] summary", chromedp.ByQuery), chromedp.WaitVisible("a[href='/admin/drivers']", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var driversText string
	if err := runBrowserStep(browserContext, "admin driver overview", chromedp.Navigate(server.URL+"/admin/drivers"), chromedp.WaitVisible(".driver-overview", chromedp.ByQuery), chromedp.Text("main", &driversText, chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !strings.Contains(driversText, "Franz Fahrer") || !strings.Contains(driversText, "Maria ohne Login") {
		t.Fatalf("driver overview = %q", driversText)
	}
	adminRulePath := "/admin/drivers/" + otherID + "/availability/rules"
	if err := runBrowserStep(browserContext, "admin edits other availability", chromedp.Navigate(server.URL+"/admin/drivers/"+otherID+"/availability"), chromedp.Click(".operation-create > summary", chromedp.ByQuery), chromedp.WaitVisible("form[action='"+adminRulePath+"']", chromedp.ByQuery), chromedp.SetValue("form[action='"+adminRulePath+"'] [name='start_time']", "12:00", chromedp.ByQuery), chromedp.SetValue("form[action='"+adminRulePath+"'] [name='end_time']", "18:00", chromedp.ByQuery), chromedp.SetValue("form[action='"+adminRulePath+"'] [name='valid_from']", "2026-01-01", chromedp.ByQuery), chromedp.Evaluate(`document.documentElement.dataset.e2eNavigationMarker='pending'`, nil), chromedp.Click("form[action='"+adminRulePath+"'] button[type='submit']", chromedp.ByQuery), chromedp.WaitNotPresent("html[data-e2e-navigation-marker='pending']", chromedp.ByQuery), chromedp.WaitVisible("details.edit-card", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}

	var horizontalOverflow bool
	var screenshot []byte
	if err := runBrowserStep(browserContext, "mobile resource creation", chromedp.EmulateViewport(360, 800), chromedp.Navigate(server.URL+"/admin/resources"), chromedp.WaitVisible("form[action='/admin/resources']", chromedp.ByQuery), chromedp.SetValue("form[action='/admin/resources'] [name='name']", "Hackmaschine 2", chromedp.ByQuery), chromedp.SetValue("form[action='/admin/resources'] [name='volume_m3']", "180", chromedp.ByQuery), chromedp.Click("form[action='/admin/resources'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible("article.operation-card", chromedp.ByQuery), chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &horizontalOverflow), chromedp.FullScreenshot(&screenshot, 90)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if horizontalOverflow {
		t.Fatal("resource page overflows horizontally at 360 px")
	}
	artifact := filepath.Join(t.ArtifactDir(), "task03-mobile-resources.png")
	if err := os.WriteFile(artifact, screenshot, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("mobile browser screenshot: %s", artifact)

	var ownerRules, otherRules, resourcesCount, ownerExceptions int
	var vacationDates string
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM availability_rules WHERE driver_id = $1", ownerID).Scan(&ownerRules); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM availability_rules WHERE driver_id = $1", otherID).Scan(&otherRules); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM resources WHERE name = 'Hackmaschine 2'").Scan(&resourcesCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM availability_exceptions WHERE driver_id = $1", ownerID).Scan(&ownerExceptions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT string_agg(local_date::text, ',' ORDER BY local_date) FROM availability_exceptions WHERE driver_id=$1 AND internal_note='E2E Urlaub'`, ownerID).Scan(&vacationDates); err != nil {
		t.Fatal(err)
	}
	wantVacationDates := "2026-09-04,2026-09-07,2026-09-08,2026-09-09,2026-09-10"
	if ownerRules != 1 || otherRules != 1 || resourcesCount != 1 || ownerExceptions != 6 || vacationDates != wantVacationDates {
		t.Fatalf("persisted owner/other/resource/exception counts and vacation dates = %d/%d/%d/%d/%q", ownerRules, otherRules, resourcesCount, ownerExceptions, vacationDates)
	}
}

func task03Application(t *testing.T, ctx context.Context, databaseURL string) (*pgxpool.Pool, *auth.Service, *driver.Service, *resource.Service, string, string, string, string) {
	t.Helper()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	databaseConfig := config.Database{URL: databaseURL, MaxConnections: 10, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second}
	pool, err := postgres.Open(ctx, databaseConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "TRUNCATE availability_exceptions, availability_rules, resources, audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE"); err != nil {
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
	driverPassword, adminPassword := randomE2EPassword(t), randomE2EPassword(t)
	system := auth.Actor{Role: auth.RoleAdmin, System: true, DisplayName: "E2E Setup"}
	if _, err := identity.CreateUser(ctx, system, auth.CreateUserInput{Username: "driver-task03", DisplayName: "Franz Fahrer", Role: auth.RoleDriver, Password: driverPassword, CreateDriver: true, RequestID: "e2e-setup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.CreateUser(ctx, system, auth.CreateUserInput{Username: "admin-task03", DisplayName: "Anna Admin", Role: auth.RoleAdmin, Password: adminPassword, RequestID: "e2e-setup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET must_change_password = false"); err != nil {
		t.Fatal(err)
	}
	var ownerID, otherID string
	if err := pool.QueryRow(ctx, "SELECT id::text FROM drivers WHERE display_name = 'Franz Fahrer'").Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO drivers (display_name) VALUES ('Maria ohne Login') RETURNING id::text").Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	driverService, err := driver.New(postgres.NewDriverStore(pool), location)
	if err != nil {
		t.Fatal(err)
	}
	resourceService, err := resource.New(postgres.NewResourceStore(pool))
	if err != nil {
		t.Fatal(err)
	}
	return pool, identity, driverService, resourceService, ownerID, otherID, driverPassword, adminPassword
}
