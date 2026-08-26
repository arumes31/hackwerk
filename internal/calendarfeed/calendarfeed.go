// Package calendarfeed provides read-only ICS exports and private subscriptions.
package calendarfeed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
)

var (
	ErrInvalid  = errors.New("calendarfeed: invalid input")
	ErrNotFound = errors.New("calendarfeed: not found")
	ErrConflict = errors.New("calendarfeed: conflict")
)

const (
	ScopeAll       = "all"
	ScopeOwn       = "own"
	DetailInternal = "internal"
	DetailMinimal  = "minimal"
)

var validResourceTypes = []string{"chipper", "transport_vehicle", "trailer", "other"}

type Config struct {
	BaseURL, UIDDomain, CalendarName string
	ExportMaxDays, HistoryDays       int
	FutureDays                       int
}

type Feed struct {
	ID, OwnerUserID, Name, Scope, Detail string
	ResourceTypes                        []string
	TokenVersion, Version                int32
	Active, OwnerActive                  bool
	ExpiresAt, LastUsedAt, RevokedAt     time.Time
	CreatedAt, UpdatedAt                 time.Time
}

type Event struct {
	ID, Lifecycle, JobNumber, JobType, VolumeM3 string
	CustomerName, Street, PostalCode, Locality  string
	CountryCode, Latitude, Longitude            string
	StartsAt, EndsAt, CreatedAt, LastModified   time.Time
	Sequence                                    int64
}

type Query struct {
	OwnerUserID, Scope string
	ResourceTypes      []string
	From, To           time.Time
}

type Store interface {
	Create(context.Context, string, []byte, CreateInput) (Feed, error)
	List(context.Context, string) ([]Feed, error)
	Rotate(context.Context, string, string, int32, []byte) (Feed, error)
	Revoke(context.Context, string, string, int32) (Feed, error)
	ByTokenHash(context.Context, []byte) (Feed, error)
	Touch(context.Context, string, time.Time) error
	Events(context.Context, Query) ([]Event, error)
}

type CreateInput struct {
	Name, Scope, Detail string
	ResourceTypes       []string
	ExpiresAt           time.Time
}

type Material struct {
	Feed          Feed
	RawToken, URL string
}

type Calendar struct {
	Bytes        []byte
	ETag         string
	LastModified time.Time
}

type Service struct {
	store    Store
	cfg      Config
	now      func() time.Time
	newToken func() (string, error)
}

func New(store Store, cfg Config, now func() time.Time, newToken func() (string, error)) (*Service, error) {
	base, err := url.Parse(cfg.BaseURL)
	if store == nil || err != nil || base.Scheme == "" || base.Host == "" || strings.TrimSpace(cfg.UIDDomain) == "" ||
		strings.TrimSpace(cfg.CalendarName) == "" || cfg.ExportMaxDays < 1 || cfg.HistoryDays < 0 || cfg.FutureDays < 1 {
		return nil, ErrInvalid
	}
	if now == nil {
		now = time.Now
	}
	if newToken == nil {
		newToken = auth.NewToken
	}
	return &Service{store: store, cfg: cfg, now: now, newToken: newToken}, nil
}

func (service *Service) Create(ctx context.Context, actor auth.Actor, input CreateInput) (Material, error) {
	if err := actor.Require(auth.PermissionCalendarFeedOwn); err != nil {
		return Material{}, err
	}
	input = normalizeInput(input)
	if err := validateInput(input); err != nil {
		return Material{}, err
	}
	raw, err := service.newToken()
	if err != nil {
		return Material{}, err
	}
	feed, err := service.store.Create(ctx, actor.UserID, auth.TokenHash(raw), input)
	if err != nil {
		return Material{}, err
	}
	return service.material(feed, raw), nil
}

func (service *Service) List(ctx context.Context, actor auth.Actor) ([]Feed, error) {
	if err := actor.Require(auth.PermissionCalendarFeedOwn); err != nil {
		return nil, err
	}
	return service.store.List(ctx, actor.UserID)
}

func (service *Service) Rotate(ctx context.Context, actor auth.Actor, id string, version int32) (Material, error) {
	if err := actor.Require(auth.PermissionCalendarFeedOwn); err != nil {
		return Material{}, err
	}
	if id == "" || version < 1 {
		return Material{}, ErrInvalid
	}
	raw, err := service.newToken()
	if err != nil {
		return Material{}, err
	}
	feed, err := service.store.Rotate(ctx, actor.UserID, id, version, auth.TokenHash(raw))
	if err != nil {
		return Material{}, err
	}
	return service.material(feed, raw), nil
}

