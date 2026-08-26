package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/calendarfeed"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CalendarFeedStore struct{ queries *dbgen.Queries }

func NewCalendarFeedStore(pool *pgxpool.Pool) *CalendarFeedStore {
	return &CalendarFeedStore{queries: dbgen.New(pool)}
}

func (store *CalendarFeedStore) Create(ctx context.Context, ownerID string, tokenHash []byte, input calendarfeed.CreateInput) (calendarfeed.Feed, error) {
	owner, err := uuid(ownerID)
	if err != nil {
		return calendarfeed.Feed{}, calendarfeed.ErrInvalid
	}
	row, err := store.queries.CreateCalendarFeed(ctx, dbgen.CreateCalendarFeedParams{
		OwnerUserID: owner, TokenHash: tokenHash, Name: input.Name, FeedScope: input.Scope, DetailLevel: input.Detail,
		ResourceTypes: input.ResourceTypes, ExpiresAt: nullableTimestamp(input.ExpiresAt),
	})
	if err != nil {
		return calendarfeed.Feed{}, err
	}
	return calendarfeed.Feed{ID: row.ID, OwnerUserID: ownerID, Name: row.Name, Scope: row.FeedScope, Detail: row.DetailLevel, ResourceTypes: row.ResourceTypes,
		TokenVersion: row.TokenVersion, Version: row.Version, Active: row.Active, ExpiresAt: timestampValue(row.ExpiresAt), LastUsedAt: timestampValue(row.LastUsedAt), RevokedAt: timestampValue(row.RevokedAt), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}, nil
}

func (store *CalendarFeedStore) List(ctx context.Context, ownerID string) ([]calendarfeed.Feed, error) {
	owner, err := uuid(ownerID)
	if err != nil {
		return nil, calendarfeed.ErrInvalid
	}
	rows, err := store.queries.ListCalendarFeeds(ctx, owner)
	if err != nil {
		return nil, err
	}
	result := make([]calendarfeed.Feed, 0, len(rows))
	for _, row := range rows {
		result = append(result, calendarfeed.Feed{ID: row.ID, OwnerUserID: ownerID, Name: row.Name, Scope: row.FeedScope, Detail: row.DetailLevel,
			ResourceTypes: row.ResourceTypes, TokenVersion: row.TokenVersion, Version: row.Version, Active: row.Active, ExpiresAt: timestampValue(row.ExpiresAt),
			LastUsedAt: timestampValue(row.LastUsedAt), RevokedAt: timestampValue(row.RevokedAt), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time})
	}
	return result, nil
}

func (store *CalendarFeedStore) Rotate(ctx context.Context, ownerID, id string, version int32, tokenHash []byte) (calendarfeed.Feed, error) {
	owner, err := uuid(ownerID)
	if err != nil {
		return calendarfeed.Feed{}, calendarfeed.ErrInvalid
	}
	feedID, err := uuid(id)
	if err != nil {
		return calendarfeed.Feed{}, calendarfeed.ErrInvalid
	}
	row, err := store.queries.RotateCalendarFeed(ctx, dbgen.RotateCalendarFeedParams{TokenHash: tokenHash, ID: feedID, OwnerUserID: owner, ExpectedVersion: version})
	if errors.Is(err, pgx.ErrNoRows) {
		return calendarfeed.Feed{}, calendarfeed.ErrConflict
	}
	if err != nil {
		return calendarfeed.Feed{}, err
	}
	return calendarfeed.Feed{ID: row.ID, OwnerUserID: ownerID, Name: row.Name, Scope: row.FeedScope, Detail: row.DetailLevel, ResourceTypes: row.ResourceTypes,
		TokenVersion: row.TokenVersion, Version: row.Version, Active: row.Active, ExpiresAt: timestampValue(row.ExpiresAt), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}, nil
}

