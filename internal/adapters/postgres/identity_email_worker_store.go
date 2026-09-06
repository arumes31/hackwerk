package postgres

import (
	"context"
	"errors"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/notification"
)

func (store *NotificationWorkerStore) ClaimIdentityEmail(ctx context.Context, workerID string, now, leaseUntil time.Time, batchSize int32) ([]notification.IdentityEmailEvent, error) {
	rows, err := store.queries.ClaimIdentityEmailOutbox(ctx, dbgen.ClaimIdentityEmailOutboxParams{
		WorkerID: &workerID, LeaseUntil: timestamp(leaseUntil.UTC()), NowUtc: timestamp(now.UTC()), BatchSize: batchSize,
	})
	if err != nil {
		return nil, err
	}
	events := make([]notification.IdentityEmailEvent, 0, len(rows))
	for _, row := range rows {
		events = append(events, notification.IdentityEmailEvent{
			OutboxID: row.OID, VerificationID: row.VerificationID, IdempotencyKey: row.IdempotencyKey,
			Attempt: row.AttemptCount, MaxAttempts: row.MaxAttempts,
		})
	}
	return events, nil
}

func (store *NotificationWorkerStore) LoadIdentityEmail(ctx context.Context, verificationID string) (notification.IdentityEmailDelivery, error) {
	row, err := store.queries.GetIdentityEmailDelivery(ctx, mustUUID(verificationID))
	if err != nil {
		return notification.IdentityEmailDelivery{}, err
	}
	return notification.IdentityEmailDelivery{
		VerificationID: row.EvID, UserID: row.EvUserID, Recipient: row.EvEmail, DisplayName: row.DisplayName,
		TokenKeyID: row.TokenKeyID, TokenVersion: row.TokenVersion, Status: row.Status, ExpiresAt: row.ExpiresAt.Time.UTC(),
	}, nil
}

func (store *NotificationWorkerStore) MarkIdentityEmailSending(ctx context.Context, event notification.IdentityEmailEvent, workerID string, now, leaseUntil time.Time) error {
	rows, err := store.queries.RenewNotificationLease(ctx, dbgen.RenewNotificationLeaseParams{
		LeaseUntil: timestamp(leaseUntil.UTC()), NowUtc: timestamp(now.UTC()), ID: mustUUID(event.OutboxID), WorkerID: &workerID,
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return notification.ErrDeliveryUncertain
	}
	return nil
}

func (store *NotificationWorkerStore) CompleteIdentityEmail(ctx context.Context, event notification.IdentityEmailEvent, workerID string) error {
	rows, err := store.queries.MarkOutboxProcessed(ctx, dbgen.MarkOutboxProcessedParams{ID: mustUUID(event.OutboxID), WorkerID: &workerID})
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("postgres: identity email lease lost")
	}
	return nil
}

func (store *NotificationWorkerStore) RetryIdentityEmail(ctx context.Context, event notification.IdentityEmailEvent, workerID string, availableAt time.Time, errorCode string) error {
	rows, err := store.queries.MarkOutboxRetry(ctx, dbgen.MarkOutboxRetryParams{
		AvailableAt: timestamp(availableAt.UTC()), ErrorCode: &errorCode, ID: mustUUID(event.OutboxID), WorkerID: &workerID,
	})
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("postgres: identity email lease lost")
	}
	return nil
}

func (store *NotificationWorkerStore) DeadIdentityEmail(ctx context.Context, event notification.IdentityEmailEvent, workerID, errorCode string) error {
	rows, err := store.queries.MarkOutboxDead(ctx, dbgen.MarkOutboxDeadParams{ErrorCode: &errorCode, ID: mustUUID(event.OutboxID), WorkerID: &workerID})
	if err != nil {
		return err
	}
	if rows != 1 {
		return errors.New("postgres: identity email lease lost")
	}
	return nil
}

var _ notification.IdentityEmailWorkerStore = (*NotificationWorkerStore)(nil)
