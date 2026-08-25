package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"
)

// User is the persistence-neutral identity record used by authentication.
type User struct {
	ID                 string
	Username           string
	DisplayName        string
	Email              string
	Role               Role
	PasswordHash       string
	MustChangePassword bool
	Active             bool
	Version            int32
	DriverID           string
}

// Session is the persistence-neutral server-side session record.
type Session struct {
	ID                string
	Actor             Actor
	CSRFTokenHash     []byte
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
	RevokedAt         *time.Time
	UserActive        bool
}

// SessionTokens are raw one-time values returned only to the browser boundary.
type SessionTokens struct {
	SessionToken string
	CSRFToken    string
	Actor        Actor
}

// NewSession contains only hashed tokens for persistence.
type NewSession struct {
	UserID            string
	TokenHash         []byte
	CSRFTokenHash     []byte
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
}

// RateLimit is a privacy-minimized login failure window.
type RateLimit struct {
	WindowStartedAt time.Time
	FailureCount    int
}

// UserSummary contains non-secret admin-list fields.
type UserSummary struct {
	ID                 string
	Username           string
	DisplayName        string
	Email              string
	Role               Role
	MustChangePassword bool
	Active             bool
	LastLoginAt        *time.Time
	Version            int32
	DriverID           string
}

// CreateUserInput is shared by the admin UI, CLI, and development seed.
type CreateUserInput struct {
	Username     string
	DisplayName  string
	Email        string
	Role         Role
	Password     string
	CreateDriver bool
	RequestID    string
}

// UpdateAccessInput changes role/active state with optimistic concurrency.
type UpdateAccessInput struct {
	UserID          string
	Role            Role
	Active          bool
	ExpectedVersion int32
	RequestID       string
}

// ResetPasswordInput revokes sessions and forces a subsequent password change.
type ResetPasswordInput struct {
	UserID          string
	Password        string
	ExpectedVersion int32
	RequestID       string
}

// Store is the database boundary. Multi-step mutation methods are atomic.
type Store interface {
	FindUserByUsername(context.Context, string) (User, error)
	FindUserByID(context.Context, string) (User, error)
	RotateLogin(context.Context, User, NewSession, []byte, []byte, string) error
	FindSession(context.Context, []byte) (Session, error)
	TouchSession(context.Context, string, time.Time) error
	RevokeSession(context.Context, []byte) error
	LoginRate(context.Context, []byte) (RateLimit, error)
	RecordLoginFailure(context.Context, []byte) error
	ListUsers(context.Context) ([]UserSummary, error)
	CreateUser(context.Context, Actor, CreateUserInput, string) (string, error)
	UpdateUserAccess(context.Context, Actor, UpdateAccessInput) error
	ResetPassword(context.Context, Actor, ResetPasswordInput, string) error
	ChangeOwnPassword(context.Context, Actor, string, int32) error
}

// Service applies authentication, password, session, and RBAC invariants.
type Service struct {
	store       Store
	hasher      PasswordHasher
	now         func() time.Time
	newToken    func() (string, error)
	idleTTL     time.Duration
	absoluteTTL time.Duration
	loginLimit  int
	dummyHash   string
}

// NewService constructs an identity service and precomputes an enumeration-safe dummy hash.
func NewService(store Store, hasher PasswordHasher, now func() time.Time, idleTTL time.Duration, absoluteTTL time.Duration, loginLimit int) (*Service, error) {
	if store == nil || now == nil || idleTTL <= 0 || absoluteTTL < idleTTL || loginLimit < 1 {
		return nil, errors.New("auth: invalid service dependencies")
	}
	dummyHash, err := hasher.Hash("Ungültiges Dummy-Passwort 2026")
	if err != nil {
		return nil, fmt.Errorf("auth: creating dummy hash: %w", err)
	}
	return &Service{
		store: store, hasher: hasher, now: now, newToken: NewToken,
		idleTTL: idleTTL, absoluteTTL: absoluteTTL, loginLimit: loginLimit, dummyHash: dummyHash,
	}, nil
}

// Login verifies credentials generically and rotates all existing sessions atomically.
func (service *Service) Login(ctx context.Context, username string, password string, clientKey string, requestID string) (SessionTokens, error) {
	now := service.now()
	rateKey := TokenHash("login\x00" + strings.ToLower(strings.TrimSpace(username)) + "\x00" + clientKey)
	rate, err := service.store.LoginRate(ctx, rateKey)
	if err != nil {
		return SessionTokens{}, fmt.Errorf("auth: reading login rate: %w", err)
	}
	if rate.FailureCount >= service.loginLimit && now.Sub(rate.WindowStartedAt) < time.Minute {
		return SessionTokens{}, ErrRateLimited
	}

	user, findErr := service.store.FindUserByUsername(ctx, strings.TrimSpace(username))
	hash := service.dummyHash
	if findErr == nil {
		hash = user.PasswordHash
	}
	valid, needsRehash, verifyErr := service.hasher.Verify(password, hash)
	if verifyErr != nil {
		valid = false
	}
	if findErr != nil || !valid || !user.Active {
		if recordErr := service.store.RecordLoginFailure(ctx, rateKey); recordErr != nil {
			return SessionTokens{}, fmt.Errorf("auth: recording login failure: %w", recordErr)
		}
		return SessionTokens{}, ErrInvalidCredentials
	}

	sessionToken, err := service.newToken()
	if err != nil {
		return SessionTokens{}, err
	}
	csrfToken, err := service.newToken()
	if err != nil {
		return SessionTokens{}, err
	}
	var replacementHash []byte
	if needsRehash {
		rehashed, hashErr := service.hasher.Hash(password)
		if hashErr != nil {
			return SessionTokens{}, hashErr
		}
		replacementHash = []byte(rehashed)
	}
	newSession := NewSession{
		UserID: user.ID, TokenHash: TokenHash(sessionToken), CSRFTokenHash: TokenHash(csrfToken),
		IdleExpiresAt: now.Add(service.idleTTL), AbsoluteExpiresAt: now.Add(service.absoluteTTL),
	}
	if err := service.store.RotateLogin(ctx, user, newSession, replacementHash, rateKey, requestID); err != nil {
		return SessionTokens{}, fmt.Errorf("auth: rotating login: %w", err)
	}
	return SessionTokens{SessionToken: sessionToken, CSRFToken: csrfToken, Actor: actorFromUser(user)}, nil
}