func (service *Service) Revoke(ctx context.Context, actor auth.Actor, id string, version int32) error {
	if err := actor.Require(auth.PermissionCalendarFeedOwn); err != nil {
		return err
	}
	if id == "" || version < 1 {
		return ErrInvalid
	}
	_, err := service.store.Revoke(ctx, actor.UserID, id, version)
	return err
}

func (service *Service) Export(ctx context.Context, actor auth.Actor, from, to time.Time, detail string) (Calendar, error) {
	if err := actor.Require(auth.PermissionCalendarViewAll); err != nil {
		return Calendar{}, err
	}
	if from.IsZero() || to.IsZero() || !to.After(from) || to.Sub(from) > time.Duration(service.cfg.ExportMaxDays)*24*time.Hour {
		return Calendar{}, ErrInvalid
	}
	if detail != DetailMinimal {
		detail = DetailInternal
	}
	events, err := service.store.Events(ctx, Query{OwnerUserID: actor.UserID, Scope: ScopeAll, From: from.UTC(), To: to.UTC()})
	if err != nil {
		return Calendar{}, err
	}
	return Generate(service.cfg.CalendarName, service.cfg.UIDDomain, service.cfg.BaseURL, detail, events, service.now().UTC())
}

func (service *Service) Public(ctx context.Context, raw string) (Calendar, error) {
	if len(raw) < 40 || len(raw) > 100 {
		return Calendar{}, ErrNotFound
	}
	feed, err := service.store.ByTokenHash(ctx, auth.TokenHash(raw))
	if err != nil || !feed.OwnerActive || !feed.Active || (!feed.ExpiresAt.IsZero() && !service.now().Before(feed.ExpiresAt)) {
		return Calendar{}, ErrNotFound
	}
	now := service.now().UTC()
	events, err := service.store.Events(ctx, Query{OwnerUserID: feed.OwnerUserID, Scope: feed.Scope, ResourceTypes: feed.ResourceTypes, From: now.AddDate(0, 0, -service.cfg.HistoryDays), To: now.AddDate(0, 0, service.cfg.FutureDays)})
	if err != nil {
		return Calendar{}, err
	}
	calendar, err := Generate(feed.Name, service.cfg.UIDDomain, service.cfg.BaseURL, feed.Detail, events, feed.UpdatedAt)
	if err == nil {
		_ = service.store.Touch(ctx, feed.ID, now)
	}
	return calendar, err
}

func (service *Service) material(feed Feed, raw string) Material {
	return Material{Feed: feed, RawToken: raw, URL: strings.TrimRight(service.cfg.BaseURL, "/") + "/feeds/" + url.PathEscape(raw) + "/calendar.ics"}
}

func normalizeInput(input CreateInput) CreateInput {
	input.Name, input.Scope, input.Detail = strings.TrimSpace(input.Name), strings.TrimSpace(input.Scope), strings.TrimSpace(input.Detail)
	input.ResourceTypes = append([]string{}, input.ResourceTypes...)
	if input.Scope == "" {
		input.Scope = ScopeAll
	}
	if input.Detail == "" {
		input.Detail = DetailInternal
	}
	slices.Sort(input.ResourceTypes)
	input.ResourceTypes = slices.Compact(input.ResourceTypes)
	return input
}

func validateInput(input CreateInput) error {
	if input.Name == "" || len([]rune(input.Name)) > 100 || (input.Scope != ScopeAll && input.Scope != ScopeOwn) || (input.Detail != DetailInternal && input.Detail != DetailMinimal) {
		return ErrInvalid
	}
	for _, value := range input.ResourceTypes {
		if !slices.Contains(validResourceTypes, value) {
			return ErrInvalid
		}
	}
	return nil
}

func etag(payload []byte) string {
	sum := sha256.Sum256(payload)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func statusLabel(value string) string {
	switch value {
	case "cancelled":
		return "Abgesagt"
	case "completed":
		return "Erledigt"
	default:
		return "Fixiert"
	}
}

func jobTypeLabel(value string) string {
	if value == "chipping_with_transport" {
		return "Hacken mit Transport"
	}
	return "Nur Hackmaschine"
}

func customerName(event Event) string { return strings.TrimSpace(event.CustomerName) }

func validateEvent(event Event) error {
	if event.ID == "" || event.StartsAt.IsZero() || !event.EndsAt.After(event.StartsAt) || event.Sequence < 1 {
		return fmt.Errorf("%w: event", ErrInvalid)
	}
	return nil
}
