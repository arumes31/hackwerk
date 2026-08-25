package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IdentityStore persists authentication and user administration through sqlc queries.
type IdentityStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

// NewIdentityStore creates a PostgreSQL identity adapter.
func NewIdentityStore(pool *pgxpool.Pool) *IdentityStore {
	return &IdentityStore{pool: pool, queries: dbgen.New(pool)}
}

// FindUserByUsername resolves a case-insensitive username.
func (store *IdentityStore) FindUserByUsername(ctx context.Context, username string) (auth.User, error) {
	row, err := store.queries.FindUserByUsername(ctx, username)
	if err != nil {
		return auth.User{}, mapNotFound(err)
	}
	return auth.User{
		ID: row.ID, Username: row.Username, DisplayName: row.DisplayName, Email: row.Email,
		Role: auth.Role(row.Role), PasswordHash: row.PasswordHash, MustChangePassword: row.MustChangePassword,
		Active: row.Active, Version: row.Version, DriverID: row.DriverID,
	}, nil
}

// FindUserByID resolves a user with an optional separate driver profile.
func (store *IdentityStore) FindUserByID(ctx context.Context, id string) (auth.User, error) {
	userID, err := uuid(id)
	if err != nil {
		return auth.User{}, auth.ErrNotFound
	}
	row, err := store.queries.FindUserByID(ctx, userID)
	if err != nil {
		return auth.User{}, mapNotFound(err)
	}
	return auth.User{
		ID: row.UID, Username: row.UUsername, DisplayName: row.DisplayName, Email: row.Email,
		Role: auth.Role(row.Role), PasswordHash: row.PasswordHash, MustChangePassword: row.MustChangePassword,
		Active: row.Active, Version: row.Version, DriverID: row.DriverID,
	}, nil
}

// RotateLogin atomically revokes older sessions, optionally rehashes, creates a session, and audits login.
func (store *IdentityStore) RotateLogin(ctx context.Context, user auth.User, session auth.NewSession, replacementHash []byte, rateKey []byte, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		userID, err := uuid(user.ID)
		if err != nil {
			return err
		}
		if len(replacementHash) > 0 {
			rows, updateErr := queries.UpdatePassword(ctx, dbgen.UpdatePasswordParams{
				PasswordHash: string(replacementHash), MustChangePassword: user.MustChangePassword,
				ID: userID, ExpectedVersion: user.Version,
			})
			if updateErr != nil {
				return updateErr
			}
			if rows != 1 {
				return auth.ErrConflict
			}
		}
		if err := queries.RevokeUserSessions(ctx, userID); err != nil {
			return err
		}
		if _, err := queries.InsertSession(ctx, dbgen.InsertSessionParams{
			UserID: userID, TokenHash: session.TokenHash, CsrfTokenHash: session.CSRFTokenHash,
			IdleExpiresAt: timestamp(session.IdleExpiresAt), AbsoluteExpiresAt: timestamp(session.AbsoluteExpiresAt),
		}); err != nil {
			return err
		}
		if err := queries.MarkLogin(ctx, userID); err != nil {
			return err
		}
		if err := queries.ClearLoginFailures(ctx, rateKey); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actorForUser(user), "auth.login", "user", user.ID, requestID, []string{"last_login_at", "sessions"})
	})
}

// FindSession loads server-side state by token hash only.
func (store *IdentityStore) FindSession(ctx context.Context, tokenHash []byte) (auth.Session, error) {
	row, err := store.queries.FindSession(ctx, tokenHash)
	if err != nil {
		return auth.Session{}, mapNotFound(err)
	}
	var revokedAt *time.Time
	if row.RevokedAt.Valid {
		value := row.RevokedAt.Time
		revokedAt = &value
	}
	return auth.Session{
		ID: row.SID,
		Actor: auth.Actor{
			UserID: row.SUserID, Username: row.UUsername, DisplayName: row.DisplayName,
			Role: auth.Role(row.Role), DriverID: row.DriverID, MustChangePassword: row.MustChangePassword,
			UserVersion: row.Version,
		},
		CSRFTokenHash: row.CsrfTokenHash, IdleExpiresAt: row.IdleExpiresAt.Time,
		AbsoluteExpiresAt: row.AbsoluteExpiresAt.Time, RevokedAt: revokedAt, UserActive: row.Active,
	}, nil
}

