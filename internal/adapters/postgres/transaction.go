package postgres

import (
	"context"
	"errors"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func withQueries(ctx context.Context, pool *pgxpool.Pool, operation func(*dbgen.Queries) error) (resultErr error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		rollbackErr := tx.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, rollbackErr)
		}
	}()
	if err := operation(dbgen.New(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
