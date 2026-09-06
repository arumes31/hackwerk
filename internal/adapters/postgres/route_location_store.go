package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/routelocation"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RouteLocationStore persists reusable, confirmed route start and end locations.
type RouteLocationStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

func NewRouteLocationStore(pool *pgxpool.Pool) *RouteLocationStore {
	return &RouteLocationStore{pool: pool, queries: dbgen.New(pool)}
}

func (s *RouteLocationStore) List(ctx context.Context) ([]routelocation.Location, error) {
	rows, err := s.queries.ListRouteLocations(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]routelocation.Location, 0, len(rows))
	for _, row := range rows {
		value, valueErr := newRouteLocation(row.ID, row.Label, row.Address, row.Latitude, row.Longitude, row.Active, row.DefaultStart, row.DefaultEnd, row.Version, row.CreatedAt.Time, row.UpdatedAt.Time)
		if valueErr != nil {
			return nil, valueErr
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *RouteLocationStore) ListActive(ctx context.Context) ([]routelocation.Location, error) {
	rows, err := s.queries.ListActiveRouteLocations(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]routelocation.Location, 0, len(rows))
	for _, row := range rows {
		value, valueErr := newRouteLocation(row.ID, row.Label, row.Address, row.Latitude, row.Longitude, row.Active, row.DefaultStart, row.DefaultEnd, row.Version, row.CreatedAt.Time, row.UpdatedAt.Time)
		if valueErr != nil {
			return nil, valueErr
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *RouteLocationStore) DefaultStart(ctx context.Context) (routelocation.Location, error) {
	row, err := s.queries.GetDefaultRouteStartLocation(ctx)
	if err != nil {
		return routelocation.Location{}, mapRouteLocationError(err)
	}
	return newRouteLocation(row.ID, row.Label, row.Address, row.Latitude, row.Longitude, row.Active, row.DefaultStart, row.DefaultEnd, row.Version, row.CreatedAt.Time, row.UpdatedAt.Time)
}

func (s *RouteLocationStore) Resolve(ctx context.Context, id string, version int32) (routelocation.Location, error) {
	locationID, err := uuid(id)
	if err != nil {
		return routelocation.Location{}, routelocation.ErrNotFound
	}
	row, err := s.queries.GetActiveRouteLocation(ctx, locationID)
	if err != nil {
		return routelocation.Location{}, mapRouteLocationError(err)
	}
	if row.Version != version {
		return routelocation.Location{}, routelocation.ErrConflict
	}
	return newRouteLocation(row.ID, row.Label, row.Address, row.Latitude, row.Longitude, row.Active, row.DefaultStart, row.DefaultEnd, row.Version, row.CreatedAt.Time, row.UpdatedAt.Time)
}

func (s *RouteLocationStore) Create(ctx context.Context, actor auth.Actor, input routelocation.Input, requestID string) (result routelocation.Location, resultErr error) {
	latitude, err := routeLocationNumeric(input.Latitude)
	if err != nil {
		return routelocation.Location{}, err
	}
	longitude, err := routeLocationNumeric(input.Longitude)
	if err != nil {
		return routelocation.Location{}, err
	}
	resultErr = withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if err := queries.LockRouteLocationDefaults(ctx); err != nil {
			return err
		}
		if input.DefaultStart {
			if err := queries.ClearRouteLocationStartDefaultForCreate(ctx); err != nil {
				return err
			}
		}
		if input.DefaultEnd {
			if err := queries.ClearRouteLocationEndDefaultForCreate(ctx); err != nil {
				return err
			}
		}
		row, insertErr := queries.InsertRouteLocation(ctx, dbgen.InsertRouteLocationParams{
			Label: input.Label, Address: input.Address, Latitude: latitude, Longitude: longitude,
			DefaultStart: input.DefaultStart, DefaultEnd: input.DefaultEnd,
		})
		if insertErr != nil {
			return mapRouteLocationError(insertErr)
		}
		value, valueErr := newRouteLocation(row.ID, row.Label, row.Address, row.Latitude, row.Longitude, row.Active, row.DefaultStart, row.DefaultEnd, row.Version, row.CreatedAt.Time, row.UpdatedAt.Time)
		if valueErr != nil {
			return valueErr
		}
		result = value
		return insertAudit(ctx, queries, actor, "route_location.created", "route_location", result.ID, requestID,
			[]string{"label", "address", "latitude", "longitude", "default_start", "default_end"})
	})
	return result, resultErr
}

