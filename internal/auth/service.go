package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// User is the persistence-neutral identity record used by authentication.
type User struct {
	ID                  string
	Username            string
	DisplayName         string
	Email               string
	Role                Role
	PasswordHash        string
	MustChangePassword  bool
	Active              bool
	Version             int32
	DriverID            string
	Salutation          string
	WorkPhoneRaw        string
	WorkPhoneNormalized string
	EmailVerifiedAt     *time.Time
	WebAuthnUserHandle  []byte
	TOTPEnabled         bool
	PasskeyEnabled      bool
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
	CreatedAt         time.Time
	LastUsedAt        time.Time
	DeviceLabel       string
}

// SessionTokens are raw one-time values returned only to the browser boundary.
type SessionTokens struct {
	SessionToken      string
	CSRFToken         string
	Actor             Actor
	MFARequired       bool
	MFAChallengeToken string
}

// NewSession contains only hashed tokens for persistence.
type NewSession struct {
	UserID            string
	TokenHash         []byte
	CSRFTokenHash     []byte
	IdleExpiresAt     time.Time
	AbsoluteExpiresAt time.Time
	DeviceLabel       string
}

// RateLimit is a privacy-minimized login attempt window.
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

// UpdateUserDetailsInput changes login and display metadata without touching a driver profile.
type UpdateUserDetailsInput struct {
	UserID          string
	Username        string
	DisplayName     string
	Email           string
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
	ReserveLoginAttempt(context.Context, []byte, time.Duration) (RateLimit, error)
	ClearLoginAttempts(context.Context, []byte) error
	ListUsers(context.Context) ([]UserSummary, error)
	CreateUser(context.Context, Actor, CreateUserInput, string) (string, error)
	UpdateUserDetails(context.Context, Actor, UpdateUserDetailsInput) error
	UpdateUserAccess(context.Context, Actor, UpdateAccessInput) error
	ResetPassword(context.Context, Actor, ResetPasswordInput, string) error
	ChangeOwnPassword(context.Context, Actor, string, int32) error
}

// accountLoginLimitFactor scales the per-client login limit into the shared
// per-account budget, which applies across all source addresses.
const accountLoginLimitFactor = 3

const (
	// loginAttemptWindow bounds password guessing per client and per account.
	loginAttemptWindow = time.Minute
	// Second-factor guesses share one per-account budget that survives
	// challenge renewal, so a known password cannot replenish TOTP guesses.
	secondFactorAttemptLimit  = 10
	secondFactorAttemptWindow = 15 * time.Minute
)

func loginAccountKey(username string) []byte {
	return TokenHash("login-account\x00" + strings.ToLower(strings.TrimSpace(username)))
}

func secondFactorAccountKey(userID string) []byte {
	return TokenHash("mfa-account\x00" + userID)
}

