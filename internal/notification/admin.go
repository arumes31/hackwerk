package notification

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"example.invalid/hackplan/internal/auth"
)

var (
	ErrRetryUnavailable       = errors.New("notification: retry unavailable")
	ErrAdminActionUnavailable = errors.New("notification: admin action unavailable")
)

type Status struct {
	ID                 string    `json:"id"`
	AppointmentID      string    `json:"appointment_id"`
	Channel            string    `json:"channel"`
	State              string    `json:"status"`
	Recipient          string    `json:"recipient"`
	ErrorCode          string    `json:"error_code,omitempty"`
	ErrorSummary       string    `json:"error_summary,omitempty"`
	SuggestedAction    string    `json:"suggested_action,omitempty"`
	ProviderReference  string    `json:"provider_reference,omitempty"`
	ConfirmationStatus string    `json:"confirmation_status,omitempty"`
	Response           string    `json:"response,omitempty"`
	ResponseNote       string    `json:"response_note,omitempty"`
	AttemptCount       int32     `json:"attempt_count"`
	MaxAttempts        int32     `json:"max_attempts"`
	AvailableAt        time.Time `json:"available_at,omitempty"`
	SentAt             time.Time `json:"sent_at,omitempty"`
	CreatedAt          time.Time `json:"created_at,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
	RespondedAt        time.Time `json:"responded_at,omitempty"`
	ExpiresAt          time.Time `json:"expires_at,omitempty"`
	ReviewedAt         time.Time `json:"reviewed_at,omitempty"`
	Reviewed           bool      `json:"reviewed"`
}

type FailureFilter string

const (
	FailureAll       FailureFilter = "all"
	FailureFailed    FailureFilter = "failed"
	FailureRetryWait FailureFilter = "retry_wait"
)

func ParseFailureFilter(value string) FailureFilter {
	switch FailureFilter(strings.TrimSpace(value)) {
	case FailureAll:
		return FailureAll
	case FailureFailed:
		return FailureFailed
	case FailureRetryWait:
		return FailureRetryWait
	default:
		return FailureAll
	}
}

type CallbackRequest struct {
	AppointmentID, JobNumber, CustomerName, Locality, Phone string
	RespondedAt, ExpiresAt                                  time.Time
}

type AdminStore interface {
	ListAppointment(context.Context, string) ([]Status, error)
	ListFailed(context.Context, FailureFilter, int32) ([]Status, error)
	ListCallbacks(context.Context, int32) ([]CallbackRequest, error)
	Retry(context.Context, auth.Actor, string, string, time.Time) error
	Review(context.Context, auth.Actor, string, string, time.Time) error
	Reissue(context.Context, auth.Actor, string, int32, string, string, time.Time) error
	ResetResponse(context.Context, auth.Actor, string, int32, string, string, time.Time) error
}

func (service *AdminService) Reissue(ctx context.Context, actor auth.Actor, appointmentID string, expectedVersion int32, reason, requestID string) error {
	if err := actor.Require(auth.PermissionNotificationResend); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if strings.TrimSpace(appointmentID) == "" || expectedVersion < 1 || reason == "" || len(reason) > 500 {
		return ErrAdminActionUnavailable
	}
	return service.store.Reissue(ctx, actor, appointmentID, expectedVersion, reason, requestID, service.now().UTC())
}

func (service *AdminService) ResetResponse(ctx context.Context, actor auth.Actor, appointmentID string, expectedVersion int32, reason, requestID string) error {
	if err := actor.Require(auth.PermissionNotificationResend); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if strings.TrimSpace(appointmentID) == "" || expectedVersion < 1 || reason == "" || len(reason) > 500 {
		return ErrAdminActionUnavailable
	}
	return service.store.ResetResponse(ctx, actor, appointmentID, expectedVersion, reason, requestID, service.now().UTC())
}

type AdminService struct {
	store AdminStore
	now   func() time.Time
}

func NewAdminService(store AdminStore, now func() time.Time) (*AdminService, error) {
	if store == nil {
		return nil, errors.New("notification: admin store is required")
	}
	if now == nil {
		now = time.Now
	}
	return &AdminService{store: store, now: now}, nil
}

func (service *AdminService) AppointmentStatuses(ctx context.Context, actor auth.Actor, appointmentID string) ([]Status, error) {
	if err := actor.Require(auth.PermissionCalendarViewAll); err != nil {
		return nil, err
	}
	if strings.TrimSpace(appointmentID) == "" {
		return nil, ErrRetryUnavailable
	}
	values, err := service.store.ListAppointment(ctx, appointmentID)
	return prepareStatuses(values, false), err
}

func (service *AdminService) AdminAppointmentHistory(ctx context.Context, actor auth.Actor, appointmentID string) ([]Status, error) {
	if err := actor.Require(auth.PermissionNotificationResend); err != nil {
		return nil, err
	}
	if strings.TrimSpace(appointmentID) == "" {
		return nil, ErrRetryUnavailable
	}
	values, err := service.store.ListAppointment(ctx, appointmentID)
	return prepareStatuses(values, true), err
}

func (service *AdminService) Failed(ctx context.Context, actor auth.Actor, filter FailureFilter, limit int32) ([]Status, error) {
	if err := actor.Require(auth.PermissionNotificationResend); err != nil {
		return nil, err
	}
	filter = ParseFailureFilter(string(filter))
	if limit < 1 || limit > 200 {
		limit = 100
	}
	values, err := service.store.ListFailed(ctx, filter, limit)
	return prepareStatuses(values, true), err
}

func (service *AdminService) Callbacks(ctx context.Context, actor auth.Actor, limit int32) ([]CallbackRequest, error) {
	if err := actor.Require(auth.PermissionNotificationResend); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	values, err := service.store.ListCallbacks(ctx, limit)
	for index := range values {
		values[index].Phone = MaskRecipient(values[index].Phone, ChannelSMS)
	}
	return values, err
}

func (service *AdminService) Retry(ctx context.Context, actor auth.Actor, notificationID, requestID string) error {
	if err := actor.Require(auth.PermissionNotificationResend); err != nil {
		return err
	}
	if strings.TrimSpace(notificationID) == "" {
		return ErrRetryUnavailable
	}
	return service.store.Retry(ctx, actor, notificationID, requestID, service.now().UTC())
}

func (service *AdminService) Review(ctx context.Context, actor auth.Actor, notificationID, requestID string) error {
	if err := actor.Require(auth.PermissionNotificationResend); err != nil {
		return err
	}
	if strings.TrimSpace(notificationID) == "" {
		return ErrAdminActionUnavailable
	}
	return service.store.Review(ctx, actor, notificationID, requestID, service.now().UTC())
}

func (service *AdminService) CSV(ctx context.Context, actor auth.Actor, filter FailureFilter) ([]byte, error) {
	values, err := service.Failed(ctx, actor, filter, 200)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if err := writer.Write([]string{"Kanal", "Maskiertes Ziel", "Status", "Versuche", "Fehlercode", "Providerreferenz", "Erstellt UTC", "Nächster Versuch UTC", "Geprüft"}); err != nil {
		return nil, fmt.Errorf("notification: writing CSV header: %w", err)
	}
	for _, value := range values {
		record := []string{
			value.Channel, value.Recipient, value.State, fmt.Sprintf("%d/%d", value.AttemptCount, value.MaxAttempts),
			value.ErrorCode, value.ProviderReference, formatCSVTime(value.CreatedAt), formatCSVTime(value.AvailableAt), fmt.Sprint(value.Reviewed),
		}
		for index := range record {
			record[index] = spreadsheetSafe(record[index])
		}
		if err := writer.Write(record); err != nil {
			return nil, fmt.Errorf("notification: writing CSV row: %w", err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, fmt.Errorf("notification: finalizing CSV: %w", err)
	}
	return output.Bytes(), nil
}

func prepareStatuses(values []Status, includeProviderReference bool) []Status {
	result := make([]Status, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Recipient = MaskRecipient(value.Recipient, Channel(value.Channel))
		result[index].ErrorSummary, result[index].SuggestedAction = ErrorGuidance(value.ErrorCode)
		result[index].Reviewed = !value.ReviewedAt.IsZero()
		if includeProviderReference {
			result[index].ProviderReference = ShortProviderReference(value.ProviderReference)
		} else {
			result[index].ProviderReference = ""
		}
	}
	return result
}

func ShortProviderReference(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= 16 {
		return string(runes)
	}
	return string(runes[:8]) + "…" + string(runes[len(runes)-4:])
}

func ErrorGuidance(code string) (string, string) {
	switch code {
	case "provider_disabled":
		return "Versandkanal ist nicht aktiv", "Providerkonfiguration prüfen und danach erneut senden."
	case "provider_temporary":
		return "Provider vorübergehend nicht erreichbar", "Nächsten automatischen Versuch abwarten oder erneut einreihen."
	case "provider_permanent":
		return "Provider hat die Nachricht abgelehnt", "Kontaktdaten und Kanal prüfen; nicht unverändert wiederholen."
	case "delivery_uncertain":
		return "Versandergebnis ist unklar", "Beim Provider abgleichen, bevor erneut gesendet wird."
	case "confirmation_inactive":
		return "Bestätigungslink ist nicht mehr aktiv", "Terminstatus prüfen und bei Bedarf einen neuen Link erzeugen."
	case "token_key_unavailable", "template_invalid":
		return "Interne Versandvorbereitung fehlgeschlagen", "Konfiguration prüfen; bei Fortbestehen administrativ eskalieren."
	case "delivery_load_failed", "notification_state_failed", "notification_completion_failed":
		return "Versandstatus konnte nicht verarbeitet werden", "Worker- und Datenbankzustand prüfen."
	default:
		return "Technischer Versandfehler", "Status prüfen und nur bei aktivem Termin erneut einreihen."
	}
}

func spreadsheetSafe(value string) string {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	default:
		return value
	}
}

func formatCSVTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