// TouchSession extends idle expiry but never beyond absolute expiry.
func (store *IdentityStore) TouchSession(ctx context.Context, id string, idleExpiresAt time.Time) error {
	sessionID, err := uuid(id)
	if err != nil {
		return auth.ErrInvalidSession
	}
	return store.queries.TouchSession(ctx, dbgen.TouchSessionParams{ID: sessionID, IdleExpiresAt: timestamp(idleExpiresAt)})
}

// RevokeSession invalidates one opaque token.
func (store *IdentityStore) RevokeSession(ctx context.Context, tokenHash []byte) error {
	return store.queries.RevokeSession(ctx, tokenHash)
}

// LoginRate returns zero for a key without prior failures.
func (store *IdentityStore) LoginRate(ctx context.Context, keyHash []byte) (auth.RateLimit, error) {
	row, err := store.queries.FindRateLimit(ctx, keyHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.RateLimit{}, nil
	}
	if err != nil {
		return auth.RateLimit{}, err
	}
	return auth.RateLimit{WindowStartedAt: row.WindowStartedAt.Time, FailureCount: int(row.FailureCount)}, nil
}

// RecordLoginFailure updates a hashed one-minute bucket.
func (store *IdentityStore) RecordLoginFailure(ctx context.Context, keyHash []byte) error {
	return store.queries.RecordLoginFailure(ctx, keyHash)
}

// ListUsers returns only admin-list fields.
func (store *IdentityStore) ListUsers(ctx context.Context) ([]auth.UserSummary, error) {
	rows, err := store.queries.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	users := make([]auth.UserSummary, 0, len(rows))
	for _, row := range rows {
		var lastLoginAt *time.Time
		if row.LastLoginAt.Valid {
			value := row.LastLoginAt.Time
			lastLoginAt = &value
		}
		users = append(users, auth.UserSummary{
			ID: row.UID, Username: row.UUsername, DisplayName: row.DisplayName, Email: row.Email,
			Role: auth.Role(row.Role), MustChangePassword: row.MustChangePassword, Active: row.Active,
			LastLoginAt: lastLoginAt, Version: row.Version, DriverID: row.DriverID,
		})
	}
	return users, nil
}

// CreateUser inserts the user, optional driver, and a minimized audit event atomically.
func (store *IdentityStore) CreateUser(ctx context.Context, actor auth.Actor, input auth.CreateUserInput, passwordHash string) (userID string, resultErr error) {
	resultErr = store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		createdID, err := queries.InsertUser(ctx, dbgen.InsertUserParams{
			Username: input.Username, DisplayName: input.DisplayName, Email: input.Email,
			Role: string(input.Role), PasswordHash: passwordHash, MustChangePassword: true,
		})
		if err != nil {
			return mapConflict(err)
		}
		userID = createdID
		changed := []string{"username", "display_name", "role", "password_hash", "must_change_password"}
		if input.Email != "" {
			changed = append(changed, "email")
		}
		if input.CreateDriver {
			if _, err := queries.InsertDriver(ctx, dbgen.InsertDriverParams{UserID: createdID, DisplayName: input.DisplayName, Email: input.Email}); err != nil {
				return err
			}
			changed = append(changed, "driver_id")
		}
		return insertAudit(ctx, queries, actor, "user.created", "user", createdID, input.RequestID, changed)
	})
	return userID, resultErr
}

// UpdateUserAccess protects the last admin under a serializable transaction and revokes disabled sessions.
func (store *IdentityStore) UpdateUserAccess(ctx context.Context, actor auth.Actor, input auth.UpdateAccessInput) error {
	return store.transaction(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(queries *dbgen.Queries, tx pgx.Tx) error {
		target, err := findUserByID(ctx, queries, input.UserID)
		if err != nil {
			return err
		}
		removesAdmin := target.Active && target.Role == auth.RoleAdmin && (!input.Active || input.Role != auth.RoleAdmin)
		if removesAdmin {
			rows, lockErr := tx.Query(ctx, "SELECT id FROM users WHERE active AND role = 'admin' FOR UPDATE")
			if lockErr != nil {
				return lockErr
			}
			rows.Close()
			count, countErr := queries.CountActiveAdmins(ctx)
			if countErr != nil {
				return countErr
			}
			if count <= 1 {
				return auth.ErrLastAdmin
			}
		}
		userID, err := uuid(input.UserID)
		if err != nil {
			return auth.ErrNotFound
		}
		affected, err := queries.UpdateUserAccess(ctx, dbgen.UpdateUserAccessParams{
			Role: string(input.Role), Active: input.Active, ID: userID, ExpectedVersion: input.ExpectedVersion,
		})
		if err != nil {
			return err
		}
		if affected != 1 {
			return auth.ErrConflict
		}
		if !input.Active {
			if err := queries.RevokeUserSessions(ctx, userID); err != nil {
				return err
			}
		}
		return insertAudit(ctx, queries, actor, "user.access_updated", "user", input.UserID, input.RequestID, []string{"role", "active"})
	})
}

