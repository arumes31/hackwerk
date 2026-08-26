//go:build e2e

package e2e_test

import (
	"bytes"
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
	"example.invalid/hackplan/internal/app"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/voice"
	"example.invalid/hackplan/internal/web"
	"github.com/chromedp/chromedp"
)

func TestTask09VoiceReviewMobileJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, _, _, _, _, _, driverPassword := task04Application(t, databaseURL)
	customerService, err := app.CustomerService(pool)
	if err != nil {
		t.Fatal(err)
	}
	location, _ := time.LoadLocation("Europe/Vienna")
	voiceService, err := voice.New(postgres.NewVoiceStore(pool), voice.FakeTranscriber{Text: "Franz Huber, Unterneukirchen 15, Telefonnummer 0664 1234567, ungefähr 80 Kubikmeter Holz, ungefähr drei Stunden Hackzeit, möglichst Anfang September"}, voice.RuleExtractor{}, voice.Config{Enabled: true, Retention: time.Hour, RateLimitPerMinute: 10, ConcurrentPerUser: 2, Timezone: location}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	cfg := config.Config{AppName: "HackWerk", BaseURL: "http://" + server.Listener.Addr().String(), Timezone: "Europe/Vienna", Database: config.Database{ReadinessTimeout: 2 * time.Second}, Auth: config.Auth{SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf", SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour}, Voice: config.Voice{Enabled: true, Transcriber: "fake", MaxDuration: 90 * time.Second, MaxBytes: 15 << 20, ProviderTimeout: 5 * time.Second, TempDir: t.TempDir(), ExternalProviderNote: "Testprovider"}}
	router, err := web.NewRouter(web.Dependencies{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"}, Identity: identity, Customers: customerService, Drivers: drivers, Resources: resources, Appointments: appointments, Dashboard: e2eDashboard(t, pool), Voice: voiceService})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = router
	server.Start()
	t.Cleanup(server.Close)
	audioPath := filepath.Join(t.TempDir(), "voice-fixture.wav")
	if err = os.WriteFile(audioPath, minimalWAV(), 0o600); err != nil {
		t.Fatal(err)
	}
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(360, 800))
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browser, cancelBrowser := chromedp.NewContext(allocator)
	t.Cleanup(cancelBrowser)
	browser, cancelTimeout := context.WithTimeout(browser, 180*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browser) })
	if err = chromedp.Run(browser, chromedp.Navigate(server.URL+"/login"), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery), chromedp.SetValue("#username", "driver-task04", chromedp.ByQuery), chromedp.SetValue("#password", driverPassword, chromedp.ByQuery), chromedp.Click("form[action='/login'] button", chromedp.ByQuery), chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery), chromedp.Navigate(server.URL+"/voice"), chromedp.WaitVisible("[data-voice-upload]", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if err = runBrowserStep(browser, "upload fixture", chromedp.SetUploadFiles("[data-voice-upload] input[type=file]", []string{audioPath}, chromedp.ByQuery), chromedp.SetValue("[data-voice-upload] input[name=duration_seconds]", "3", chromedp.ByQuery), chromedp.Click("[data-voice-upload] button[type=submit]", chromedp.ByQuery), chromedp.WaitVisible("form[action$='/commit']", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	var reviewText string
	var fieldValues []string
	var overflow, smallTarget bool
	if err = chromedp.Run(browser, chromedp.Text("main", &reviewText, chromedp.ByQuery), chromedp.Evaluate(`['first_name','last_name','address_freeform','phone','volume_m3','hack_duration','preference_text'].map(n=>document.querySelector('[name="'+n+'"]').value)`, &fieldValues), chromedp.Evaluate(`document.documentElement.scrollWidth>window.innerWidth`, &overflow), chromedp.Evaluate(`Array.from(document.querySelectorAll('main button,main a.button')).some(e=>e.getBoundingClientRect().height<44||e.getBoundingClientRect().width<44)`, &smallTarget)); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Franz Huber", "Unterneukirchen 15", "80", "drei Stunden", "Anfang September", "prüfen", "Es wird kein Termin"} {
		if !strings.Contains(reviewText, expected) {
			t.Fatalf("review missing %q: %s", expected, reviewText)
		}
	}
	wantValues := []string{"Franz", "Huber", "Unterneukirchen 15", "0664 1234567", "80", "180", "Anfang September"}
	if strings.Join(fieldValues, "|") != strings.Join(wantValues, "|") {
		t.Fatalf("review field values=%v want=%v", fieldValues, wantValues)
	}
	if overflow || smallTarget {
		t.Fatalf("mobile overflow/small target=%v/%v", overflow, smallTarget)
	}
	if err = runBrowserStep(browser, "cancel draft discard",
		chromedp.Evaluate(`window.confirm=()=>false`, nil),
		chromedp.Click("form[action$='/discard'] button[type=submit]", chromedp.ByQuery),
		chromedp.WaitVisible("form[action$='/commit']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	var reviewDrafts int
	if err = pool.QueryRow(t.Context(), "SELECT count(*) FROM voice_drafts WHERE status='needs_review'").Scan(&reviewDrafts); err != nil {
		t.Fatal(err)
	}
	if reviewDrafts != 1 {
		t.Fatalf("cancelled discard changed draft state: needs_review=%d", reviewDrafts)
	}
	var customersBefore, jobsBefore, waitlistBefore, appointmentsBefore int
	for query, target := range map[string]*int{"SELECT count(*) FROM customers": &customersBefore, "SELECT count(*) FROM jobs": &jobsBefore, "SELECT count(*) FROM waitlist_entries": &waitlistBefore, "SELECT count(*) FROM appointments": &appointmentsBefore} {
		if err = pool.QueryRow(t.Context(), query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if err = runBrowserStep(browser, "review commit", chromedp.Click("input[name=reviewed]", chromedp.ByQuery), chromedp.Click("form[action$='/commit'] button[type=submit]", chromedp.ByQuery), chromedp.WaitVisible("main .detail-grid", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	var customersAfter, jobsAfter, waitlistAfter, appointmentsAfter, outbox int
	for query, target := range map[string]*int{"SELECT count(*) FROM customers": &customersAfter, "SELECT count(*) FROM jobs": &jobsAfter, "SELECT count(*) FROM waitlist_entries": &waitlistAfter, "SELECT count(*) FROM appointments": &appointmentsAfter, "SELECT count(*) FROM outbox_events": &outbox} {
		if err = pool.QueryRow(t.Context(), query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if customersAfter != customersBefore+1 || jobsAfter != jobsBefore+1 || waitlistAfter != waitlistBefore+1 || appointmentsAfter != appointmentsBefore || outbox != 0 {
		t.Fatalf("before customer/job/waitlist/appointment=%d/%d/%d/%d after=%d/%d/%d/%d outbox=%d", customersBefore, jobsBefore, waitlistBefore, appointmentsBefore, customersAfter, jobsAfter, waitlistAfter, appointmentsAfter, outbox)
	}
	var source, lifecycle string
	if err = pool.QueryRow(t.Context(), "SELECT source,workflow_status FROM jobs ORDER BY created_at DESC LIMIT 1").Scan(&source, &lifecycle); err != nil {
		t.Fatal(err)
	}
	if source != "voice" || lifecycle != "waitlist" {
		t.Fatalf("source/lifecycle=%s/%s", source, lifecycle)
	}
}

func minimalWAV() []byte {
	header := []byte{'R', 'I', 'F', 'F', 36, 0, 0, 0, 'W', 'A', 'V', 'E', 'f', 'm', 't', ' ', 16, 0, 0, 0, 1, 0, 1, 0, 0x40, 0x1f, 0, 0, 0x80, 0x3e, 0, 0, 2, 0, 16, 0, 'd', 'a', 't', 'a', 0, 0, 0, 0}
	return bytes.Clone(header)
}
