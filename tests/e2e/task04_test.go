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
	"sync"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/appointment"
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

func TestTask04CalendarBrowserJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, driverID, chipperID, jobID, adminPassword, driverPassword := task04Application(t, databaseURL)
	cfg := config.Config{
		AppName: "HackWerk", BaseURL: "http://127.0.0.1:18533", Database: config.Database{ReadinessTimeout: 2 * time.Second},
		Auth: config.Auth{SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf", SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour},
	}
	router, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"},
		Identity: identity, Drivers: drivers, Resources: resources, Appointments: appointments,
	})
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

	var exceptionLock sync.Mutex
	exceptions := make([]string, 0)
	chromedp.ListenTarget(browserContext, func(event any) {
		if value, ok := event.(*cdpruntime.EventExceptionThrown); ok {
			exceptionLock.Lock()
			exceptions = append(exceptions, value.ExceptionDetails.Text)
			exceptionLock.Unlock()
		}
	})

	if err := chromedp.Run(browserContext,
		chromedp.Navigate(server.URL+"/login"), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("open login: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "admin login",
		chromedp.SetValue("#username", "admin-task04", chromedp.ByQuery), chromedp.SetValue("#password", adminPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("a[href='/calendar']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "open calendar",
		chromedp.Navigate(server.URL+"/calendar"),
		chromedp.WaitVisible("[data-calendar]", chromedp.ByQuery), chromedp.WaitVisible("[data-plan-job='"+jobID+"']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "open mobile proposal form",
		chromedp.EmulateViewport(360, 820),
		chromedp.Click("[data-plan-job='"+jobID+"']", chromedp.ByQuery),
		chromedp.WaitVisible("[data-planning-dialog]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var submittedForm string
	if err := runBrowserStep(browserContext, "submit mobile proposal form",
		chromedp.SetValue("[data-planning-start]", "2026-08-25T08:00", chromedp.ByQuery),
		chromedp.SetValue("[data-planning-duration]", "180", chromedp.ByQuery),
		chromedp.Click("input[name='driver_id'][value='"+driverID+"']", chromedp.ByQuery),
		chromedp.SetValue("select[name='primary_driver_id']", driverID, chromedp.ByQuery),
		chromedp.SetValue("select[name='chipper_resource_id']", chipperID, chromedp.ByQuery),
		chromedp.Evaluate(`JSON.stringify([...new FormData(document.querySelector('[data-planning-form]')).entries()])`, &submittedForm),
		chromedp.Click("[data-planning-form] button[type='submit']", chromedp.ByQuery),
		chromedp.Poll(`!document.querySelector('[data-planning-dialog]').open || !document.querySelector('[data-planning-error]').hidden`, nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var planningError string
	var planningOpen bool
	if err := chromedp.Run(browserContext,
		chromedp.Text("[data-planning-error]", &planningError, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-planning-dialog]').open`, &planningOpen),
	); err != nil {
		t.Fatal(err)
	}
	if planningOpen {
		t.Fatalf("planning form stayed open: %s; form=%s", planningError, submittedForm)
	}
	if err := runBrowserStep(browserContext, "proposal appears",
		chromedp.WaitVisible("[data-calendar] .calendar-event-content", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}

	var appointmentID string
	var lifecycle string
	if err := pool.QueryRow(t.Context(), "SELECT id::text, lifecycle_status FROM appointments WHERE job_id=$1", jobID).Scan(&appointmentID, &lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "proposal" {
		t.Fatalf("planned lifecycle = %q", lifecycle)
	}

	var horizontalOverflow bool
	var screenshot []byte
	if err := runBrowserStep(browserContext, "explicit fix",
		chromedp.Click("[data-calendar] .calendar-event-content", chromedp.ByQuery),
		chromedp.WaitVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-appointment-fix]", chromedp.ByQuery),
		chromedp.Evaluate(`window.confirm=()=>true`, nil),
		chromedp.Click("[data-appointment-fix]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &horizontalOverflow),
		chromedp.FullScreenshot(&screenshot, 90),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if horizontalOverflow {
		t.Fatal("calendar overflows horizontally at 360 px")
	}
	artifact := filepath.Join(t.ArtifactDir(), "task04-mobile-calendar.png")
	if err := os.WriteFile(artifact, screenshot, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("mobile calendar screenshot: %s", artifact)

	var confirmation, workflow string
	var outbox int
	if err := pool.QueryRow(t.Context(), "SELECT a.lifecycle_status, a.confirmation_status, j.workflow_status FROM appointments a JOIN jobs j ON j.id=a.job_id WHERE a.id=$1", appointmentID).Scan(&lifecycle, &confirmation, &workflow); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='appointment.fixed'", appointmentID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "fixed" || confirmation != "pending" || workflow != "scheduled" || outbox != 1 {
		t.Fatalf("fixed lifecycle/confirmation/workflow/outbox = %s/%s/%s/%d", lifecycle, confirmation, workflow, outbox)
	}

	if err := runBrowserStep(browserContext, "driver read only",
		chromedp.Click("form[action='/logout'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
		chromedp.SetValue("#username", "driver-task04", chromedp.ByQuery), chromedp.SetValue("#password", driverPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("a[href='/calendar']", chromedp.ByQuery), chromedp.Navigate(server.URL+"/calendar"),
		chromedp.WaitVisible("[data-calendar] .calendar-event-content", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var planningControls int
	var forbiddenStatus int
	expression := fmt.Sprintf(`fetch(%q,{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf_token:document.querySelector('[data-calendar]').dataset.csrf,version:'4',starts_at:'2026-09-02T06:00:00Z',ends_at:'2026-09-02T09:00:00Z'})}).then(r=>r.status)`, "/api/v1/appointments/"+appointmentID+"/move")
	awaitPromise := func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return params.WithAwaitPromise(true)
	}
	if err := chromedp.Run(browserContext,
		chromedp.Evaluate(`document.querySelectorAll('[data-calendar-waitlist],[data-planning-dialog],[data-appointment-fix]').length`, &planningControls),
		chromedp.Evaluate(expression, &forbiddenStatus, awaitPromise),
	); err != nil {
		t.Fatal(err)
	}
	if planningControls != 0 || forbiddenStatus != 403 {
		t.Fatalf("driver planning controls/direct status = %d/%d", planningControls, forbiddenStatus)
	}

	exceptionLock.Lock()
	defer exceptionLock.Unlock()
	if len(exceptions) > 0 {
		t.Fatalf("browser JavaScript exceptions: %v", exceptions)
	}
}

func task04Application(t *testing.T, databaseURL string) (*pgxpool.Pool, *auth.Service, *driver.Service, *resource.Service, *appointment.Service, string, string, string, string, string) {
	t.Helper()
	ctx := t.Context()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(ctx, config.Database{URL: databaseURL, MaxConnections: 12, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "TRUNCATE outbox_events, appointments, waitlist_entries, jobs, customers, availability_exceptions, availability_rules, resources, audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE"); err != nil {
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
	adminPassword, driverPassword := randomE2EPassword(t), randomE2EPassword(t)
	system := auth.Actor{Role: auth.RoleAdmin, System: true, DisplayName: "E2E Setup"}
	for _, account := range []auth.CreateUserInput{
		{Username: "admin-task04", DisplayName: "Anna Admin", Role: auth.RoleAdmin, Password: adminPassword, RequestID: "e2e-setup"},
		{Username: "driver-task04", DisplayName: "Franz Fahrer", Role: auth.RoleDriver, Password: driverPassword, CreateDriver: true, RequestID: "e2e-setup"},
	} {
		if _, err := identity.CreateUser(ctx, system, account); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET must_change_password=false"); err != nil {
		t.Fatal(err)
	}
	var driverID string
	if err := pool.QueryRow(ctx, "SELECT id::text FROM drivers WHERE display_name='Franz Fahrer'").Scan(&driverID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO availability_rules (driver_id, iso_weekday, local_start, local_end, valid_from, status) VALUES ($1,2,'06:00','20:00','2026-01-01','available')", driverID); err != nil {
		t.Fatal(err)
	}
	var chipperID string
	if err := pool.QueryRow(ctx, "INSERT INTO resources (resource_type,name,exclusive) VALUES ('chipper','Hackmaschine 1',true) RETURNING id::text").Scan(&chipperID); err != nil {
		t.Fatal(err)
	}
	var customerID, jobID string
	if err := pool.QueryRow(ctx, "INSERT INTO customers (first_name,last_name,street,postal_code,locality) VALUES ('Franz','Huber','Waldweg 1','4710','Grieskirchen') RETURNING id::text").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO jobs (job_number,customer_id,job_type,volume_m3,estimated_hack_minutes) VALUES ('HW-2026-0401',$1,'chipping_only',80,180) RETURNING id::text", customerID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO waitlist_entries (job_id) VALUES ($1)", jobID); err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	drivers, err := driver.New(postgres.NewDriverStore(pool), location)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := resource.New(postgres.NewResourceStore(pool))
	if err != nil {
		t.Fatal(err)
	}
	appointments, err := appointment.New(postgres.NewAppointmentStore(pool), drivers, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return pool, identity, drivers, resources, appointments, driverID, chipperID, jobID, adminPassword, driverPassword
}
