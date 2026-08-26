// Package voice implements short-lived, human-reviewed speech intake drafts.
package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/customers"
)

var (
	ErrDisabled   = errors.New("voice: disabled")
	ErrNotFound   = errors.New("voice: not found")
	ErrConflict   = errors.New("voice: conflict")
	ErrValidation = errors.New("voice: validation failed")
	ErrRateLimit  = errors.New("voice: rate limited")
	ErrProvider   = errors.New("voice: provider unavailable")
)

type Status string

const (
	StatusTranscribing Status = "transcribing"
	StatusNeedsReview  Status = "needs_review"
	StatusFailed       Status = "failed"
	StatusCommitted    Status = "committed"
	StatusExpired      Status = "expired"
)

type Field struct {
	Value      string   `json:"value"`
	Source     string   `json:"source,omitempty"`
	Confidence float64  `json:"confidence"`
	Warnings   []string `json:"warnings,omitempty"`
}

type Fields struct {
	FirstName                 Field `json:"first_name"`
	LastName                  Field `json:"last_name"`
	CompanyName               Field `json:"company_name"`
	AddressFreeform           Field `json:"address_freeform"`
	PhoneRaw                  Field `json:"phone_raw"`
	Email                     Field `json:"email"`
	VolumeM3                  Field `json:"volume_m3"`
	EstimatedHackMinutes      Field `json:"estimated_hack_minutes"`
	EstimatedTransportMinutes Field `json:"estimated_transport_minutes"`
	TransportTripCount        Field `json:"transport_trip_count"`
	TransportMode             Field `json:"transport_mode"`
	PreferenceText            Field `json:"preference_text"`
	PreferredStartDate        Field `json:"preferred_start_date"`
	PreferredEndDate          Field `json:"preferred_end_date"`
	Urgency                   Field `json:"urgency"`
	Region                    Field `json:"region"`
	Note                      Field `json:"note"`
}

type Draft struct {
	ID, OwnerUserID, Transcript, ProviderName, ProviderVersion, ParserVersion, FailureCode string
	Status                                                                                 Status
	Fields                                                                                 Fields
	Warnings                                                                               []string
	OverallConfidence                                                                      float64
	RetryCount, Version                                                                    int32
	Committed                                                                              customers.CreatedIntake
	CreatedAt, UpdatedAt, ExpiresAt                                                        time.Time
}

type Audio struct {
	Reader      io.Reader
	Filename    string
	ContentType string
	Size        int64
}

type Metadata struct {
	RecordedAt time.Time
	Duration   time.Duration
}

type Transcript struct {
	Text, Provider, Version string
	Confidence              float64
}

type Transcriber interface {
	Transcribe(context.Context, Audio, string, Metadata) (Transcript, error)
}

type Extractor interface {
	Extract(context.Context, string, time.Time, *time.Location) (Fields, []string, float64)
	Version() string
}

type Store interface {
	Create(context.Context, auth.Actor, time.Time) (Draft, error)
	Complete(context.Context, auth.Actor, string, Transcript, Fields, []string, float64, string) error
	Fail(context.Context, auth.Actor, string, string) error
	Get(context.Context, auth.Actor, string) (Draft, error)
	FindDuplicates(context.Context, customers.CustomerInput) ([]customers.Duplicate, error)
	Commit(context.Context, auth.Actor, CommitInput) (customers.CreatedIntake, error)
	Discard(context.Context, auth.Actor, string) error
	Cleanup(context.Context) (int64, error)
}

type Config struct {
	Enabled            bool
	Retention          time.Duration
	RateLimitPerMinute int
	ConcurrentPerUser  int
	Timezone           *time.Location
}

type CommitInput struct {
	DraftID, ExistingCustomerID, RequestID string
	ExpectedVersion                        int32
	Reviewed                               bool
	DuplicateReviewed                      bool
	Intake                                 customers.IntakeInput
}

type Service struct {
	store       Store
	transcriber Transcriber
	extractor   Extractor
	config      Config
	now         func() time.Time
	limiter     *userLimiter
	observer    Observer
}

type Observer interface{ ObserveVoice(string, time.Duration) }
type Option func(*Service)

func WithObserver(observer Observer) Option {
	return func(service *Service) { service.observer = observer }
}

