package notification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
)

type ClaimedEvent struct {
	OutboxID, NotificationID, IdempotencyKey string
	Attempt, MaxAttempts                     int32
}

type Delivery struct {
	NotificationID, AppointmentID, ConfirmationRequestID string
	Channel, Recipient, TokenKeyID                       string
	TokenVersion                                         int32
	ConfirmationStatus, Lifecycle                        string
	StartsAt, EndsAt, ExpiresAt                          time.Time
	JobType, VolumeM3, CustomerName                      string
}

type WorkerStore interface {
	Claim(context.Context, string, time.Time, time.Time, int32) ([]ClaimedEvent, error)
	LoadDelivery(context.Context, string) (Delivery, error)
	MarkSending(context.Context, ClaimedEvent, string, time.Time, time.Time) error
	Complete(context.Context, ClaimedEvent, string, string) error
	Retry(context.Context, ClaimedEvent, string, time.Time, string) error
	Dead(context.Context, ClaimedEvent, string, string) error
}

type IdentityEmailEvent struct {
	OutboxID, VerificationID, IdempotencyKey string
	Attempt, MaxAttempts                     int32
}

type IdentityEmailDelivery struct {
	VerificationID, UserID, Recipient, DisplayName, TokenKeyID, Status string
	TokenVersion                                                       int32
	ExpiresAt                                                          time.Time
}

type IdentityEmailWorkerStore interface {
	ClaimIdentityEmail(context.Context, string, time.Time, time.Time, int32) ([]IdentityEmailEvent, error)
	LoadIdentityEmail(context.Context, string) (IdentityEmailDelivery, error)
	MarkIdentityEmailSending(context.Context, IdentityEmailEvent, string, time.Time, time.Time) error
	CompleteIdentityEmail(context.Context, IdentityEmailEvent, string) error
	RetryIdentityEmail(context.Context, IdentityEmailEvent, string, time.Time, string) error
	DeadIdentityEmail(context.Context, IdentityEmailEvent, string, string) error
}

type Processor struct {
	store          WorkerStore
	providers      map[Channel]Provider
	tokens         *KeyRing
	baseURL        string
	location       *time.Location
	businessName   string
	businessAddr   string
	businessPhone  string
	workerID       string
	lease          time.Duration
	batchSize      int32
	now            func() time.Time
	logger         *slog.Logger
	identityEmails IdentityEmailWorkerStore
	securityKeys   *auth.SecurityKeyRing
}

// ConfigureIdentityEmail enables transactional verification-mail delivery on the existing worker.
func (processor *Processor) ConfigureIdentityEmail(store IdentityEmailWorkerStore, keys *auth.SecurityKeyRing) error {
	if store == nil || keys == nil {
		return errors.New("notification: invalid identity email dependencies")
	}
	processor.identityEmails = store
	processor.securityKeys = keys
	return nil
}

type ProcessorConfig struct {
	BaseURL, BusinessName, BusinessAddress, BusinessPhone string
	WorkerID                                              string
	Lease                                                 time.Duration
	BatchSize                                             int32
}

func NewProcessor(store WorkerStore, providers map[Channel]Provider, tokens *KeyRing, location *time.Location, cfg ProcessorConfig, now func() time.Time, logger *slog.Logger) (*Processor, error) {
	if store == nil || tokens == nil || location == nil || cfg.Lease <= 0 || cfg.BatchSize < 1 || strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("notification: invalid processor dependencies")
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = NewWorkerID()
	}
	return &Processor{
		store: store, providers: providers, tokens: tokens, baseURL: strings.TrimRight(cfg.BaseURL, "/"), location: location,
		businessName: cfg.BusinessName, businessAddr: cfg.BusinessAddress, businessPhone: cfg.BusinessPhone,
		workerID: cfg.WorkerID, lease: cfg.Lease, batchSize: cfg.BatchSize, now: now, logger: logger,
	}, nil
}

func NewWorkerID() string {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "worker"
	}
	return "worker-" + hex.EncodeToString(value)
}

func (processor *Processor) RunOnce(ctx context.Context) (int, error) {
	processed := 0
	preferIdentity := processor.identityEmails != nil
	for processed < int(processor.batchSize) {
		now := processor.now().UTC()
		if preferIdentity && processor.identityEmails != nil {
			events, err := processor.identityEmails.ClaimIdentityEmail(ctx, processor.workerID, now, now.Add(processor.lease), 1)
			if err != nil {
				return processed, err
			}
			if len(events) > 0 {
				processor.processIdentityEmail(ctx, events[0], now)
				processed++
				preferIdentity = false
				continue
			}
		}
		events, err := processor.store.Claim(ctx, processor.workerID, now, now.Add(processor.lease), 1)
		if err != nil {
			return processed, err
		}
		if len(events) == 0 {
			if !preferIdentity && processor.identityEmails != nil {
				preferIdentity = true
				continue
			}
			break
		}
		processor.process(ctx, events[0], now)
		processed++
		preferIdentity = true
	}
	return processed, nil
}

