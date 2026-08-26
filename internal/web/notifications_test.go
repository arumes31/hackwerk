package web

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/notification"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
)

type notificationHTTPStore struct {
	statuses  []notification.Status
	callbacks []notification.CallbackRequest
	reviewed  bool
}

func (store *notificationHTTPStore) ListAppointment(context.Context, string) ([]notification.Status, error) {
	return append([]notification.Status(nil), store.statuses...), nil
}
func (store *notificationHTTPStore) ListFailed(context.Context, notification.FailureFilter, int32) ([]notification.Status, error) {
	return append([]notification.Status(nil), store.statuses...), nil
}
func (store *notificationHTTPStore) ListCallbacks(context.Context, int32) ([]notification.CallbackRequest, error) {
	return append([]notification.CallbackRequest(nil), store.callbacks...), nil
}
func (*notificationHTTPStore) Retry(context.Context, auth.Actor, string, string, time.Time) error {
	return nil
}
func (store *notificationHTTPStore) Review(context.Context, auth.Actor, string, string, time.Time) error {
	store.reviewed = true
	return nil
}
func (*notificationHTTPStore) Reissue(context.Context, auth.Actor, string, int32, string, string, time.Time) error {
	return nil
}
func (*notificationHTTPStore) ResetResponse(context.Context, auth.Actor, string, int32, string, string, time.Time) error {
	return nil
}

func TestNotificationFailuresRendersSafeOperationalDetails(t *testing.T) {
	now := time.Now().UTC()
	store := &notificationHTTPStore{
		statuses: []notification.Status{{
			ID: "notification", AppointmentID: "appointment", Channel: "sms", State: "retry_wait", Recipient: "+43 664 1234567",
			ErrorCode: "provider_temporary", ProviderReference: "provider-reference-0123456789", CreatedAt: now.Add(-time.Hour), AvailableAt: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour),
		}},
		callbacks: []notification.CallbackRequest{{AppointmentID: "appointment", JobNumber: "HW-1", CustomerName: "Maria Muster", Locality: "Musterort", Phone: "+43 664 1234567", RespondedAt: now}},
	}
	service, _ := notification.NewAdminService(store, func() time.Time { return now })
	request := notificationAdminRequest(t, http.MethodGet, "/admin/notifications?status=retry_wait")
	response := httptest.NewRecorder()
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	notificationFailures(service, templates.PageData{AppName: "HackWerk", Version: "test"}, "csrf", logger).ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, body)
	}
	for _, wanted := range []string{"Wartet auf Wiederholung", "***567", "Nächster Versuch", "provider…6789", "Offene Rückrufwünsche", "Synthetische Beispieldaten", "Segment(e)", "E-Mail"} {
		if !strings.Contains(body, wanted) {
			t.Fatalf("notification page missing %q: %s", wanted, body)
		}
	}
	for _, forbidden := range []string{"+43 664 1234567", "provider-reference-0123456789", "SENDBERRY", "api_key"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("notification page leaked %q", forbidden)
		}
	}
}

func TestNotificationReportAndReview(t *testing.T) {
	now := time.Now().UTC()
	store := &notificationHTTPStore{statuses: []notification.Status{{
		ID: "notification", Channel: "email", State: "failed", Recipient: "private@example.test", ErrorCode: "=CMD()", ProviderReference: "+provider-reference",
	}}}
	service, _ := notification.NewAdminService(store, func() time.Time { return now })
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	report := httptest.NewRecorder()
	notificationReport(service, logger).ServeHTTP(report, notificationAdminRequest(t, http.MethodGet, "/admin/notifications/report.csv?status=failed"))
	if report.Code != http.StatusOK || report.Header().Get("Cache-Control") != "no-store" || !strings.Contains(report.Header().Get("Content-Type"), "text/csv") || strings.Contains(report.Body.String(), "private@example.test") || !strings.Contains(report.Body.String(), "'=CMD()") {
		t.Fatalf("unsafe report status/headers/body = %d/%v/%q", report.Code, report.Header(), report.Body.String())
	}

	router := chi.NewRouter()
	router.Post("/admin/notifications/{notificationID}/review", reviewNotification(service, templates.PageData{AppName: "HackWerk"}, logger))
	review := httptest.NewRecorder()
	router.ServeHTTP(review, notificationAdminRequest(t, http.MethodPost, "/admin/notifications/notification/review"))
	if review.Code != http.StatusSeeOther || !store.reviewed {
		t.Fatalf("review status=%d reviewed=%t body=%q", review.Code, store.reviewed, review.Body.String())
	}
}

func notificationAdminRequest(t *testing.T, method, target string) *http.Request {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	// #nosec G124 -- request-only test fixture; no cookie is emitted to a browser.
	request.AddCookie(&http.Cookie{Name: "csrf", Value: "csrf-token"})
	return request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, auth.Session{Actor: auth.Actor{
		UserID: "admin", DisplayName: "Administrator", Role: auth.RoleAdmin,
	}}))
}