func (s *RouteLocationStore) Update(ctx context.Context, actor auth.Actor, id string, version int32, input routelocation.Input, requestID string) (result routelocation.Location, resultErr error) {
	locationID, err := uuid(id)
	if err != nil {
		return routelocation.Location{}, routelocation.ErrNotFound
	}
	latitude, err := routeLocationNumeric(input.Latitude)
	if err != nil {
		return routelocation.Location{}, err
	}
	longitude, err := routeLocationNumeric(input.Longitude)
	if err != nil {
		return routelocation.Location{}, err
	}
	resultErr = withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if err := queries.LockRouteLocationDefaults(ctx); err != nil {
			return err
		}
		current, getErr := queries.GetRouteLocationForUpdate(ctx, locationID)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return routelocation.ErrNotFound
		}
		if getErr != nil {
			return getErr
		}
		if !current.Active || current.Version != version {
			return routelocation.ErrConflict
		}
		if input.DefaultStart {
			if err := queries.ClearRouteLocationStartDefaultForUpdate(ctx, locationID); err != nil {
				return err
			}
		}
		if input.DefaultEnd {
			if err := queries.ClearRouteLocationEndDefaultForUpdate(ctx, locationID); err != nil {
				return err
			}
		}
		row, updateErr := queries.UpdateRouteLocation(ctx, dbgen.UpdateRouteLocationParams{
			Label: input.Label, Address: input.Address, Latitude: latitude, Longitude: longitude,
			DefaultStart: input.DefaultStart, DefaultEnd: input.DefaultEnd, ID: locationID, ExpectedVersion: version,
		})
		if errors.Is(updateErr, pgx.ErrNoRows) {
			return routelocation.ErrConflict
		}
		if updateErr != nil {
			return mapRouteLocationError(updateErr)
		}
		value, valueErr := newRouteLocation(row.ID, row.Label, row.Address, row.Latitude, row.Longitude, row.Active, row.DefaultStart, row.DefaultEnd, row.Version, row.CreatedAt.Time, row.UpdatedAt.Time)
		if valueErr != nil {
			return valueErr
		}
		result = value
		return insertAudit(ctx, queries, actor, "route_location.updated", "route_location", result.ID, requestID,
			[]string{"label", "address", "latitude", "longitude", "default_start", "default_end"})
	})
	return result, resultErr
}

func (s *RouteLocationStore) Deactivate(ctx context.Context, actor auth.Actor, id string, version int32, requestID string) error {
	locationID, err := uuid(id)
	if err != nil {
		return routelocation.ErrNotFound
	}
	return withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		rows, updateErr := queries.DeactivateRouteLocation(ctx, dbgen.DeactivateRouteLocationParams{ID: locationID, ExpectedVersion: version})
		if updateErr != nil {
			return mapRouteLocationError(updateErr)
		}
		if rows == 0 {
			return routelocation.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "route_location.deactivated", "route_location", id, requestID, []string{"active", "default_start", "default_end"})
	})
}

func newRouteLocation(id, label, address, latitudeText, longitudeText string, active, defaultStart, defaultEnd bool, version int32, createdAt, updatedAt time.Time) (routelocation.Location, error) {
	latitude, err := strconv.ParseFloat(latitudeText, 64)
	if err != nil {
		return routelocation.Location{}, fmt.Errorf("route location: parsing latitude: %w", err)
	}
	longitude, err := strconv.ParseFloat(longitudeText, 64)
	if err != nil {
		return routelocation.Location{}, fmt.Errorf("route location: parsing longitude: %w", err)
	}
	value := routelocation.Location{ID: id, Label: label, Address: address, Latitude: latitude, Longitude: longitude, Active: active, DefaultStart: defaultStart, DefaultEnd: defaultEnd, Version: version, CreatedAt: createdAt, UpdatedAt: updatedAt}
	if value.ID == "" || value.Version < 1 || (routelocation.Input{
		Label: value.Label, Address: value.Address, Latitude: value.Latitude, Longitude: value.Longitude,
	}).Validate() != nil {
		return routelocation.Location{}, routelocation.ErrValidation
	}
	return value, nil
}

func routeLocationNumeric(value float64) (pgtype.Numeric, error) {
	var result pgtype.Numeric
	if err := result.Scan(strconv.FormatFloat(value, 'f', 6, 64)); err != nil {
		return pgtype.Numeric{}, routelocation.ErrValidation
	}
	return result, nil
}

func mapRouteLocationError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return routelocation.ErrNotFound
	}
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) && (postgresErr.Code == "23505" || postgresErr.Code == "23514") {
		return routelocation.ErrConflict
	}
	return err
}
