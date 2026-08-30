package notification

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
)

type adminStoreStub struct {
	statuses  []Status
	callbacks []CallbackRequest
	filter    FailureFilter
	reviewed  bool
	retried   bool
	reissued  bool
	reset     bool
	reason    string
	version   int32
	at        time.Time
}

func (store *adminStoreStub) ListAppointment(context.Context, string) ([]Status, error) {
	return append([]Status(nil), store.statuses...), nil
}
func (store *adminStoreStub) ListFailed(_ context.Context, filter FailureFilter, _ int32) ([]Status, error) {
	store.filter = filter
	return append([]Status(nil), store.statuses...), nil
}
func (store *adminStoreStub) ListCallbacks(context.Context, int32) ([]CallbackRequest, error) {
	return append([]CallbackRequest(nil), store.callbacks...), nil
}
func (store *adminStoreStub) Retry(_ context.Context, _ auth.Actor, _ string, _ string, at time.Time) error {
	store.retried, store.at = true, at
	return nil
}
func (store *adminStoreStub) Review(context.Context, auth.Actor, string, string, time.Time) error {
	store.reviewed = true
	return nil
}
func (store *adminStoreStub) Reissue(_ context.Context, _ auth.Actor, _ string, version int32, reason, _ string, at time.Time) error {
	store.reissued, store.version, store.reason, store.at = true, version, reason, at
	return nil
}
func (store *adminStoreStub) ResetResponse(_ context.Context, _ auth.Actor, _ string, version int32, reason, _ string, at time.Time) error {
	store.reset, store.version, store.reason, store.at = true, version, reason, at
	return nil
}

