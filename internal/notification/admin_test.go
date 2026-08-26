package notification

import (
	"context"
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
func (*adminStoreStub) Retry(context.Context, auth.Actor, string, string, time.Time) error {
	return nil
}
func (store *adminStoreStub) Review(context.Context, auth.Actor, string, string, time.Time) error {
	store.reviewed = true
	return nil
}
func (*adminStoreStub) Reissue(context.Context, auth.Actor, string, int32, string, string, time.Time) error {
	return nil
}
func (*adminStoreStub) ResetResponse(context.Context, auth.Actor, string, int32, string, string, time.Time) error {
	return nil
}

func TestAdminServiceMasksOperationalViewsAndKeepsProviderReferenceAdminOnly(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	store := &adminStoreStub{
		statuses: []Status{{
			ID: "notification", AppointmentID: "appointment", Channel: "email", State: "failed",
			Recipient: "private@example.test", ErrorCode: "provider_permanent", ProviderReference: "provider-reference-0123456789",
			CreatedAt: now.Add(-time.Hour), ReviewedAt: now,
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
	if err != nil || appointment[0].ProviderReference != "" {
		t.Fatalf("appointment status leaked provider reference: %+v, %v", appointment, err)
	}
	adminHistory, err := service.AdminAppointmentHistory(t.Context(), admin, "appointment")
	if err != nil || adminHistory[0].ProviderReference != "provider…6789" {
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
