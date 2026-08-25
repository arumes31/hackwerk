// Package migrate applies HackWerk's embedded Goose migrations.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"example.invalid/hackplan/db/migrations"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Direction identifies a supported migration operation.
type Direction string

const (
	DirectionUp     Direction = "up"
	DirectionDown   Direction = "down"
	DirectionStatus Direction = "status"
)

// Run executes one embedded migration operation and writes a human-readable result.
func Run(ctx context.Context, databaseURL string, direction Direction, output io.Writer) (runErr error) {
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: opening database: %w", err)
	}
	defer func() {
		runErr = errors.Join(runErr, database.Close())
	}()

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		database,
		migrations.Files,
	)
	if err != nil {
		return fmt.Errorf("migrate: creating provider: %w", err)
	}
	defer func() {
		runErr = errors.Join(runErr, provider.Close())
	}()

	switch direction {
	case DirectionUp:
		results, upErr := provider.Up(ctx)
		if upErr != nil {
			return fmt.Errorf("migrate: applying migrations: %w", upErr)
		}
		writeResults(output, results)
	case DirectionDown:
		result, downErr := provider.Down(ctx)
		if downErr != nil {
			if errors.Is(downErr, goose.ErrNoNextVersion) {
				_, _ = fmt.Fprintln(output, "Keine angewendete Migration zum Zurücknehmen.")
				return nil
			}
			return fmt.Errorf("migrate: reverting migration: %w", downErr)
		}
		_, _ = fmt.Fprintln(output, result.String())
	case DirectionStatus:
		statuses, statusErr := provider.Status(ctx)
		if statusErr != nil {
			return fmt.Errorf("migrate: reading status: %w", statusErr)
		}
		if len(statuses) == 0 {
			_, _ = fmt.Fprintln(output, "Keine Migrationen gefunden.")
			return nil
		}
		for _, status := range statuses {
			_, _ = fmt.Fprintf(
				output,
				"%s %05d %s\n",
				status.State,
				status.Source.Version,
				status.Source.Path,
			)
		}
	default:
		return fmt.Errorf("migrate: unsupported direction %q", direction)
	}
	return nil
}

func writeResults(output io.Writer, results []*goose.MigrationResult) {
	if len(results) == 0 {
		_, _ = fmt.Fprintln(output, "Schema ist bereits aktuell.")
		return
	}
	for _, result := range results {
		_, _ = fmt.Fprintln(output, result.String())
	}
}
