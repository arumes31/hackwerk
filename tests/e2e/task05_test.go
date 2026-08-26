//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/notification"
	"example.invalid/hackplan/internal/web"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func TestTask05NotificationConfirmationBrowserJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, driverID, chipperID, jobID, _, adminPassword, _ := task04Application(t, databaseURL)
	var admin auth.Actor
	admin.Role, admin.DisplayName = auth.RoleAdmin, "Anna Admin"
	if err := pool.QueryRow(t.Context(), "SELECT id::text FROM users WHERE username='admin-task04'").Scan(&admin.UserID); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC)
	draft, err := appointments.CreateDraftFromWaitlist(t.Context(), admin, appointment.CreateDraftInput{JobID: jobID, RequestID: "e2e-draft", Time: appointment.TimeInput{StartsAt: start, EndsAt: start.Add(3 * time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := appointments.AssignDriversAndResources(t.Context(), admin, appointment.AssignInput{
		MutateInput: appointment.MutateInput{ID: draft.ID, ExpectedVersion: draft.Version, RequestID: "e2e-assign"},
		Assignments: appointment.AssignmentInput{DriverIDs: []string{driverID}, PrimaryDriverID: driverID, Resources: []appointment.ResourceAssignment{{ID: chipperID, Purpose: appointment.PurposeChipping}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	proposed, err := appointments.ProposeAppointment(t.Context(), admin, appointment.MutateInput{ID: assigned.ID, ExpectedVersion: assigned.Version, RequestID: "e2e-propose"}, "")
	if err != nil {
		t.Fatal(err)
	}

	ring := notification.DevelopmentKeyRing()
	store := postgres.NewNotificationStore(pool)
	confirmations, _ := notification.NewConfirmationService(store, ring, time.Now)
	notificationAdmin, _ := notification.NewAdminService(store, time.Now)
	server := httptest.NewUnstartedServer(nil)
	cfg := config.Config{
		AppName: "HackWerk", BaseURL: "http://" + server.Listener.Addr().String(), Database: config.Database{ReadinessTimeout: 2 * time.Second},
		Auth:         config.Auth{SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf", SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour},
		Confirmation: config.Confirmation{RateLimit: 30}, Mail: config.Mail{Enabled: true},
	}
	router, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"},
		Identity: identity, Drivers: drivers, Resources: resources, Appointments: appointments, Dashboard: e2eDashboard(t, pool),
		Confirmations: confirmations, Notifications: notificationAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	type submissionFacts struct {
		contentType  string
		action       string
		formNonceSet bool
		originKind   string
	}
	submission := make(chan submissionFacts, 1)
	server.Config.Handler = http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/antwort") {
			body, readErr := io.ReadAll(request.Body)
			if readErr == nil {
				request.Body = io.NopCloser(bytes.NewReader(body))
				values, parseErr := url.ParseQuery(string(body))
				mediaType, _, mediaErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
				originKind := "other"
				switch request.Header.Get("Origin") {
				case "":
					originKind = "absent"
				case "null":
					originKind = "opaque"
				case cfg.BaseURL:
					originKind = "matching"
				}
				facts := submissionFacts{
					contentType:  mediaType,
					action:       values.Get("action"),
					formNonceSet: parseErr == nil && values.Get("form_nonce") != "",
					originKind:   originKind,
				}
				if mediaErr != nil {
					facts.contentType = ""
				}
				select {
				case submission <- facts:
				default:
				}
			}
		}
		router.ServeHTTP(response, request)
	})
	server.Start()
	t.Cleanup(server.Close)

	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(390, 844))
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 180*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browserContext) })

	if err := chromedp.Run(browserContext, chromedp.Navigate(server.URL+"/login"), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if err := runBrowserStep(browserContext, "login",
		chromedp.SetValue("#username", "admin-task04", chromedp.ByQuery), chromedp.SetValue("#password", adminPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible(".mobile-bottom-nav a[href='/calendar']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "fix appointment",
		chromedp.Navigate(server.URL+"/calendar?date=2026-08-25"), chromedp.WaitVisible("[data-calendar] .calendar-event-content", chromedp.ByQuery),
		chromedp.Click("[data-calendar] .calendar-event-content", chromedp.ByQuery), chromedp.WaitVisible("[data-appointment-fix]", chromedp.ByQuery),
		chromedp.Evaluate(`window.confirm=()=>true`, nil), chromedp.Click("[data-appointment-fix]", chromedp.ByQuery), chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}

	fakeMail := notification.NewFakeProvider(nil)
	processor, err := notification.NewProcessor(
		postgres.NewNotificationWorkerStore(pool), map[notification.Channel]notification.Provider{notification.ChannelEmail: fakeMail}, ring, mustVienna(t),
		notification.ProcessorConfig{BaseURL: server.URL, BusinessName: "HackWerk", BusinessAddress: "Werkstraße 1", BusinessPhone: "+43 1 234", Lease: time.Minute, BatchSize: 10},
		time.Now, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := processor.RunOnce(t.Context()); err != nil || count != 1 {
		t.Fatalf("worker count/error = %d/%v", count, err)
	}
	deliveries := fakeMail.Deliveries()
	if len(deliveries) != 1 {
		t.Fatalf("fake SMTP deliveries = %d", len(deliveries))
	}
	link := confirmationLink(deliveries[0].Text)
	response, err := http.Get(link)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.Header.Get("Referrer-Policy") != "no-referrer" || !strings.Contains(response.Header.Get("Cache-Control"), "no-store") || strings.Contains(string(body), "https://") {
		t.Fatalf("unsafe confirmation response headers/body: %v %q", response.Header, body)
	}
	if err := runBrowserStep(browserContext, "open customer confirmation",
		chromedp.Navigate(link), chromedp.WaitVisible("form.confirmation-actions", chromedp.ByQuery),
	); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(browserContext,
		chromedp.Click("form.confirmation-actions button[value='confirmed']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("confirmation submit: %s", browserDiagnostics(browserContext, err))
	}
	select {
	case facts := <-submission:
		if facts.contentType != "application/x-www-form-urlencoded" || facts.action != "confirmed" || !facts.formNonceSet || facts.originKind != "opaque" {
			t.Fatalf("native confirmation form facts = %+v", facts)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native confirmation form was not submitted")
	}
	if err := runBrowserStep(browserContext, "wait for customer confirmation result",
		chromedp.WaitVisible("#confirmation-title", chromedp.ByQuery),
		chromedp.Poll(`document.querySelector('#confirmation-title')?.textContent === 'Antwort gespeichert'`, nil),
	); err != nil {
		t.Fatalf("confirmation result: %s", browserDiagnostics(browserContext, err))
	}
	var confirmationResult string
	if err := chromedp.Run(browserContext, chromedp.Text("main", &confirmationResult, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(confirmationResult, "Antwort gespeichert") {
		t.Fatalf("confirmation result = %q", confirmationResult)
	}
	if err := runBrowserStep(browserContext, "confirmation result", chromedp.WaitVisible("h1", chromedp.ByQuery)); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var detail map[string]any
	if err := chromedp.Run(browserContext, chromedp.Evaluate(`fetch('/api/v1/appointments/`+proposed.ID+`').then(r=>r.json())`, &detail, func(parameters *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return parameters.WithAwaitPromise(true)
	})); err != nil {
		t.Fatal(err)
	}
	if detail["status_label"] != "Kunde bestätigt" {
		t.Fatalf("calendar confirmation status = %#v", detail)
	}

	current, err := appointments.AppointmentDetail(t.Context(), admin, proposed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := appointments.MoveAppointment(t.Context(), admin, appointment.MoveInput{
		MutateInput: appointment.MutateInput{ID: proposed.ID, ExpectedVersion: current.Version, RequestID: "e2e-move"},
		StartsAt:    start.Add(7 * 24 * time.Hour), EndsAt: start.Add(7*24*time.Hour + 3*time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	var invalidText string
	if err := runBrowserStep(browserContext, "old link revoked", chromedp.Navigate(link), chromedp.WaitVisible("h1", chromedp.ByQuery), chromedp.Text("main", &invalidText, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(invalidText, "nicht mehr gültig") {
		t.Fatalf("old link result = %q", invalidText)
	}

	if count, err := processor.RunOnce(t.Context()); err != nil || count != 1 {
		t.Fatalf("worker count/error after move = %d/%v", count, err)
	}
	deliveries = fakeMail.Deliveries()
	if len(deliveries) != 2 {
		t.Fatalf("fake SMTP deliveries after move = %d", len(deliveries))
	}
	newLink := confirmationLink(deliveries[1].Text)
	if newLink == "" || newLink == link {
		t.Fatalf("replacement confirmation link is empty or unchanged")
	}

	submitCustomerAction := func(action, expectedResult string) {
		t.Helper()
		if err := runBrowserStep(browserContext, "open replacement customer confirmation",
			chromedp.Navigate(newLink), chromedp.WaitVisible("form.confirmation-actions", chromedp.ByQuery),
		); err != nil {
			t.Fatal(browserDiagnostics(browserContext, err))
		}
		if err := chromedp.Run(browserContext,
			chromedp.Click("form.confirmation-actions button[value='"+action+"']", chromedp.ByQuery),
		); err != nil {
			t.Fatalf("%s confirmation submit: %s", action, browserDiagnostics(browserContext, err))
		}
		select {
		case facts := <-submission:
			if facts.contentType != "application/x-www-form-urlencoded" || facts.action != action || !facts.formNonceSet || facts.originKind != "opaque" {
				t.Fatalf("native %s form facts = %+v", action, facts)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("native %s confirmation form was not submitted", action)
		}
		var resultText string
		if err := runBrowserStep(browserContext, "wait for "+action+" result",
			chromedp.WaitVisible("#confirmation-title", chromedp.ByQuery),
			chromedp.Poll(`document.querySelector('#confirmation-title')?.textContent === 'Antwort gespeichert'`, nil),
			chromedp.Text("main", &resultText, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("%s confirmation result: %s", action, browserDiagnostics(browserContext, err))
		}
		if !strings.Contains(resultText, expectedResult) {
			t.Fatalf("%s confirmation result = %q", action, resultText)
		}
	}

	submitCustomerAction("callback_requested", "Rückrufwunsch wurde gespeichert")
	callbackDetail, err := appointments.AppointmentDetail(t.Context(), admin, proposed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if callbackDetail.Lifecycle != appointment.LifecycleFixed || callbackDetail.Confirmation != appointment.ConfirmationCallback {
		t.Fatalf("callback appointment lifecycle/confirmation = %q/%q", callbackDetail.Lifecycle, callbackDetail.Confirmation)
	}
	var callbackAdminText string
	if err := runBrowserStep(browserContext, "callback appears for admin",
		chromedp.Navigate(server.URL+"/admin/notifications"),
		chromedp.WaitVisible("#callback-table-title", chromedp.ByQuery),
		chromedp.Text("main", &callbackAdminText, chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !strings.Contains(callbackAdminText, "Offene Rückrufwünsche") || !strings.Contains(callbackAdminText, "HW-2026-0401") {
		t.Fatalf("admin callback list = %q", callbackAdminText)
	}

	submitCustomerAction("declined", "Termin abgelehnt")
	declinedDetail, err := appointments.AppointmentDetail(t.Context(), admin, proposed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if declinedDetail.Lifecycle != appointment.LifecycleFixed || declinedDetail.Confirmation != appointment.ConfirmationDeclined {
		t.Fatalf("declined appointment lifecycle/confirmation = %q/%q", declinedDetail.Lifecycle, declinedDetail.Confirmation)
	}
	callbacks, err := notificationAdmin.Callbacks(t.Context(), admin, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(callbacks) != 0 {
		t.Fatalf("callback remained open after decline: %+v", callbacks)
	}

	if err := notificationAdmin.Reissue(t.Context(), admin, proposed.ID, declinedDetail.Version, "E2E retry test", "e2e-reissue"); err != nil {
		t.Fatal(err)
	}
	failingProvider := notification.NewFakeProvider(notification.ErrTemporary)
	failingProcessor, err := notification.NewProcessor(
		postgres.NewNotificationWorkerStore(pool), map[notification.Channel]notification.Provider{notification.ChannelEmail: failingProvider}, ring, mustVienna(t),
		notification.ProcessorConfig{BaseURL: server.URL, BusinessName: "HackWerk", BusinessAddress: "Werkstraße 1", BusinessPhone: "+43 1 234", Lease: time.Minute, BatchSize: 10},
		time.Now, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := failingProcessor.RunOnce(t.Context()); err != nil || count != 1 {
		t.Fatalf("failing worker count/error = %d/%v", count, err)
	}
	var retryNotificationID, retryState string
	if err := pool.QueryRow(t.Context(), `
		SELECT id::text, status
		FROM notifications
		WHERE appointment_id=$1 AND status='retry_wait'
		ORDER BY created_at DESC
		LIMIT 1`, proposed.ID).Scan(&retryNotificationID, &retryState); err != nil {
		t.Fatal(err)
	}
	if retryState != "retry_wait" {
		t.Fatalf("notification state before admin retry = %q", retryState)
	}
	retryFormSelector := "form[action='/admin/notifications/" + retryNotificationID + "/retry']"
	var retryResponseStatus int
	awaitPromise := func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return params.WithAwaitPromise(true)
	}
	if err := runBrowserStep(browserContext, "admin retries failed notification",
		chromedp.Navigate(server.URL+"/admin/notifications?status=retry_wait"),
		chromedp.WaitVisible(retryFormSelector, chromedp.ByQuery),
		chromedp.Evaluate(`(()=>{const form=document.querySelector(`+quoteJS(retryFormSelector)+`);return fetch(form.action,{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams(new FormData(form))}).then(response=>response.status)})()`, &retryResponseStatus, awaitPromise),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if retryResponseStatus != http.StatusOK {
		t.Fatalf("admin retry response status = %d, want %d after redirect", retryResponseStatus, http.StatusOK)
	}
	var notificationState, outboxState string
	if err := pool.QueryRow(t.Context(), `
		SELECT n.status, o.status
		FROM notifications n
		JOIN outbox_events o ON o.aggregate_type='notification' AND o.aggregate_id=n.id
		WHERE n.id=$1`, retryNotificationID).Scan(&notificationState, &outboxState); err != nil {
		t.Fatal(err)
	}
	if notificationState != "queued" || outboxState != "queued" {
		t.Fatalf("admin retry notification/outbox state = %q/%q", notificationState, outboxState)
	}
	var retryAuditCount int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE action='notification.requeued' AND object_id=$1", proposed.ID).Scan(&retryAuditCount); err != nil {
		t.Fatal(err)
	}
	if retryAuditCount != 1 {
		t.Fatalf("notification retry audit count = %d", retryAuditCount)
	}
}

func mustVienna(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func confirmationLink(message string) string {
	for _, field := range strings.Fields(message) {
		if strings.Contains(field, "/termin/") {
			return strings.TrimSpace(field)
		}
	}
	return ""
}