func New(store Store, transcriber Transcriber, extractor Extractor, cfg Config, now func() time.Time, options ...Option) (*Service, error) {
	if store == nil || transcriber == nil || extractor == nil || cfg.Timezone == nil || now == nil {
		return nil, errors.New("voice: missing dependency")
	}
	if cfg.Retention < 5*time.Minute || cfg.Retention > 7*24*time.Hour || cfg.RateLimitPerMinute < 1 || cfg.ConcurrentPerUser < 1 {
		return nil, errors.New("voice: invalid configuration")
	}
	service := &Service{store: store, transcriber: transcriber, extractor: extractor, config: cfg, now: now, limiter: newUserLimiter(cfg.RateLimitPerMinute, cfg.ConcurrentPerUser)}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

func (service *Service) Enabled() bool { return service.config.Enabled }

func (service *Service) Process(ctx context.Context, actor auth.Actor, audio Audio, metadata Metadata) (result Draft, resultErr error) {
	startedAt := time.Now()
	defer func() {
		if service.observer == nil {
			return
		}
		status := "rejected"
		if errors.Is(resultErr, ErrRateLimit) {
			status = "rate_limited"
		} else if result.Status == StatusFailed {
			status = "failed"
		} else if result.Status == StatusNeedsReview {
			status = "needs_review"
		}
		service.observer.ObserveVoice(status, time.Since(startedAt))
	}()
	for _, permission := range []auth.Permission{auth.PermissionCustomerCreate, auth.PermissionJobCreate, auth.PermissionWaitlistAdd} {
		if err := actor.Require(permission); err != nil {
			return Draft{}, err
		}
	}
	if !service.config.Enabled {
		return Draft{}, ErrDisabled
	}
	release, ok := service.limiter.acquire(actor.UserID, service.now())
	if !ok {
		return Draft{}, ErrRateLimit
	}
	defer release()
	if audio.Reader == nil || audio.Size <= 0 {
		return Draft{}, fmt.Errorf("%w: empty audio", ErrValidation)
	}
	draft, err := service.store.Create(ctx, actor, service.now().Add(service.config.Retention))
	if err != nil {
		return Draft{}, fmt.Errorf("voice: creating draft: %w", err)
	}
	transcript, err := service.transcriber.Transcribe(ctx, audio, "de", metadata)
	if err != nil {
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if failErr := service.store.Fail(failCtx, actor, draft.ID, "transcription_failed"); failErr != nil {
			return Draft{}, fmt.Errorf("voice: recording provider failure: %w", failErr)
		}
		return service.store.Get(failCtx, actor, draft.ID)
	}
	transcript.Text = strings.TrimSpace(transcript.Text)
	if transcript.Text == "" || len([]rune(transcript.Text)) > 12000 {
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if failErr := service.store.Fail(failCtx, actor, draft.ID, "invalid_transcript"); failErr != nil {
			return Draft{}, fmt.Errorf("voice: recording invalid transcript: %w", failErr)
		}
		return service.store.Get(failCtx, actor, draft.ID)
	}
	fields, warnings, confidence := service.extractor.Extract(ctx, transcript.Text, metadata.RecordedAt, service.config.Timezone)
	if err := service.store.Complete(ctx, actor, draft.ID, transcript, fields, warnings, confidence, service.extractor.Version()); err != nil {
		return Draft{}, fmt.Errorf("voice: completing draft: %w", err)
	}
	return service.store.Get(ctx, actor, draft.ID)
}

func (service *Service) Get(ctx context.Context, actor auth.Actor, id string) (Draft, error) {
	if err := actor.Require(auth.PermissionCustomerCreate); err != nil {
		return Draft{}, err
	}
	return service.store.Get(ctx, actor, strings.TrimSpace(id))
}

func (service *Service) Duplicates(ctx context.Context, actor auth.Actor, draft Draft) ([]customers.Duplicate, error) {
	if err := actor.Require(auth.PermissionCustomerCreate); err != nil {
		return nil, err
	}
	return service.store.FindDuplicates(ctx, CustomerFromFields(draft.Fields))
}

func (service *Service) Commit(ctx context.Context, actor auth.Actor, input CommitInput) (customers.CreatedIntake, error) {
	for _, permission := range []auth.Permission{auth.PermissionCustomerCreate, auth.PermissionJobCreate, auth.PermissionWaitlistAdd} {
		if err := actor.Require(permission); err != nil {
			return customers.CreatedIntake{}, err
		}
	}
	input.DraftID = strings.TrimSpace(input.DraftID)
	input.ExistingCustomerID = strings.TrimSpace(input.ExistingCustomerID)
	input.Intake.Job.Source = customers.SourceVoice
	if !input.Reviewed || input.DraftID == "" || input.ExpectedVersion < 1 {
		return customers.CreatedIntake{}, fmt.Errorf("%w: explicit review is required", ErrValidation)
	}
	current, err := service.store.Get(ctx, actor, input.DraftID)
	if err != nil {
		return customers.CreatedIntake{}, err
	}
	if current.Status == StatusCommitted {
		return current.Committed, nil
	}
	if err := customers.PrepareIntake(&input.Intake); err != nil {
		return customers.CreatedIntake{}, err
	}
	duplicates, err := service.store.FindDuplicates(ctx, input.Intake.Customer)
	if err != nil {
		return customers.CreatedIntake{}, fmt.Errorf("voice: checking duplicates: %w", err)
	}
	if len(duplicates) > 0 {
		current, getErr := service.store.Get(ctx, actor, input.DraftID)
		if getErr != nil {
			return customers.CreatedIntake{}, getErr
		}
		if current.Status == StatusCommitted {
			return current.Committed, nil
		}
	}
	if input.ExistingCustomerID != "" && !containsDuplicate(duplicates, input.ExistingCustomerID) {
		return customers.CreatedIntake{}, fmt.Errorf("%w: selected customer is not a reviewed duplicate", ErrValidation)
	}
	if len(duplicates) > 0 && !input.DuplicateReviewed {
		return customers.CreatedIntake{}, fmt.Errorf("%w: duplicate decision is required", ErrValidation)
	}
	return service.store.Commit(ctx, actor, input)
}

func (service *Service) Discard(ctx context.Context, actor auth.Actor, id string) error {
	if err := actor.Require(auth.PermissionCustomerCreate); err != nil {
		return err
	}
	return service.store.Discard(ctx, actor, strings.TrimSpace(id))
}

func (service *Service) Cleanup(ctx context.Context) (int64, error) {
	return service.store.Cleanup(ctx)
}

func CustomerFromFields(fields Fields) customers.CustomerInput {
	return customers.CustomerInput{FirstName: fields.FirstName.Value, LastName: fields.LastName.Value, CompanyName: fields.CompanyName.Value, AddressFreeform: fields.AddressFreeform.Value, PhoneRaw: fields.PhoneRaw.Value, Email: fields.Email.Value, CountryCode: "AT", NotificationPreference: customers.NotifyNone}
}

func IntakeFromFields(fields Fields) customers.IntakeInput {
	hackMinutes, _ := strconv.Atoi(fields.EstimatedHackMinutes.Value)
	transportMinutes, _ := strconv.Atoi(fields.EstimatedTransportMinutes.Value)
	trips, _ := strconv.Atoi(fields.TransportTripCount.Value)
	jobType := customers.JobTypeChippingOnly
	mode := customers.TransportNone
	if fields.TransportMode.Value != "" && fields.TransportMode.Value != "none" {
		jobType = customers.JobTypeChippingWithTransport
		mode = customers.TransportMode(fields.TransportMode.Value)
	}
	urgency := customers.Urgency(fields.Urgency.Value)
	if !urgency.Valid() {
		urgency = customers.UrgencyNormal
	}
	return customers.IntakeInput{
		Customer:    CustomerFromFields(fields),
		Job:         customers.JobInput{JobType: jobType, VolumeM3: fields.VolumeM3.Value, EstimatedHackMinutes: hackMinutes, EstimatedTransportMinutes: transportMinutes, TransportTripCount: trips, TransportMode: mode, PreferredStartDate: fields.PreferredStartDate.Value, PreferredEndDate: fields.PreferredEndDate.Value, PreferenceText: fields.PreferenceText.Value, Urgency: urgency, Region: fields.Region.Value, Source: customers.SourceVoice},
		InitialNote: fields.Note.Value,
	}
}

func containsDuplicate(items []customers.Duplicate, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

type userLimit struct {
	window        time.Time
	count, active int
}
type userLimiter struct {
	mu               sync.Mutex
	limits           map[string]userLimit
	rate, concurrent int
}

func newUserLimiter(rate, concurrent int) *userLimiter {
	return &userLimiter{limits: make(map[string]userLimit), rate: rate, concurrent: concurrent}
}
func (limiter *userLimiter) acquire(userID string, now time.Time) (func(), bool) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	entry := limiter.limits[userID]
	if entry.window.IsZero() || now.Sub(entry.window) >= time.Minute {
		entry.window, entry.count = now, 0
	}
	if entry.count >= limiter.rate || entry.active >= limiter.concurrent {
		return func() {}, false
	}
	entry.count++
	entry.active++
	limiter.limits[userID] = entry
	return func() {
		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		current := limiter.limits[userID]
		if current.active > 0 {
			current.active--
		}
		limiter.limits[userID] = current
	}, true
}