// Authenticate resolves a raw opaque token and enforces idle/absolute expiry and user state.
func (service *Service) Authenticate(ctx context.Context, rawToken string) (Session, error) {
	if rawToken == "" {
		return Session{}, ErrInvalidSession
	}
	session, err := service.store.FindSession(ctx, TokenHash(rawToken))
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	now := service.now()
	if session.RevokedAt != nil || !session.UserActive || !session.Actor.Role.Valid() ||
		!now.Before(session.IdleExpiresAt) || !now.Before(session.AbsoluteExpiresAt) {
		return Session{}, ErrInvalidSession
	}
	if err := service.store.TouchSession(ctx, session.ID, now.Add(service.idleTTL)); err != nil {
		return Session{}, fmt.Errorf("auth: extending session: %w", err)
	}
	return session, nil
}

// ValidateCSRF compares a presented synchronizer token to the session hash.
func (service *Service) ValidateCSRF(session Session, rawToken string) bool {
	if rawToken == "" || len(session.CSRFTokenHash) != 32 {
		return false
	}
	return subtle.ConstantTimeCompare(session.CSRFTokenHash, TokenHash(rawToken)) == 1
}

// Logout revokes the current session hash.
func (service *Service) Logout(ctx context.Context, rawToken string) error {
	if rawToken == "" {
		return nil
	}
	if err := service.store.RevokeSession(ctx, TokenHash(rawToken)); err != nil {
		return fmt.Errorf("auth: revoking session: %w", err)
	}
	return nil
}

// ListUsers requires user-management permission.
func (service *Service) ListUsers(ctx context.Context, actor Actor) ([]UserSummary, error) {
	if err := actor.Require(PermissionUserManage); err != nil {
		return nil, err
	}
	return service.store.ListUsers(ctx)
}

// FindUserForAdministration resolves one user without returning a password hash.
func (service *Service) FindUserForAdministration(ctx context.Context, actor Actor, username string) (UserSummary, error) {
	if err := actor.Require(PermissionUserManage); err != nil {
		return UserSummary{}, err
	}
	user, err := service.store.FindUserByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		return UserSummary{}, err
	}
	return UserSummary{
		ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Email: user.Email,
		Role: user.Role, MustChangePassword: user.MustChangePassword, Active: user.Active,
		Version: user.Version, DriverID: user.DriverID,
	}, nil
}

// CreateUser validates role/password and delegates one atomic admin mutation.
func (service *Service) CreateUser(ctx context.Context, actor Actor, input CreateUserInput) (string, error) {
	if err := actor.Require(PermissionUserManage); err != nil {
		return "", err
	}
	if !input.Role.Valid() || strings.TrimSpace(input.Username) == "" || strings.TrimSpace(input.DisplayName) == "" {
		return "", errors.New("auth: invalid user input")
	}
	hash, err := service.hasher.Hash(input.Password)
	if err != nil {
		return "", err
	}
	return service.store.CreateUser(ctx, actor, input, hash)
}

// UpdateUserAccess enforces admin permission; the store protects the last active admin atomically.
func (service *Service) UpdateUserAccess(ctx context.Context, actor Actor, input UpdateAccessInput) error {
	if err := actor.Require(PermissionUserManage); err != nil {
		return err
	}
	if !input.Role.Valid() || input.ExpectedVersion < 1 {
		return errors.New("auth: invalid access update")
	}
	return service.store.UpdateUserAccess(ctx, actor, input)
}

// ResetPassword forces change-on-login and revokes all existing sessions.
func (service *Service) ResetPassword(ctx context.Context, actor Actor, input ResetPasswordInput) error {
	if err := actor.Require(PermissionUserManage); err != nil {
		return err
	}
	hash, err := service.hasher.Hash(input.Password)
	if err != nil {
		return err
	}
	return service.store.ResetPassword(ctx, actor, input, hash)
}

// ChangeOwnPassword removes must-change-password and invalidates other sessions at the store boundary.
func (service *Service) ChangeOwnPassword(ctx context.Context, actor Actor, password string, expectedVersion int32) error {
	if actor.UserID == "" {
		return ErrForbidden
	}
	hash, err := service.hasher.Hash(password)
	if err != nil {
		return err
	}
	return service.store.ChangeOwnPassword(ctx, actor, hash, expectedVersion)
}

func actorFromUser(user User) Actor {
	return Actor{UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Role: user.Role, DriverID: user.DriverID, MustChangePassword: user.MustChangePassword, UserVersion: user.Version}
}
