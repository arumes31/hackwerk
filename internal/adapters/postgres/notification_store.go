package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/notification"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type NotificationStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
	planner *AppointmentStore
}

type NotificationStoreOption func(*NotificationStore)

func WithNotificationPlanning(tokens *notification.KeyRing, ttl time.Duration, mailMaxAttempts, smsMaxAttempts int, mailEnabled, smsEnabled bool) NotificationStoreOption {
	return func(store *NotificationStore) {
		store.planner = NewAppointmentStore(store.pool, WithConfirmationPlanning(tokens, ttl, mailMaxAttempts, smsMaxAttempts, mailEnabled, smsEnabled))
	}
}

func NewNotificationStore(pool *pgxpool.Pool, options ...NotificationStoreOption) *NotificationStore {
	store := &NotificationStore{pool: pool, queries: dbgen.New(pool), planner: NewAppointmentStore(pool)}
	for _, option := range options {
		option(store)
	}
	return store
}

func (store *NotificationStore) Reissue(ctx context.Context, actor auth.Actor, appointmentID string, expectedVersion int32, reason, requestID string, now time.Time) error {
	return withQueries(ctx, store.pool, func(queries *dbgen.Queries) error {
		id := mustUUID(appointmentID)
		current, err := queries.GetAppointmentForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notification.ErrAdminActionUnavailable
		}
		if err != nil {
			return err
		}
		if current.LifecycleStatus != "fixed" || current.Version != expectedVersion {
			return notification.ErrAdminActionUnavailable
		}
		rows, err := queries.BumpAppointmentVersion(ctx, dbgen.BumpAppointmentVersionParams{ID: id, ExpectedVersion: expectedVersion})
		if err != nil {
			return err
		}
		if rows != 1 {
			return notification.ErrAdminActionUnavailable
		}
		if err := store.planner.planConfirmationAt(ctx, queries, id, "", "admin reissued confirmation", now); err != nil {
			if errors.Is(err, appointment.ErrNotification) {
				return notification.ErrAdminActionUnavailable
			}
			return err
		}
		metadata, _ := json.Marshal(map[string]any{
			"changed_fields":  []string{"confirmation_status", "confirmation_request"},
			"reason_category": "manual_admin_action", "reason_provided": reason != "",
		})
		return queries.InsertAuditEvent(ctx, dbgen.InsertAuditEventParams{
			ActorType: "user", ActorUserID: actor.UserID, Action: "confirmation.reissued", ObjectType: "appointment",
			ObjectID: appointmentID, RequestID: requestID, Metadata: metadata,
		})
	})
}

func (store *NotificationStore) ResetResponse(ctx context.Context, actor auth.Actor, appointmentID string, expectedVersion int32, reason, requestID string, now time.Time) error {
	return withQueries(ctx, store.pool, func(queries *dbgen.Queries) error {
		id := mustUUID(appointmentID)
		if _, err := queries.LockAppointmentForConfirmation(ctx, id); err != nil {
			return notification.ErrAdminActionUnavailable
		}
		current, err := queries.GetActiveConfirmationForUpdate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return notification.ErrAdminActionUnavailable
		}
		if err != nil {
			return err
		}
		if current.LifecycleStatus != "fixed" || current.Version != expectedVersion || current.Response == "" || !now.Before(current.ExpiresAt.Time) {
			return notification.ErrAdminActionUnavailable
		}
		rows, err := queries.ResetConfirmationResponse(ctx, mustUUID(current.CrID))
		if err != nil {
			return err
		}
		if rows != 1 {
			return notification.ErrAdminActionUnavailable
		}
		if err := queries.SetAppointmentConfirmation(ctx, dbgen.SetAppointmentConfirmationParams{ConfirmationStatus: "pending", AppointmentID: id}); err != nil {
			return err
		}
		metadata, _ := json.Marshal(map[string]any{
			"changed_fields":  []string{"confirmation_status"},
			"reason_category": "manual_admin_action", "reason_provided": reason != "",
		})
		return queries.InsertAuditEvent(ctx, dbgen.InsertAuditEventParams{
			ActorType: "user", ActorUserID: actor.UserID, Action: "confirmation.response_reset", ObjectType: "appointment",
			ObjectID: appointmentID, RequestID: requestID, Metadata: metadata,
		})
	})
}

func (store *NotificationStore) Lookup(ctx context.Context, tokenHash []byte) (notification.Confirmation, error) {
	row, err := store.queries.GetConfirmationByTokenHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return notification.Confirmation{}, notification.ErrConfirmationUnavailable
	}
	if err != nil {
		return notification.Confirmation{}, err
	}
	return notification.Confirmation{
		RequestID: row.CrID, AppointmentID: row.CrAppointmentID, CustomerName: row.CustomerName, Locality: row.Locality,
		JobNumber: row.JobNumber, JobType: row.JobType, VolumeM3: row.JVolumeM3,
		TokenKeyID: row.TokenKeyID, TokenVersion: row.TokenVersion, AppointmentVersion: row.Version,
		Status: row.Status, Lifecycle: row.LifecycleStatus, ConfirmationStatus: row.ConfirmationStatus,
		Response: notification.Response(row.Response), StartsAt: row.StartsAt.Time.UTC(), EndsAt: row.EndsAt.Time.UTC(),
		ExpiresAt: row.ExpiresAt.Time.UTC(), TokenHash: append([]byte(nil), row.TokenHash...), FormNonceHash: append([]byte(nil), row.FormNonceHash...),
	}, nil
}