func (store *CalendarFeedStore) Revoke(ctx context.Context, ownerID, id string, version int32) (calendarfeed.Feed, error) {
	owner, err := uuid(ownerID)
	if err != nil {
		return calendarfeed.Feed{}, calendarfeed.ErrInvalid
	}
	feedID, err := uuid(id)
	if err != nil {
		return calendarfeed.Feed{}, calendarfeed.ErrInvalid
	}
	row, err := store.queries.RevokeCalendarFeed(ctx, dbgen.RevokeCalendarFeedParams{ID: feedID, OwnerUserID: owner, ExpectedVersion: version})
	if errors.Is(err, pgx.ErrNoRows) {
		return calendarfeed.Feed{}, calendarfeed.ErrConflict
	}
	if err != nil {
		return calendarfeed.Feed{}, err
	}
	return calendarfeed.Feed{ID: row.ID, OwnerUserID: ownerID, Name: row.Name, Scope: row.FeedScope, Detail: row.DetailLevel, ResourceTypes: row.ResourceTypes,
		TokenVersion: row.TokenVersion, Version: row.Version, Active: row.Active, RevokedAt: timestampValue(row.RevokedAt), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}, nil
}

func (store *CalendarFeedStore) ByTokenHash(ctx context.Context, tokenHash []byte) (calendarfeed.Feed, error) {
	row, err := store.queries.GetCalendarFeedByTokenHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return calendarfeed.Feed{}, calendarfeed.ErrNotFound
	}
	if err != nil {
		return calendarfeed.Feed{}, err
	}
	return calendarfeed.Feed{ID: row.FID, OwnerUserID: row.FOwnerUserID, Name: row.Name, Scope: row.FeedScope, Detail: row.DetailLevel,
		ResourceTypes: row.ResourceTypes, TokenVersion: row.TokenVersion, Active: true, OwnerActive: row.OwnerActive,
		ExpiresAt: timestampValue(row.ExpiresAt), UpdatedAt: row.UpdatedAt.Time}, nil
}

func (store *CalendarFeedStore) Touch(ctx context.Context, id string, usedAt time.Time) error {
	feedID, err := uuid(id)
	if err != nil {
		return calendarfeed.ErrInvalid
	}
	return store.queries.TouchCalendarFeed(ctx, dbgen.TouchCalendarFeedParams{UsedAt: timestamp(usedAt), ID: feedID})
}

func (store *CalendarFeedStore) Events(ctx context.Context, query calendarfeed.Query) ([]calendarfeed.Event, error) {
	owner, err := uuid(query.OwnerUserID)
	if err != nil {
		return nil, calendarfeed.ErrInvalid
	}
	rows, err := store.queries.ListCalendarFeedEvents(ctx, dbgen.ListCalendarFeedEventsParams{
		RangeEnd: timestamp(query.To), RangeStart: timestamp(query.From), FeedScope: query.Scope, OwnerUserID: owner, ResourceTypes: query.ResourceTypes,
	})
	if err != nil {
		return nil, err
	}
	result := make([]calendarfeed.Event, 0, len(rows))
	for _, row := range rows {
		modified := maxTimestamp(row.AppointmentUpdatedAt.Time, row.JobUpdatedAt.Time, row.CustomerUpdatedAt.Time)
		result = append(result, calendarfeed.Event{
			ID: row.AID, Lifecycle: row.LifecycleStatus, JobNumber: row.JobNumber, JobType: row.JobType, VolumeM3: row.JVolumeM3,
			CustomerName: strings.TrimSpace(strings.Join([]string{row.FirstName, row.LastName, row.CompanyName}, " ")), Street: row.Street,
			PostalCode: row.PostalCode, Locality: row.Locality, CountryCode: row.CountryCode, Latitude: row.Latitude, Longitude: row.Longitude,
			StartsAt: row.StartsAt.Time.UTC(), EndsAt: row.EndsAt.Time.UTC(), CreatedAt: row.AppointmentCreatedAt.Time.UTC(), LastModified: modified.UTC(),
			Sequence: int64(row.AppointmentVersion) + int64(row.JobVersion) + int64(row.CustomerVersion) - 2,
		})
	}
	return result, nil
}

func nullableTimestamp(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return timestamp(value)
}
func timestampValue(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}
func maxTimestamp(values ...time.Time) time.Time {
	var result time.Time
	for _, value := range values {
		if value.After(result) {
			result = value
		}
	}
	return result
}
