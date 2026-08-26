package postgres

import (
	"context"
	"errors"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/notification"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type NotificationWorkerStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

func NewNotificationWorkerStore(pool *pgxpool.Pool) *NotificationWorkerStore {
	return &NotificationWorkerStore{pool: pool, queries: dbgen.New(pool)}
}

func (store *NotificationWorkerStore) Claim(ctx context.Context, workerID string, now, leaseUntil time.Time, batchSize int32) ([]notification.ClaimedEvent, error) {
	rows, err := store.queries.ClaimNotificationOutbox(ctx, dbgen.ClaimNotificationOutboxParams{
		WorkerID: &workerID, LeaseUntil: timestamp(leaseUntil.UTC()), NowUtc: timestamp(now.UTC()), BatchSize: batchSize,
	})
	if err != nil {
		return nil, err
	}
	events := make([]notification.ClaimedEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, notification.ClaimedEvent{
			OutboxID: row.OID, NotificationID: row.NotificationID, IdempotencyKey: row.IdempotencyKey,
			Attempt: row.AttemptCount, MaxAttempts: row.MaxAttempts,
		})
	}
	return events, nil
}

func (store *NotificationWorkerStore) LoadDelivery(ctx context.Context, notificationID string) (notification.Delivery, error) {
	row, err := store.queries.GetNotificationDelivery(ctx, mustUUID(notificationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return notification.Delivery{}, notification.ErrConfirmationUnavailable
	}
	if err != nil {
		return notification.Delivery{}, err
	}
	return notification.Delivery{
		NotificationID: row.NID, AppointmentID: row.NAppointmentID, ConfirmationRequestID: row.NConfirmationRequestID,
		Channel: row.Channel, Recipient: row.RecipientSnapshot, TokenKeyID: row.TokenKeyID, TokenVersion: row.TokenVersion,
		ConfirmationStatus: row.ConfirmationRequestStatus, Lifecycle: row.LifecycleStatus,
		StartsAt: row.StartsAt.Time.UTC(), EndsAt: row.EndsAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC(),
		JobType: row.JobType, VolumeM3: row.VolumeM3, CustomerName: row.CustomerName,
	}, nil
}

func (store *NotificationWorkerStore) MarkSending(
	ctx context.Context,
	event notification.ClaimedEvent,
	workerID string,
	now, leaseUntil time.Time,
) error {
	return withQueries(ctx, store.pool, func(queries *dbgen.Queries) error {
		rows, err := queries.RenewNotificationLease(ctx, dbgen.RenewNotificationLeaseParams{
			LeaseUntil: timestamp(leaseUntil.UTC()), NowUtc: timestamp(now.UTC()), ID: mustUUID(event.OutboxID), WorkerID: &workerID,
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return errors.New("postgres: notification lease lost")
		}
		rows, err = queries.MarkNotificationSending(ctx, mustUUID(event.NotificationID))
		if err != nil {
			return err
		}
		if rows != 1 {
			return notification.ErrDeliveryUncertain
		}
		return nil
	})
}

func (store *NotificationWorkerStore) Complete(ctx context.Context, event notification.ClaimedEvent, workerID, providerID string) error {
	return withQueries(ctx, store.pool, func(queries *dbgen.Queries) error {
		rows, err := queries.MarkOutboxProcessed(ctx, dbgen.MarkOutboxProcessedParams{ID: mustUUID(event.OutboxID), WorkerID: &workerID})
		if err != nil {
			return err
		}
		if rows != 1 {
			return errors.New("postgres: notification lease lost")
		}
		return queries.MarkNotificationSent(ctx, dbgen.MarkNotificationSentParams{ProviderID: providerID, ID: mustUUID(event.NotificationID)})
	})
}

func (store *NotificationWorkerStore) Retry(ctx context.Context, event notification.ClaimedEvent, workerID string, availableAt time.Time, errorCode string) error {
	return withQueries(ctx, store.pool, func(queries *dbgen.Queries) error {
		rows, err := queries.MarkOutboxRetry(ctx, dbgen.MarkOutboxRetryParams{
			AvailableAt: timestamp(availableAt.UTC()), ErrorCode: &errorCode, ID: mustUUID(event.OutboxID), WorkerID: &workerID,
		})
		if err != nil {
			return err
		}
		if rows != 1 {
			return errors.New("postgres: notification lease lost")
		}
		return queries.MarkNotificationRetry(ctx, dbgen.MarkNotificationRetryParams{
			AvailableAt: timestamp(availableAt.UTC()), ErrorCode: &errorCode, ID: mustUUID(event.NotificationID),
		})
	})
}

func (store *NotificationWorkerStore) Dead(ctx context.Context, event notification.ClaimedEvent, workerID, errorCode string) error {
	return withQueries(ctx, store.pool, func(queries *dbgen.Queries) error {
		rows, err := queries.MarkOutboxDead(ctx, dbgen.MarkOutboxDeadParams{ErrorCode: &errorCode, ID: mustUUID(event.OutboxID), WorkerID: &workerID})
		if err != nil {
			return err
		}
		if rows != 1 {
			return errors.New("postgres: notification lease lost")
		}
		return queries.MarkNotificationFailed(ctx, dbgen.MarkNotificationFailedParams{ErrorCode: &errorCode, ID: mustUUID(event.NotificationID)})
	})
}
