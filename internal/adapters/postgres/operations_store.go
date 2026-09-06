package postgres

import (
	"context"
	"errors"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/observability"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OperationsStore struct {
	pool       *pgxpool.Pool
	queries    *dbgen.Queries
	staleAfter time.Duration
}

func NewOperationsStore(pool *pgxpool.Pool, staleAfter ...time.Duration) *OperationsStore {
	stale := 2 * time.Minute
	if len(staleAfter) > 0 && staleAfter[0] > 0 {
		stale = staleAfter[0]
	}
	return &OperationsStore{pool: pool, queries: dbgen.New(pool), staleAfter: stale}
}

func (store *OperationsStore) Ping(ctx context.Context) error { return store.pool.Ping(ctx) }

func (store *OperationsStore) Ready(ctx context.Context, expected int64) error {
	if err := store.pool.Ping(ctx); err != nil {
		return err
	}
	application, err := store.queries.SchemaApplication(ctx)
	if err != nil || application != "hackplan" {
		return errors.New("postgres: incompatible application schema")
	}
	version, err := store.queries.LatestAppliedMigration(ctx)
	if err != nil || version != expected {
		return errors.New("postgres: incompatible schema version")
	}
	return nil
}

func (store *OperationsStore) Heartbeat(ctx context.Context, workerID string, startedAt, heartbeatAt time.Time, status string) error {
	return store.queries.UpsertWorkerHeartbeat(ctx, dbgen.UpsertWorkerHeartbeatParams{WorkerID: workerID, StartedAt: timestamp(startedAt.UTC()), HeartbeatAt: timestamp(heartbeatAt.UTC()), Status: status})
}

func (store *OperationsStore) WorkerHealthy(ctx context.Context, staleAfter time.Duration) (time.Time, bool, error) {
	value, err := store.queries.LatestWorkerHeartbeat(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	if !value.Valid || value.InfinityModifier != 0 {
		return time.Time{}, false, nil
	}
	heartbeat := value.Time.UTC()
	return heartbeat, time.Since(heartbeat) <= staleAfter, nil
}

func (store *OperationsStore) WorkerHealthyByID(ctx context.Context, workerID string, staleAfter time.Duration) (time.Time, bool, error) {
	value, err := store.queries.WorkerHeartbeatByID(ctx, workerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if !value.Valid || value.InfinityModifier != 0 {
		return time.Time{}, false, nil
	}
	heartbeat := value.Time.UTC()
	return heartbeat, time.Since(heartbeat) <= staleAfter, nil
}

func (store *OperationsStore) Collect(ctx context.Context) (observability.Snapshot, error) {
	row, err := store.queries.OperationalSnapshot(ctx)
	if err != nil {
		return observability.Snapshot{}, err
	}
	heartbeat, healthy, err := store.WorkerHealthy(ctx, store.staleAfter)
	if err != nil {
		return observability.Snapshot{}, err
	}
	notifications, err := store.queries.NotificationMetricCounts(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return observability.Snapshot{}, err
	}
	voices, err := store.queries.VoiceMetricCounts(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return observability.Snapshot{}, err
	}
	stats := store.pool.Stat()
	result := observability.Snapshot{
		DBMax: stats.MaxConns(), DBTotal: stats.TotalConns(), DBAcquired: stats.AcquiredConns(), DBIdle: stats.IdleConns(),
		OutboxPending: row.OutboxPending, OutboxOldestSeconds: row.OutboxOldestSeconds, OutboxAttempts: row.OutboxAttempts,
		ActiveSessions: row.ActiveSessions, PlanningRunsRecent: row.PlanningRunsRecent, PlanningCandidatesRecent: row.PlanningCandidatesRecent,
		WorkerHeartbeat: heartbeat, WorkerHealthy: healthy,
	}
	for _, value := range notifications {
		result.Notifications = append(result.Notifications, observability.Count{Kind: value.Channel, Status: value.Status, Total: value.Total})
	}
	for _, value := range voices {
		result.Voice = append(result.Voice, observability.Count{Status: value.Status, Total: value.Total})
	}
	return result, nil
}