func TestAdminServiceMasksOperationalViewsAndKeepsProviderReferenceAdminOnly(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	store := &adminStoreStub{
		statuses: []Status{{
			ID: "notification", AppointmentID: "appointment", Channel: "email", State: "failed",
			Recipient: "private@example.test", ErrorCode: "provider_permanent", ProviderReference: "provider-reference-0123456789",
			ResponseNote: "Bitte nur vormittags zurückrufen",
			CreatedAt:    now.Add(-time.Hour), ReviewedAt: now,
		}},
		callbacks: []CallbackRequest{{AppointmentID: "appointment", Phone: "+43 664 1234567", RespondedAt: now}},
	}
	service, err := NewAdminService(store, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	admin := auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
	failed, err := service.Failed(t.Context(), admin, FailureFailed, 20)
	if err != nil || len(failed) != 1 {
		t.Fatalf("Failed() = %+v, %v", failed, err)
	}
	if store.filter != FailureFailed || failed[0].Recipient != "p***@example.test" || failed[0].ProviderReference != "provider…6789" || !failed[0].Reviewed || failed[0].SuggestedAction == "" {
		t.Fatalf("prepared failure = %+v filter=%s", failed[0], store.filter)
	}
	appointment, err := service.AppointmentStatuses(t.Context(), admin, "appointment")
	if err != nil || appointment[0].ProviderReference != "" || appointment[0].ResponseNote == "" {
		t.Fatalf("appointment status leaked provider reference: %+v, %v", appointment, err)
	}
	driverAppointment, err := service.AppointmentStatuses(t.Context(), auth.Actor{UserID: "driver", Role: auth.RoleDriver}, "appointment")
	if err != nil || driverAppointment[0].ResponseNote != "" {
		t.Fatalf("driver appointment status leaked response note: %+v, %v", driverAppointment, err)
	}
	adminHistory, err := service.AdminAppointmentHistory(t.Context(), admin, "appointment")
	if err != nil || adminHistory[0].ProviderReference != "provider…6789" || adminHistory[0].ResponseNote == "" {
		t.Fatalf("admin history = %+v, %v", adminHistory, err)
	}
	callbacks, err := service.Callbacks(t.Context(), admin, 20)
	if err != nil || callbacks[0].Phone != "***567" {
		t.Fatalf("callbacks = %+v, %v", callbacks, err)
	}
}

func TestAdminServiceReviewFilterAndCSVAreSafe(t *testing.T) {
	store := &adminStoreStub{statuses: []Status{{
		ID: "notification", Channel: "sms", State: "failed", Recipient: "+43 664 1234567",
		ErrorCode: "=CMD()", ProviderReference: "+provider-reference",
	}}}
	service, _ := NewAdminService(store, func() time.Time { return time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC) })
	admin := auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
	if err := service.Review(t.Context(), admin, "notification", "request"); err != nil || !store.reviewed {
		t.Fatalf("Review() = %v reviewed=%t", err, store.reviewed)
	}
	report, err := service.CSV(t.Context(), admin, FailureFilter("untrusted"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(report)
	if store.filter != FailureAll || strings.Contains(text, "+43 664 1234567") || !strings.Contains(text, "'=CMD()") || !strings.Contains(text, "'+provide…ence") {
		t.Fatalf("unsafe CSV/filter: %q filter=%s", text, store.filter)
	}
}

func TestNotificationHelpers(t *testing.T) {
	if got := ParseFailureFilter("failed"); got != FailureFailed {
		t.Fatalf("filter = %s", got)
	}
	if got := ParseFailureFilter("DROP TABLE"); got != FailureAll {
		t.Fatalf("untrusted filter = %s", got)
	}
	if got := ShortProviderReference("12345678901234567890"); got != "12345678…7890" {
		t.Fatalf("provider reference = %q", got)
	}
}

func TestAdminActionsRequireAdministratorAndValidatedReason(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 11, 0, 0, 0, time.UTC)
	store := &adminStoreStub{}
	service, err := NewAdminService(store, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	admin := auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
	driver := auth.Actor{UserID: "driver", Role: auth.RoleDriver}
	if err := service.Retry(t.Context(), admin, "notification", "request"); err != nil || !store.retried || !store.at.Equal(now) {
		t.Fatalf("Retry() store/error = %#v / %v", store, err)
	}
	if err := service.Reissue(t.Context(), admin, "appointment", 2, "  Kunde informiert  ", "request"); err != nil || !store.reissued || store.version != 2 || store.reason != "Kunde informiert" {
		t.Fatalf("Reissue() store/error = %#v / %v", store, err)
	}
	if err := service.ResetResponse(t.Context(), admin, "appointment", 3, "  Irrtum korrigieren  ", "request"); err != nil || !store.reset || store.version != 3 || store.reason != "Irrtum korrigieren" {
		t.Fatalf("ResetResponse() store/error = %#v / %v", store, err)
	}
	if err := service.Reissue(t.Context(), admin, "appointment", 4, strings.Repeat("ä", 500), "request"); err != nil {
		t.Fatalf("500-character Unicode reason rejected: %v", err)
	}
	for _, test := range []struct {
		name string
		call func() error
		want error
	}{
		{name: "driver retry", call: func() error { return service.Retry(t.Context(), driver, "notification", "request") }, want: auth.ErrForbidden},
		{name: "empty retry", call: func() error { return service.Retry(t.Context(), admin, " ", "request") }, want: ErrRetryUnavailable},
		{name: "driver reissue", call: func() error { return service.Reissue(t.Context(), driver, "appointment", 1, "reason", "request") }, want: auth.ErrForbidden},
		{name: "missing reissue reason", call: func() error { return service.Reissue(t.Context(), admin, "appointment", 1, " ", "request") }, want: ErrAdminActionUnavailable},
		{name: "oversize reset reason", call: func() error {
			return service.ResetResponse(t.Context(), admin, "appointment", 1, strings.Repeat("ä", 501), "request")
		}, want: ErrAdminActionUnavailable},
		{name: "empty appointment", call: func() error { return service.ResetResponse(t.Context(), admin, "", 1, "reason", "request") }, want: ErrAdminActionUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := NewAdminService(nil, nil); err == nil {
		t.Fatal("NewAdminService accepted nil store")
	}
}

func TestAdminPresentationGuidanceCoversFailureStates(t *testing.T) {
	t.Parallel()
	for _, code := range []string{
		"provider_disabled", "provider_temporary", "provider_permanent", "delivery_uncertain", "confirmation_inactive",
		"token_key_unavailable", "template_invalid", "delivery_load_failed", "notification_state_failed", "notification_completion_failed", "unknown",
	} {
		summary, action := ErrorGuidance(code)
		if summary == "" || action == "" {
			t.Fatalf("ErrorGuidance(%q) = %q / %q", code, summary, action)
		}
	}
	if got := formatCSVTime(time.Time{}); got != "" {
		t.Fatalf("zero CSV time = %q", got)
	}
	if got := formatCSVTime(time.Date(2026, 8, 27, 12, 0, 0, 0, time.FixedZone("CEST", 7200))); got != "2026-08-27T10:00:00Z" {
		t.Fatalf("UTC CSV time = %q", got)
	}
}