// ResetPassword updates the optimistic version, forces change, revokes sessions, and audits field names only.
func (store *IdentityStore) ResetPassword(ctx context.Context, actor auth.Actor, input auth.ResetPasswordInput, passwordHash string) error {
	return store.updatePassword(ctx, actor, input.UserID, input.ExpectedVersion, passwordHash, true, "user.password_reset", input.RequestID)
}

// ChangeOwnPassword clears the must-change flag and revokes all sessions.
func (store *IdentityStore) ChangeOwnPassword(ctx context.Context, actor auth.Actor, passwordHash string, expectedVersion int32) error {
	return store.updatePassword(ctx, actor, actor.UserID, expectedVersion, passwordHash, false, "user.password_changed", "")
}

func (store *IdentityStore) updatePassword(ctx context.Context, actor auth.Actor, userID string, expectedVersion int32, passwordHash string, mustChange bool, action string, requestID string) error {
	return store.transaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries, _ pgx.Tx) error {
		id, err := uuid(userID)
		if err != nil {
			return auth.ErrNotFound
		}
		affected, err := queries.UpdatePassword(ctx, dbgen.UpdatePasswordParams{
			PasswordHash: passwordHash, MustChangePassword: mustChange, ID: id, ExpectedVersion: expectedVersion,
		})
		if err != nil {
			return err
		}
		if affected != 1 {
			return auth.ErrConflict
		}
		if err := queries.RevokeUserSessions(ctx, id); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, action, "user", userID, requestID, []string{"password_hash", "must_change_password", "sessions"})
	})
}

func (store *IdentityStore) transaction(ctx context.Context, options pgx.TxOptions, operation func(*dbgen.Queries, pgx.Tx) error) (resultErr error) {
	tx, err := store.pool.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer func() {
		rollbackErr := tx.Rollback(ctx)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			resultErr = errors.Join(resultErr, rollbackErr)
		}
	}()
	if err := operation(store.queries.WithTx(tx), tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func findUserByID(ctx context.Context, queries *dbgen.Queries, id string) (auth.User, error) {
	userID, err := uuid(id)
	if err != nil {
		return auth.User{}, auth.ErrNotFound
	}
	row, err := queries.FindUserByID(ctx, userID)
	if err != nil {
		return auth.User{}, mapNotFound(err)
	}
	return auth.User{ID: row.UID, Role: auth.Role(row.Role), Active: row.Active, Version: row.Version}, nil
}

func insertAudit(ctx context.Context, queries *dbgen.Queries, actor auth.Actor, action string, objectType string, objectID string, requestID string, changedFields []string) error {
	metadata, err := json.Marshal(map[string][]string{"changed_fields": changedFields})
	if err != nil {
		return fmt.Errorf("postgres: encoding audit metadata: %w", err)
	}
	actorType := "user"
	actorID := actor.UserID
	if actor.System {
		actorType = "system"
		actorID = ""
	}
	return queries.InsertAuditEvent(ctx, dbgen.InsertAuditEventParams{
		ActorType: actorType, ActorUserID: actorID, Action: action, ObjectType: objectType,
		ObjectID: objectID, RequestID: requestID, Metadata: metadata,
	})
}

func actorForUser(user auth.User) auth.Actor {
	return auth.Actor{UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Role: user.Role}
}

func uuid(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil || !id.Valid {
		return pgtype.UUID{}, errors.New("postgres: invalid uuid")
	}
	return id, nil
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func mapNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.ErrNotFound
	}
	return err
}

func mapConflict(err error) error {
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) && postgresErr.Code == "23505" {
		return auth.ErrConflict
	}
	return err
}