func (store *NotificationStore) Respond(
	ctx context.Context,
	tokenHash, nonceHash []byte,
	response notification.Response,
	requestID string,
	now time.Time,
) (notification.Confirmation, error) {
	var result notification.Confirmation
	err := withQueries(ctx, store.pool, func(queries *dbgen.Queries) error {
		appointmentIDText, lookupErr := queries.GetConfirmationAppointmentID(ctx, tokenHash)
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return notification.ErrConfirmationUnavailable
		}
		if lookupErr != nil {
			return lookupErr
		}
		if _, lockErr := queries.LockAppointmentForConfirmation(ctx, mustUUID(appointmentIDText)); lockErr != nil {
			return notification.ErrConfirmationUnavailable
		}
		row, err := queries.GetConfirmationForUpdate(ctx, tokenHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return notification.ErrConfirmationUnavailable
		}
		if err != nil {
			return err
		}
		if row.Status != "active" || row.LifecycleStatus != "fixed" || !now.Before(row.ExpiresAt.Time) ||
			!notification.ConstantTimeEqual(row.TokenHash, tokenHash) || !notification.ConstantTimeEqual(row.FormNonceHash, nonceHash) {
			return notification.ErrConfirmationUnavailable
		}
		current := notification.Response(row.Response)
		if current == response {
			result = notification.Confirmation{RequestID: row.CrID, AppointmentID: row.CrAppointmentID, Response: current, Status: row.Status, Lifecycle: row.LifecycleStatus, ConfirmationStatus: row.ConfirmationStatus, AppointmentVersion: row.Version, ExpiresAt: row.ExpiresAt.Time.UTC()}
			return nil
		}
		if current == notification.ResponseConfirmed || current == notification.ResponseDeclined || (current == notification.ResponseCallback && response == notification.ResponseCallback) {
			return notification.ErrResponseLocked
		}
		responseValue := string(response)
		rows, err := queries.SetConfirmationResponse(ctx, dbgen.SetConfirmationResponseParams{Response: &responseValue, ID: mustUUID(row.CrID)})
		if err != nil {
			return err
		}
		if rows != 1 {
			return notification.ErrResponseLocked
		}
		appointmentID := mustUUID(row.CrAppointmentID)
		if err := queries.SetAppointmentConfirmation(ctx, dbgen.SetAppointmentConfirmationParams{ConfirmationStatus: string(response), AppointmentID: appointmentID}); err != nil {
			return err
		}
		if err := queries.InsertConfirmationRespondedEvent(ctx, dbgen.InsertConfirmationRespondedEventParams{
			AppointmentID: appointmentID, Response: string(response), ConfirmationRequestID: row.CrID,
		}); err != nil {
			return err
		}
		metadata, _ := json.Marshal(map[string][]string{"changed_fields": {"confirmation_status"}})
		if err := queries.InsertAuditEvent(ctx, dbgen.InsertAuditEventParams{
			ActorType: "public", ActorUserID: "", Action: "confirmation.responded", ObjectType: "appointment",
			ObjectID: row.CrAppointmentID, RequestID: requestID, Metadata: metadata,
		}); err != nil {
			return err
		}
		result = notification.Confirmation{RequestID: row.CrID, AppointmentID: row.CrAppointmentID, Response: response, Status: row.Status, Lifecycle: row.LifecycleStatus, ConfirmationStatus: string(response), AppointmentVersion: row.Version + 1, ExpiresAt: row.ExpiresAt.Time.UTC()}
		return nil
	})
	return result, err
}

func (store *NotificationStore) ListAppointment(ctx context.Context, appointmentID string) ([]notification.Status, error) {
	rows, err := store.queries.ListAppointmentNotifications(ctx, mustUUID(appointmentID))
	if err != nil {
		return nil, err
	}
	values := make([]notification.Status, 0, len(rows))
	for _, row := range rows {
		values = append(values, notification.Status{
			ID: row.NID, AppointmentID: appointmentID, Channel: row.Channel, State: row.Status,
			Recipient: row.RecipientSnapshot, ErrorCode: row.LastErrorCode,
			ProviderReference: row.ProviderID, ConfirmationStatus: row.ConfirmationRequestStatus, Response: row.Response,
			AttemptCount: row.AttemptCount, MaxAttempts: row.MaxAttempts,
			AvailableAt: timestampValue(row.AvailableAt), SentAt: timestampValue(row.SentAt),
			CreatedAt: timestampValue(row.CreatedAt), UpdatedAt: timestampValue(row.UpdatedAt),
			RespondedAt: timestampValue(row.RespondedAt), ExpiresAt: timestampValue(row.ExpiresAt), ReviewedAt: timestampValue(row.ReviewedAt),
		})
	}
	return values, nil
}