// Service applies authentication, password, session, and RBAC invariants.
type Service struct {
	store          Store
	hasher         PasswordHasher
	now            func() time.Time
	newToken       func() (string, error)
	idleTTL        time.Duration
	absoluteTTL    time.Duration
	loginLimit     int
	dummyHash      string
	security       SecurityStore
	securityConfig *SecurityConfig
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

// Login verifies credentials generically and creates a parallel server-side session.
func (service *Service) Login(ctx context.Context, username string, password string, clientKey string, requestID string) (SessionTokens, error) {
	return service.LoginWithDevice(ctx, username, password, clientKey, "Unbekanntes Gerät", requestID)
}

// LoginWithDevice verifies the password and either creates a session or starts the required second-factor step.
func (service *Service) LoginWithDevice(ctx context.Context, username string, password string, clientKey string, deviceLabel string, requestID string) (SessionTokens, error) {
	now := service.now()
	normalizedUsername := strings.ToLower(strings.TrimSpace(username))
	rateKey := TokenHash("login\x00" + normalizedUsername + "\x00" + clientKey)
	// Attempts are counted atomically before the password is verified, so
	// parallel requests cannot all pass the check with the same stale count.
	if err := service.reserveAttempt(ctx, rateKey, service.loginLimit, loginAttemptWindow); err != nil {
		return SessionTokens{}, err
	}
	// A second per-account budget caps guessing that rotates source addresses.
	accountKey := loginAccountKey(normalizedUsername)
	if err := service.reserveAttempt(ctx, accountKey, service.loginLimit*accountLoginLimitFactor, loginAttemptWindow); err != nil {
		return SessionTokens{}, err
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
		return SessionTokens{}, ErrInvalidCredentials
	}

	// With a second factor the login budgets are cleared only after that
	// factor succeeds; a correct password alone must not reset them.
	if user.TOTPEnabled || user.PasskeyEnabled {
		if service.securityConfig == nil || service.security == nil {
			return SessionTokens{}, ErrInvalidMFA
		}
		challengeToken, tokenErr := service.newToken()
		if tokenErr != nil {
			return SessionTokens{}, tokenErr
		}
		var replacementHash []byte
		if needsRehash {
			rehashed, hashErr := service.hasher.Hash(password)
			if hashErr != nil {
				return SessionTokens{}, hashErr
			}
			replacementHash = []byte(rehashed)
		}
		if err := service.security.StartLoginChallenge(ctx, user, TokenHash(challengeToken), now.Add(service.securityConfig.MFAChallengeTTL), replacementHash, requestID); err != nil {
			return SessionTokens{}, fmt.Errorf("auth: starting second factor: %w", err)
		}
		return SessionTokens{Actor: actorFromUser(user), MFARequired: true, MFAChallengeToken: challengeToken}, nil
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
		DeviceLabel: normalizeDeviceLabel(deviceLabel),
	}
	if err := service.store.RotateLogin(ctx, user, newSession, replacementHash, rateKey, requestID); err != nil {
		return SessionTokens{}, fmt.Errorf("auth: rotating login: %w", err)
	}
	if err := service.store.ClearLoginAttempts(ctx, accountKey); err != nil {
		return SessionTokens{}, fmt.Errorf("auth: clearing login attempts: %w", err)
	}
	return SessionTokens{SessionToken: sessionToken, CSRFToken: csrfToken, Actor: actorFromUser(user)}, nil
}

// VerifyCurrentPassword re-authenticates a signed-in user before a sensitive
// account change. Attempts share a per-account budget so a stolen session
// cannot be used as an unlimited password oracle.
func (service *Service) VerifyCurrentPassword(ctx context.Context, actor Actor, password string) error {
	if actor.UserID == "" {
		return ErrForbidden
	}
	rateKey := TokenHash("reauth\x00" + actor.UserID)
	if err := service.reserveAttempt(ctx, rateKey, service.loginLimit, loginAttemptWindow); err != nil {
		return err
	}
	user, findErr := service.store.FindUserByID(ctx, actor.UserID)
	hash := service.dummyHash
	if findErr == nil {
		hash = user.PasswordHash
	}
	valid, _, verifyErr := service.hasher.Verify(password, hash)
	if findErr != nil || verifyErr != nil || !valid || !user.Active {
		return ErrInvalidCredentials
	}
	if err := service.store.ClearLoginAttempts(ctx, rateKey); err != nil {
		return fmt.Errorf("auth: clearing re-authentication attempts: %w", err)
	}
	return nil
}

// reserveAttempt counts one attempt before any credential work and rejects it
// once the current window holds more than limit attempts.
func (service *Service) reserveAttempt(ctx context.Context, key []byte, limit int, window time.Duration) error {
	rate, err := service.store.ReserveLoginAttempt(ctx, key, window)
	if err != nil {
		return fmt.Errorf("auth: reserving login attempt: %w", err)
	}
	if rate.FailureCount > limit && service.now().Sub(rate.WindowStartedAt) < window {
		return ErrRateLimited
	}
	return nil
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
	username, displayName, email, err := normalizeUserDetails(input.Username, input.DisplayName, input.Email)
	if err != nil || !input.Role.Valid() {
		return "", ErrInvalidInput
	}
	input.Username = username
	input.DisplayName = displayName
	input.Email = email
	hash, err := service.hasher.Hash(input.Password)
	if err != nil {
		return "", err
	}
	return service.store.CreateUser(ctx, actor, input, hash)
}

// UpdateUserDetails changes only account metadata and leaves a linked driver profile unchanged.
func (service *Service) UpdateUserDetails(ctx context.Context, actor Actor, input UpdateUserDetailsInput) error {
	if err := actor.Require(PermissionUserManage); err != nil {
		return err
	}
	username, displayName, email, err := normalizeUserDetails(input.Username, input.DisplayName, input.Email)
	if err != nil || strings.TrimSpace(input.UserID) == "" || input.ExpectedVersion < 1 {
		return ErrInvalidInput
	}
	input.Username = username
	input.DisplayName = displayName
	input.Email = email
	return service.store.UpdateUserDetails(ctx, actor, input)
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

func normalizeUserDetails(username string, displayName string, email string) (string, string, string, error) {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	email = strings.TrimSpace(email)
	invalidText := username == "" || displayName == "" || len([]rune(username)) > 200 ||
		len([]rune(displayName)) > 200 || strings.ContainsAny(username+displayName, "\r\n")
	if invalidText {
		return "", "", "", ErrInvalidInput
	}
	if email == "" {
		return username, displayName, email, nil
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 320 || strings.ContainsAny(email, "\r\n") {
		return "", "", "", ErrInvalidInput
	}
	return username, displayName, email, nil
}
