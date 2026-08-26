package postgres

import (
	"context"
	"errors"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/resource"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ResourceStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

func NewResourceStore(pool *pgxpool.Pool) *ResourceStore {
	return &ResourceStore{pool: pool, queries: dbgen.New(pool)}
}

func (s *ResourceStore) List(ctx context.Context) ([]resource.Resource, error) {
	rows, err := s.queries.ListResources(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]resource.Resource, 0, len(rows))
	for _, row := range rows {
		capacity, decodeErr := resource.DecodeCapacity(row.CapacityMetadata)
		if decodeErr != nil {
			return nil, decodeErr
		}
		result = append(result, resource.Resource{
			ID: row.ID, Type: resource.Type(row.ResourceType), Name: row.Name,
			IsExclusive: row.Exclusive, IsActive: row.Active, Capacity: capacity,
			InternalNote: row.InternalNote, Version: row.Version,
			CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return result, nil
}

func (s *ResourceStore) Create(ctx context.Context, actor auth.Actor, input resource.Input, requestID string) (id string, resultErr error) {
	capacity, err := resource.EncodeCapacity(input.Capacity)
	if err != nil {
		return "", err
	}
	resultErr = withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		var insertErr error
		id, insertErr = queries.InsertResource(ctx, dbgen.InsertResourceParams{
			ResourceType: string(input.Type), Name: input.Name, Exclusive: input.IsExclusive,
			CapacityMetadata: capacity, InternalNote: input.InternalNote,
		})
		if insertErr != nil {
			return mapResourceConflict(insertErr)
		}
		return insertAudit(ctx, queries, actor, "resource.created", "resource", id, requestID,
			[]string{"resource_type", "name", "exclusive", "capacity_metadata"})
	})
	return id, resultErr
}

func (s *ResourceStore) Update(ctx context.Context, actor auth.Actor, id string, version int32, input resource.Input, requestID string) error {
	resourceID, err := uuid(id)
	if err != nil {
		return resource.ErrNotFound
	}
	capacity, err := resource.EncodeCapacity(input.Capacity)
	if err != nil {
		return err
	}
	return withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		current, getErr := queries.GetResourceForUpdate(ctx, resourceID)
		if getErr != nil {
			return getErr
		}
		if current.Version != version || !current.Active {
			return resource.ErrConflict
		}
		if current.ResourceType != string(input.Type) || current.Exclusive != input.IsExclusive {
			reserved, reservedErr := queries.HasActiveResourceReservations(ctx, resourceID)
			if reservedErr != nil {
				return reservedErr
			}
			if reserved {
				return resource.ErrConflict
			}
		}
		rows, updateErr := queries.UpdateResource(ctx, dbgen.UpdateResourceParams{
			ResourceType: string(input.Type), Name: input.Name, Exclusive: input.IsExclusive,
			CapacityMetadata: capacity, InternalNote: input.InternalNote,
			ID: resourceID, ExpectedVersion: version,
		})
		if updateErr != nil {
			return mapResourceConflict(updateErr)
		}
		if rows == 0 {
			return resource.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "resource.updated", "resource", id, requestID,
			[]string{"resource_type", "name", "exclusive", "capacity_metadata", "internal_note"})
	})
}

func (s *ResourceStore) Deactivate(ctx context.Context, actor auth.Actor, id string, version int32, requestID string) error {
	resourceID, err := uuid(id)
	if err != nil {
		return resource.ErrNotFound
	}
	return withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		current, getErr := queries.GetResourceForUpdate(ctx, resourceID)
		if getErr != nil {
			return getErr
		}
		if current.Version != version || !current.Active {
			return resource.ErrConflict
		}
		reserved, reservedErr := queries.HasActiveResourceReservations(ctx, resourceID)
		if reservedErr != nil {
			return reservedErr
		}
		if reserved {
			return resource.ErrConflict
		}
		rows, updateErr := queries.DeactivateResource(ctx, dbgen.DeactivateResourceParams{ID: resourceID, ExpectedVersion: version})
		if updateErr != nil {
			return updateErr
		}
		if rows == 0 {
			return resource.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "resource.deactivated", "resource", id, requestID, []string{"active"})
	})
}

func mapResourceConflict(err error) error {
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) && (postgresErr.Code == "23505" || postgresErr.Code == "23P01") {
		return resource.ErrConflict
	}
	return err
}