func (store *NotificationStore) ListFailed(ctx context.Context, filter notification.FailureFilter, limit int32) ([]notification.Status, error) {
	rows, err := store.queries.ListFailedNotifications(ctx, dbgen.ListFailedNotificationsParams{StatusFilter: string(filter), ResultLimit: limit})
	if err != nil {
		return nil, err
	}
	values := make([]notification.Status, 0, len(rows))
	for _, row := range rows {
		values = append(values, notification.Status{
			ID: row.NID, AppointmentID: row.NAppointmentID, Channel: row.Channel, State: row.Status,
			Recipient: row.RecipientSnapshot, ErrorCode: row.LastErrorCode,
			ProviderReference: row.ProviderID, ConfirmationStatus: row.ConfirmationRequestStatus, Response: row.Response,
			AttemptCount: row.AttemptCount, MaxAttempts: row.MaxAttempts,
			AvailableAt: timestampValue(row.AvailableAt), SentAt: timestampValue(row.SentAt),
			CreatedAt: timestampValue(row.CreatedAt), UpdatedAt: timestampValue(row.UpdatedAt),
			RespondedAt: timestampValue(row.RespondedAt), ExpiresAt: timestampValue(row.ExpiresAt), ReviewedAt: timestampValue(row.ReviewedAt),
		})
	}
	return values, nil
}

func (store *NotificationStore) ListCallbacks(ctx context.Context, limit int32) ([]notification.CallbackRequest, error) {
	rows, err := store.queries.ListCallbackRequests(ctx, limit)
	if err != nil {
		return nil, err
	}
	values := make([]notification.CallbackRequest, 0, len(rows))
	for _, row := range rows {
		values = append(values, notification.CallbackRequest{
			AppointmentID: row.AppointmentID, JobNumber: row.JobNumber, CustomerName: row.CustomerName,
			Locality: row.Locality, Phone: row.Phone, RespondedAt: timestampValue(row.RespondedAt), ExpiresAt: timestampValue(row.ExpiresAt),
		})
	}
	return values, nil
}

func (store *NotificationStore) Retry(ctx context.Context, actor auth.Actor, notificationID, requestID string, now time.Time) error {
	return withQueries(ctx, store.pool, func(queries *dbgen.Queries) error {
		delivery, err := queries.GetNotificationDelivery(ctx, mustUUID(notificationID))
		if errors.Is(err, pgx.ErrNoRows) {
			return notification.ErrRetryUnavailable
		}
		if err != nil {
			return err
		}
		if delivery.ConfirmationRequestStatus != "active" || delivery.LifecycleStatus != "fixed" || !now.Before(delivery.ExpiresAt.Time) {
			return notification.ErrRetryUnavailable
		}
		notificationRows, err := queries.RequeueNotification(ctx, mustUUID(notificationID))
		if err != nil {
			return err
		}
		outboxRows, err := queries.RequeueNotificationOutbox(ctx, mustUUID(notificationID))
		if err != nil {
			return err
		}
		if notificationRows != 1 || outboxRows != 1 {
			return notification.ErrRetryUnavailable
		}
		metadata, _ := json.Marshal(map[string][]string{"changed_fields": {"notification_status"}})
		return queries.InsertAuditEvent(ctx, dbgen.InsertAuditEventParams{
			ActorType: "user", ActorUserID: actor.UserID, Action: "notification.requeued", ObjectType: "appointment",
			ObjectID: delivery.NAppointmentID, RequestID: requestID, Metadata: metadata,
		})
	})
}

func (store *NotificationStore) Review(ctx context.Context, actor auth.Actor, notificationID, requestID string, now time.Time) error {
	return withQueries(ctx, store.pool, func(queries *dbgen.Queries) error {
		appointmentID, err := queries.MarkNotificationReviewed(ctx, dbgen.MarkNotificationReviewedParams{
			ReviewedAt: timestamp(now.UTC()), ReviewedByUserID: mustUUID(actor.UserID), ID: mustUUID(notificationID),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return notification.ErrAdminActionUnavailable
		}
		if err != nil {
			return err
		}
		metadata, _ := json.Marshal(map[string][]string{"changed_fields": {"notification_reviewed_at"}})
		return queries.InsertAuditEvent(ctx, dbgen.InsertAuditEventParams{
			ActorType: "user", ActorUserID: actor.UserID, Action: "notification.reviewed", ObjectType: "appointment",
			ObjectID: appointmentID, RequestID: requestID, Metadata: metadata,
		})
	})
}