func (processor *Processor) processIdentityEmail(ctx context.Context, event IdentityEmailEvent, now time.Time) {
	delivery, err := processor.identityEmails.LoadIdentityEmail(ctx, event.VerificationID)
	if err != nil {
		processor.failIdentityEmail(ctx, event, now, "identity_email_load_failed", err)
		return
	}
	if delivery.Status != "pending" || !now.Before(delivery.ExpiresAt) {
		processor.deadIdentityEmail(ctx, event, "identity_email_inactive")
		return
	}
	rawToken, err := processor.securityKeys.ReconstructEmailToken(delivery.TokenKeyID, delivery.VerificationID, delivery.UserID, delivery.TokenVersion)
	if err != nil {
		processor.deadIdentityEmail(ctx, event, "identity_email_key_unavailable")
		return
	}
	provider := processor.providers[ChannelEmail]
	if provider == nil {
		processor.failIdentityEmail(ctx, event, now, "provider_disabled", ErrTemporary)
		return
	}
	markNow := processor.now().UTC()
	if err := processor.identityEmails.MarkIdentityEmailSending(ctx, event, processor.workerID, markNow, markNow.Add(processor.lease)); err != nil {
		processor.failIdentityEmail(ctx, event, now, "identity_email_lease_lost", err)
		return
	}
	verificationURL := processor.baseURL + "/profil/email/bestaetigen?token=" + url.QueryEscape(rawToken)
	name := strings.TrimSpace(delivery.DisplayName)
	if name == "" {
		name = "HackWerk-Nutzer"
	}
	subject := "E-Mail-Adresse für HackWerk bestätigen"
	textBody := fmt.Sprintf("Hallo %s,\n\nbestätigen Sie Ihre neue E-Mail-Adresse über diesen Link:\n%s\n\nDer Link ist zeitlich begrenzt. Wenn Sie die Änderung nicht angefordert haben, ignorieren Sie diese Nachricht.\n", name, verificationURL)
	htmlBody := fmt.Sprintf("<p>Hallo %s,</p><p>Bestätigen Sie Ihre neue E-Mail-Adresse:</p><p><a href=\"%s\">E-Mail-Adresse bestätigen</a></p><p>Der Link ist zeitlich begrenzt. Wenn Sie die Änderung nicht angefordert haben, ignorieren Sie diese Nachricht.</p>", html.EscapeString(name), html.EscapeString(verificationURL))
	_, sendErr := provider.Send(ctx, Message{
		NotificationID: delivery.VerificationID, IdempotencyKey: event.IdempotencyKey, Channel: ChannelEmail,
		Recipient: delivery.Recipient, Subject: subject, Text: textBody, HTML: htmlBody,
	})
	if sendErr != nil {
		code := "provider_temporary"
		if errors.Is(sendErr, ErrPermanent) {
			code = "provider_permanent"
		}
		processor.failIdentityEmail(ctx, event, now, code, sendErr)
		return
	}
	if err := processor.identityEmails.CompleteIdentityEmail(ctx, event, processor.workerID); err != nil {
		processor.logger.WarnContext(ctx, "identity email completion failed", slog.String("error_code", "identity_email_completion_failed"))
	}
}

func (processor *Processor) failIdentityEmail(ctx context.Context, event IdentityEmailEvent, now time.Time, code string, cause error) {
	permanent := errors.Is(cause, ErrPermanent) || event.Attempt >= event.MaxAttempts
	var stateErr error
	if permanent {
		stateErr = processor.identityEmails.DeadIdentityEmail(ctx, event, processor.workerID, code)
	} else {
		stateErr = processor.identityEmails.RetryIdentityEmail(ctx, event, processor.workerID, now.Add(Backoff(int(event.Attempt), event.IdempotencyKey)), code)
	}
	if stateErr != nil {
		processor.logger.ErrorContext(ctx, "identity email state could not be persisted", slog.String("error_code", "identity_email_state_failed"))
	}
	processor.logger.WarnContext(ctx, "identity email delivery failed", slog.String("error_code", code), slog.Int("attempt", int(event.Attempt)))
}

