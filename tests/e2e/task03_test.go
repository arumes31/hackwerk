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
	router, err := web.NewRouter(web.Dependencies{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"}, Identity: identity, Drivers: driverService, Resources: resourceService})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.UserDataDir(t.TempDir()), chromedp.WindowSize(1280, 900))
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 180*time.Second)
	t.Cleanup(cancelTimeout)

	if err := chromedp.Run(browserContext, chromedp.Navigate(server.URL+"/login"), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := chromedp.Run(browserContext, chromedp.SetValue("#username", "driver-task03", chromedp.ByQuery), chromedp.SetValue("#password", driverPassword, chromedp.ByQuery), chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "create own week", chromedp.WaitVisible("a[href='/availability']", chromedp.ByQuery), chromedp.Navigate(server.URL+"/availability"), chromedp.WaitVisible("form[action='/availability/rules']", chromedp.ByQuery), chromedp.SetValue("form[action='/availability/rules'] [name='local_start']", "08:00", chromedp.ByQuery), chromedp.SetValue("form[action='/availability/rules'] [name='local_end']", "17:00", chromedp.ByQuery), chromedp.SetValue("form[action='/availability/rules'] [name='valid_from']", "2026-01-01", chromedp.ByQuery), chromedp.Click("form[action='/availability/rules'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible("details.edit-card", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
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

	if err := runBrowserStep(browserContext, "admin login", chromedp.Click("form[action='/logout'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery), chromedp.SetValue("#username", "admin-task03", chromedp.ByQuery), chromedp.SetValue("#password", adminPassword, chromedp.ByQuery), chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible("a[href='/admin/drivers']", chromedp.ByQuery)); err != nil {
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
	if err := runBrowserStep(browserContext, "admin edits other availability", chromedp.Navigate(server.URL+"/admin/drivers/"+otherID+"/availability"), chromedp.WaitVisible("form[action='"+adminRulePath+"']", chromedp.ByQuery), chromedp.SetValue("form[action='"+adminRulePath+"'] [name='local_start']", "12:00", chromedp.ByQuery), chromedp.SetValue("form[action='"+adminRulePath+"'] [name='local_end']", "18:00", chromedp.ByQuery), chromedp.SetValue("form[action='"+adminRulePath+"'] [name='valid_from']", "2026-01-01", chromedp.ByQuery), chromedp.Click("form[action='"+adminRulePath+"'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible("details.edit-card", chromedp.ByQuery)); err != nil {
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

	var ownerRules, otherRules, resourcesCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM availability_rules WHERE driver_id = $1", ownerID).Scan(&ownerRules); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM availability_rules WHERE driver_id = $1", otherID).Scan(&otherRules); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM resources WHERE name = 'Hackmaschine 2'").Scan(&resourcesCount); err != nil {
		t.Fatal(err)
	}
	if ownerRules != 1 || otherRules != 1 || resourcesCount != 1 {
		t.Fatalf("persisted owner/other/resource counts = %d/%d/%d", ownerRules, otherRules, resourcesCount)
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
