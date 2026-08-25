package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeStore struct {
	user             User
	findUserError    error
	rate             RateLimit
	recordedFailures int
	rotated          NewSession
	session          Session
	touched          bool
}

func (store *fakeStore) FindUserByUsername(context.Context, string) (User, error) {
	return store.user, store.findUserError
}
func (store *fakeStore) FindUserByID(context.Context, string) (User, error) {
	return store.user, store.findUserError
}
func (store *fakeStore) RotateLogin(_ context.Context, _ User, session NewSession, _ []byte, _ []byte, _ string) error {
	store.rotated = session
	return nil
}
func (store *fakeStore) FindSession(context.Context, []byte) (Session, error) {
	return store.session, nil
}
func (store *fakeStore) TouchSession(context.Context, string, time.Time) error {
	store.touched = true
	return nil
}
func (store *fakeStore) RevokeSession(context.Context, []byte) error          { return nil }
func (store *fakeStore) LoginRate(context.Context, []byte) (RateLimit, error) { return store.rate, nil }
func (store *fakeStore) RecordLoginFailure(context.Context, []byte) error {
	store.recordedFailures++
	return nil
}
func (store *fakeStore) ListUsers(context.Context) ([]UserSummary, error) { return nil, nil }
func (store *fakeStore) CreateUser(context.Context, Actor, CreateUserInput, string) (string, error) {
	return "user", nil
}
func (store *fakeStore) UpdateUserAccess(context.Context, Actor, UpdateAccessInput) error { return nil }
func (store *fakeStore) ResetPassword(context.Context, Actor, ResetPasswordInput, string) error {
	return nil
}
func (store *fakeStore) ChangeOwnPassword(context.Context, Actor, string, int32) error { return nil }

func testService(t *testing.T, store *fakeStore, now time.Time) (*Service, PasswordHasher) {
	t.Helper()
	hasher, err := NewPasswordHasher(PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 3)
	if err != nil {
		t.Fatal(err)
	}
	return service, hasher
}

func TestLoginStoresOnlyTokenHashes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{}
	service, hasher := testService(t, store, now)
	hash, err := hasher.Hash("Ein gutes Testpasswort 2026")
	if err != nil {
		t.Fatal(err)
	}
	store.user = User{ID: "user", Username: "anna", DisplayName: "Anna", Role: RoleAdmin, PasswordHash: hash, Active: true, Version: 1}
	tokens, err := service.Login(context.Background(), "anna", "Ein gutes Testpasswort 2026", "client", "request")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.SessionToken == "" || tokens.CSRFToken == "" {
		t.Fatal("raw browser tokens are empty")
	}
	if string(store.rotated.TokenHash) == tokens.SessionToken || string(store.rotated.CSRFTokenHash) == tokens.CSRFToken {
		t.Fatal("raw token reached persistence input")
	}
	if string(store.rotated.TokenHash) != string(TokenHash(tokens.SessionToken)) {
		t.Fatal("session token hash mismatch")
	}
}

func TestLoginFailureIsGenericAndRateLimited(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{findUserError: ErrNotFound}
	service, _ := testService(t, store, now)
	_, err := service.Login(context.Background(), "unknown", "Falsches Testpasswort 2026", "client", "request")
	if !errors.Is(err, ErrInvalidCredentials) || store.recordedFailures != 1 {
		t.Fatalf("Login() error = %v, failures = %d", err, store.recordedFailures)
	}
	store.rate = RateLimit{WindowStartedAt: now.Add(-10 * time.Second), FailureCount: 3}
	_, err = service.Login(context.Background(), "unknown", "Falsches Testpasswort 2026", "client", "request")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Login() error = %v", err)
	}
}

func TestAuthenticateExpiryAndCSRF(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	store := &fakeStore{session: Session{ID: "session", Actor: Actor{UserID: "user", Role: RoleDriver}, UserActive: true, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour), CSRFTokenHash: TokenHash("csrf")}}
	service, _ := testService(t, store, now)
	session, err := service.Authenticate(context.Background(), "session-token")
	if err != nil || !store.touched {
		t.Fatalf("Authenticate() error = %v, touched = %v", err, store.touched)
	}
	if !service.ValidateCSRF(session, "csrf") || service.ValidateCSRF(session, "wrong") {
		t.Fatal("CSRF validation is incorrect")
	}
	store.session.IdleExpiresAt = now
	_, err = service.Authenticate(context.Background(), "session-token")
	if !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("expired Authenticate() error = %v", err)
	}
}