func (processor *Processor) deadIdentityEmail(ctx context.Context, event IdentityEmailEvent, code string) {
	if err := processor.identityEmails.DeadIdentityEmail(ctx, event, processor.workerID, code); err != nil {
		processor.logger.ErrorContext(ctx, "identity email dead state could not be persisted", slog.String("error_code", "identity_email_dead_state_failed"))
	}
}

func (processor *Processor) process(ctx context.Context, event ClaimedEvent, now time.Time) {
	delivery, err := processor.store.LoadDelivery(ctx, event.NotificationID)
	if err != nil {
		processor.fail(ctx, event, now, "delivery_load_failed", err)
		return
	}
	if delivery.ConfirmationStatus != "active" || delivery.Lifecycle != "fixed" || !now.Before(delivery.ExpiresAt) {
		processor.markDead(ctx, event, "confirmation_inactive")
		return
	}
	material, err := processor.tokens.Reconstruct(delivery.TokenKeyID, delivery.ConfirmationRequestID, delivery.AppointmentID, delivery.TokenVersion)
	if err != nil {
		processor.markDead(ctx, event, "token_key_unavailable")
		return
	}
	rendered, err := Render(TemplateInput{
		CustomerName: delivery.CustomerName, JobType: delivery.JobType, VolumeM3: delivery.VolumeM3,
		StartsAt: delivery.StartsAt, EndsAt: delivery.EndsAt,
		ConfirmationURL: processor.baseURL + "/termin/" + material.Raw,
		BusinessName:    processor.businessName, BusinessAddress: processor.businessAddr, BusinessPhone: processor.businessPhone,
	}, processor.location)
	if err != nil {
		processor.markDead(ctx, event, "template_invalid")
		return
	}
	channel := Channel(delivery.Channel)
	provider := processor.providers[channel]
	if provider == nil {
		processor.fail(ctx, event, now, "provider_disabled", ErrTemporary)
		return
	}
	markNow := processor.now().UTC()
	if err := processor.store.MarkSending(ctx, event, processor.workerID, markNow, markNow.Add(processor.lease)); err != nil {
		if errors.Is(err, ErrDeliveryUncertain) {
			processor.markDead(ctx, event, "delivery_uncertain")
			processor.logger.WarnContext(ctx, "notification delivery requires reconciliation", slog.String("error_code", "delivery_uncertain"), slog.String("channel", delivery.Channel))
			return
		}
		processor.fail(ctx, event, now, "notification_state_failed", err)
		return
	}
	message := Message{
		NotificationID: delivery.NotificationID, IdempotencyKey: event.IdempotencyKey, Channel: channel, Recipient: delivery.Recipient,
		Subject: rendered.Subject, Text: rendered.Text, HTML: rendered.HTML,
	}
	if channel == ChannelSMS {
		message.Text, message.HTML = rendered.SMS, ""
	}
	providerID, sendErr := provider.Send(ctx, message)
	if sendErr != nil {
		code := "provider_temporary"
		if errors.Is(sendErr, ErrPermanent) {
			code = "provider_permanent"
		}
		processor.fail(ctx, event, now, code, sendErr)
		return
	}
	if err := processor.store.Complete(ctx, event, processor.workerID, providerID); err != nil {
		processor.logger.WarnContext(ctx, "notification completion failed", slog.String("error_code", "notification_completion_failed"), slog.String("channel", string(channel)))
	}
}

func (processor *Processor) fail(ctx context.Context, event ClaimedEvent, now time.Time, code string, cause error) {
	permanent := errors.Is(cause, ErrPermanent) || event.Attempt >= event.MaxAttempts
	var stateErr error
	if permanent {
		stateErr = processor.store.Dead(ctx, event, processor.workerID, code)
	} else {
		stateErr = processor.store.Retry(ctx, event, processor.workerID, now.Add(Backoff(int(event.Attempt), event.IdempotencyKey)), code)
	}
	if stateErr != nil {
		processor.logger.ErrorContext(ctx, "notification failure state could not be persisted", slog.String("error_code", "notification_state_persist_failed"))
	}
	processor.logger.WarnContext(ctx, "notification delivery failed", slog.String("error_code", code), slog.Int("attempt", int(event.Attempt)))
}

func (processor *Processor) markDead(ctx context.Context, event ClaimedEvent, code string) {
	if err := processor.store.Dead(ctx, event, processor.workerID, code); err != nil {
		processor.logger.ErrorContext(ctx, "notification dead state could not be persisted", slog.String("error_code", "notification_state_persist_failed"))
	}
}
