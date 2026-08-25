//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/web"
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
	}
	router, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool,
		Build: buildinfo.Info{Version: "e2e"}, Identity: identity, Customers: customerService,
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
		chromedp.UserDataDir(t.TempDir()), chromedp.WindowSize(1280, 900),
	)
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 180*time.Second)
	t.Cleanup(cancelTimeout)

	var exceptionLock sync.Mutex
	exceptions := make([]string, 0)
	chromedp.ListenTarget(browserContext, func(event any) {
		if exception, ok := event.(*cdpruntime.EventExceptionThrown); ok {
			exceptionLock.Lock()
			exceptions = append(exceptions, exception.ExceptionDetails.Text)
			exceptionLock.Unlock()
		}
	})

	var transportInitiallyHidden bool
	if err := chromedp.Run(browserContext,
		chromedp.Navigate(server.URL+"/login"),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("open login page: %v", err)
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
		chromedp.WaitVisible("form[action='/customers']", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-transport-field]').hidden`, &transportInitiallyHidden),
	); err != nil {
		t.Fatalf("open customer intake: %s", browserDiagnostics(browserContext, err))
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
		chromedp.WaitVisible("article.job-card", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("customer detail after intake: %s", browserDiagnostics(browserContext, err))
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
	var mapsURL string
	var transportVisible, externalConfirmationVisible bool
	if err := chromedp.Run(browserContext,
		chromedp.AttributeValue("a[href^='https://www.google.com/maps/search/']", "href", &mapsURL, nil, chromedp.ByQuery),
		chromedp.Navigate(server.URL+"/customers/"+customerID+"/jobs/new"),
		chromedp.WaitVisible("[data-job-type]", chromedp.ByQuery),
		chromedp.Evaluate(`(() => { const e=document.querySelector('[data-job-type]'); e.value='chipping_with_transport'; e.dispatchEvent(new Event('change',{bubbles:true})); return !document.querySelector('[data-transport-field]').hidden; })()`, &transportVisible),
		chromedp.Evaluate(`(() => { const e=document.querySelector('[data-transport-mode]'); e.value='external'; e.dispatchEvent(new Event('change',{bubbles:true})); return !document.querySelector('[data-external-confirmation]').hidden; })()`, &externalConfirmationVisible),
		chromedp.SetValue("[name='volume_m3']", "120", chromedp.ByQuery),
		chromedp.SetValue("[name='hack_duration']", "4:00", chromedp.ByQuery),
		chromedp.SetValue("[name='transport_duration']", "1:00", chromedp.ByQuery),
		chromedp.SetValue("[name='transport_trips']", "2", chromedp.ByQuery),
		chromedp.Click("[name='external_confirmed']", chromedp.ByQuery),
		chromedp.SetValue("[name='region']", "Unterneukirchen", chromedp.ByQuery),
		chromedp.SetValue("[name='preference_text']", "Oktober", chromedp.ByQuery),
		chromedp.Click("form[data-transport-form] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("article.job-card:nth-of-type(2)", chromedp.ByQuery),
	); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(mapsURL, "https://www.google.com/maps/search/") || !transportVisible || !externalConfirmationVisible {
		t.Fatalf("maps = %q, transport visible = %v, external confirmation visible = %v", mapsURL, transportVisible, externalConfirmationVisible)
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

	var searchLocation, firstWaitlistText string
	var horizontalOverflow bool
	var screenshot []byte
	if err := runBrowserStep(browserContext, "logout driver",
		chromedp.Click("form[action='/logout'] button[type='submit']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("logout driver: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "login admin",
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
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
		chromedp.WaitVisible("a[href='/customers/"+customerID+"']", chromedp.ByQuery),
		chromedp.Location(&searchLocation),
	); err != nil {
		t.Fatalf("inspect customer search: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "open customer detail",
		chromedp.Click("a[href='/customers/"+customerID+"']", chromedp.ByQuery),
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
	if err := runBrowserStep(browserContext, "mobile waitlist",
		chromedp.WaitNotPresent("details.edit-card[open]", chromedp.ByQuery),
		chromedp.EmulateViewport(360, 800),
		chromedp.Navigate(server.URL+"/waitlist?sort=volume&direction=desc"),
		chromedp.WaitVisible("article.waitlist-card", chromedp.ByQuery),
		chromedp.Text("article.waitlist-card", &firstWaitlistText, chromedp.ByQuery),
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

func runBrowserStep(ctx context.Context, name string, actions ...chromedp.Action) error {
	stepContext, cancel := context.WithTimeout(ctx, 15*time.Second)
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
