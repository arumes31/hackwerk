package auth

import (
	"context"
	"errors"
	"strings"
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
	touchError       error
	revokedToken     []byte
	revokeError      error
	users            []UserSummary
	listUsersError   error
	createdInput     CreateUserInput
	createdHash      string
	createUserID     string
	createUserError  error
	updatedDetails   UpdateUserDetailsInput
	updateDetailsErr error
	updatedAccess    UpdateAccessInput
	updateAccessErr  error
	resetInput       ResetPasswordInput
	resetHash        string
	resetError       error
	changedActor     Actor
	changedHash      string
	changedVersion   int32
	changeError      error
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
	return store.touchError
}
func (store *fakeStore) RevokeSession(_ context.Context, token []byte) error {
	store.revokedToken = append([]byte(nil), token...)
	return store.revokeError
}
func (store *fakeStore) LoginRate(context.Context, []byte) (RateLimit, error) { return store.rate, nil }
func (store *fakeStore) RecordLoginFailure(context.Context, []byte) error {
	store.recordedFailures++
	return nil
}
func (store *fakeStore) ListUsers(context.Context) ([]UserSummary, error) {
	return store.users, store.listUsersError
}
func (store *fakeStore) CreateUser(_ context.Context, _ Actor, input CreateUserInput, hash string) (string, error) {
	store.createdInput, store.createdHash = input, hash
	if store.createUserID == "" {
		store.createUserID = "user"
	}
	return store.createUserID, store.createUserError
}
func (store *fakeStore) UpdateUserDetails(_ context.Context, _ Actor, input UpdateUserDetailsInput) error {
	store.updatedDetails = input
	return store.updateDetailsErr
}
func (store *fakeStore) UpdateUserAccess(_ context.Context, _ Actor, input UpdateAccessInput) error {
	store.updatedAccess = input
	return store.updateAccessErr
}
func (store *fakeStore) ResetPassword(_ context.Context, _ Actor, input ResetPasswordInput, hash string) error {
	store.resetInput, store.resetHash = input, hash
	return store.resetError
}
func (store *fakeStore) ChangeOwnPassword(_ context.Context, actor Actor, hash string, version int32) error {
	store.changedActor, store.changedHash, store.changedVersion = actor, hash, version
	return store.changeError
}

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

func TestLoginFailsClosedWhenSecondFactorStoreIsUnavailable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	store := &fakeStore{}
	service, hasher := testService(t, store, now)
	// #nosec G101 -- deterministic test-only password fixture.
	password := "Ein sicheres Fahrerpasswort 2026"
	hash, err := hasher.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	store.user = User{ID: "user", Username: "driver", DisplayName: "Driver", Role: RoleDriver, PasswordHash: hash, Active: true, Version: 1, TOTPEnabled: true}
	if _, err := service.LoginWithDevice(t.Context(), "driver", password, "client", "Chrome auf Windows", "request"); !errors.Is(err, ErrInvalidMFA) {
		t.Fatalf("login error = %v", err)
	}
	if store.rotated.UserID != "" {
		t.Fatal("login created a session while MFA dependencies were unavailable")
	}
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

func TestUpdateUserDetailsValidatesAndNormalizes(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	service, _ := testService(t, store, time.Now())
	admin := Actor{UserID: "admin", Role: RoleAdmin}

	err := service.UpdateUserDetails(t.Context(), admin, UpdateUserDetailsInput{
		UserID: "user", Username: "  neue-anmeldung  ", DisplayName: "  Neuer Name  ",
		Email: "  neu@example.test  ", ExpectedVersion: 3, RequestID: "request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.updatedDetails.Username != "neue-anmeldung" || store.updatedDetails.DisplayName != "Neuer Name" ||
		store.updatedDetails.Email != "neu@example.test" || store.updatedDetails.ExpectedVersion != 3 {
		t.Fatalf("UpdateUserDetails() input = %#v", store.updatedDetails)
	}
}

func TestUpdateUserDetailsRejectsForbiddenAndInvalidInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		actor    Actor
		input    UpdateUserDetailsInput
		expected error
	}{
		{
			name: "driver forbidden", actor: Actor{UserID: "driver", Role: RoleDriver},
			input:    UpdateUserDetailsInput{UserID: "user", Username: "name", DisplayName: "Name", ExpectedVersion: 1},
			expected: ErrForbidden,
		},
		{
			name: "invalid email", actor: Actor{UserID: "admin", Role: RoleAdmin},
			input:    UpdateUserDetailsInput{UserID: "user", Username: "name", DisplayName: "Name", Email: "Name <mail@example.test>", ExpectedVersion: 1},
			expected: ErrInvalidInput,
		},
		{
			name: "stale version", actor: Actor{UserID: "admin", Role: RoleAdmin},
			input:    UpdateUserDetailsInput{UserID: "user", Username: "name", DisplayName: "Name", ExpectedVersion: 0},
			expected: ErrInvalidInput,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{}
			service, _ := testService(t, store, time.Now())
			err := service.UpdateUserDetails(t.Context(), test.actor, test.input)
			if !errors.Is(err, test.expected) {
				t.Fatalf("UpdateUserDetails() error = %v, want %v", err, test.expected)
			}
			if store.updatedDetails.UserID != "" {
				t.Fatalf("invalid update reached store: %#v", store.updatedDetails)
			}
		})
	}
}

func TestLogoutHashesTokenAndWrapsStoreError(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	service, _ := testService(t, store, time.Now())
	if err := service.Logout(t.Context(), ""); err != nil || store.revokedToken != nil {
		t.Fatalf("empty Logout() = %v, token = %x", err, store.revokedToken)
	}
	if err := service.Logout(t.Context(), "opaque-session-token"); err != nil {
		t.Fatal(err)
	}
	if string(store.revokedToken) != string(TokenHash("opaque-session-token")) {
		t.Fatal("Logout() did not pass the token hash to the store")
	}
	store.revokeError = errors.New("store down")
	if err := service.Logout(t.Context(), "other-token"); err == nil || !strings.Contains(err.Error(), "auth: revoking session") {
		t.Fatalf("Logout() error = %v", err)
	}
}

func TestAuthenticateRejectsInvalidStatesAndStoreFailures(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	valid := Session{ID: "session", Actor: Actor{UserID: "user", Role: RoleDriver}, UserActive: true, IdleExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(2 * time.Hour)}
	tests := []struct {
		name   string
		mutate func(*fakeStore)
		want   error
	}{
		{name: "empty token", mutate: func(*fakeStore) {}, want: ErrInvalidSession},
		{name: "revoked", mutate: func(store *fakeStore) { revoked := now; store.session.RevokedAt = &revoked }, want: ErrInvalidSession},
		{name: "inactive user", mutate: func(store *fakeStore) { store.session.UserActive = false }, want: ErrInvalidSession},
		{name: "invalid role", mutate: func(store *fakeStore) { store.session.Actor.Role = "unknown" }, want: ErrInvalidSession},
		{name: "absolute expiry", mutate: func(store *fakeStore) { store.session.AbsoluteExpiresAt = now }, want: ErrInvalidSession},
		{name: "touch failure", mutate: func(store *fakeStore) { store.touchError = errors.New("write failed") }, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{session: valid}
			test.mutate(store)
			service, _ := testService(t, store, now)
			token := "token"
			if test.name == "empty token" {
				token = ""
			}
			_, err := service.Authenticate(t.Context(), token)
			if test.want != nil {
				if !errors.Is(err, test.want) {
					t.Fatalf("Authenticate() error = %v, want %v", err, test.want)
				}
			} else if err == nil || !strings.Contains(err.Error(), "auth: extending session") {
				t.Fatalf("Authenticate() error = %v", err)
			}
		})
	}
}

func TestUserAdministrationLifecycle(t *testing.T) {
	t.Parallel()
	store := &fakeStore{user: User{ID: "u-1", Username: "maria", DisplayName: "Maria Muster", Email: "maria@example.test", Role: RoleDriver, MustChangePassword: true, Active: true, Version: 4, DriverID: "d-1"}, users: []UserSummary{{ID: "u-2", Username: "anna"}}}
	service, hasher := testService(t, store, time.Now())
	admin := Actor{UserID: "admin", Role: RoleAdmin}

	users, err := service.ListUsers(t.Context(), admin)
	if err != nil || len(users) != 1 || users[0].ID != "u-2" {
		t.Fatalf("ListUsers() = %#v, %v", users, err)
	}
	found, err := service.FindUserForAdministration(t.Context(), admin, " maria ")
	if err != nil || found.ID != "u-1" || found.DriverID != "d-1" || found.MustChangePassword != true {
		t.Fatalf("FindUserForAdministration() = %#v, %v", found, err)
	}
	// #nosec G101 -- deterministic non-secret test fixture password.
	id, err := service.CreateUser(t.Context(), admin, CreateUserInput{Username: "  neu  ", DisplayName: " Neue Person ", Email: " neu@example.test ", Role: RoleDriver, Password: "Ein gutes Testpasswort 2026"})
	if err != nil || id != "user" || store.createdInput.Username != "neu" || store.createdInput.DisplayName != "Neue Person" {
		t.Fatalf("CreateUser() = %q, %v; input = %#v", id, err, store.createdInput)
	}
	if valid, _, err := hasher.Verify("Ein gutes Testpasswort 2026", store.createdHash); err != nil || !valid {
		t.Fatalf("CreateUser() stored hash validation = %v, %v", valid, err)
	}
	if err := service.UpdateUserAccess(t.Context(), admin, UpdateAccessInput{UserID: "u-1", Role: RoleAdmin, Active: true, ExpectedVersion: 4}); err != nil || store.updatedAccess.UserID != "u-1" {
		t.Fatalf("UpdateUserAccess() = %v, %#v", err, store.updatedAccess)
	}
	// #nosec G101 -- deterministic non-secret test fixture password.
	if err := service.ResetPassword(t.Context(), admin, ResetPasswordInput{UserID: "u-1", Password: "Ein weiteres Testpasswort 2026", ExpectedVersion: 4}); err != nil {
		t.Fatal(err)
	}
	if valid, _, err := hasher.Verify("Ein weiteres Testpasswort 2026", store.resetHash); err != nil || !valid || store.resetInput.UserID != "u-1" {
		t.Fatalf("ResetPassword() stored hash/input = %v, %v, %#v", valid, err, store.resetInput)
	}
	if err := service.ChangeOwnPassword(t.Context(), Actor{UserID: "u-1", Role: RoleDriver}, "Ein drittes Testpasswort 2026", 4); err != nil {
		t.Fatal(err)
	}
	if valid, _, err := hasher.Verify("Ein drittes Testpasswort 2026", store.changedHash); err != nil || !valid || store.changedVersion != 4 {
		t.Fatalf("ChangeOwnPassword() stored hash/version = %v, %v, %d", valid, err, store.changedVersion)
	}
}

func TestUserAdministrationRejectsUnauthorizedInvalidAndStoreErrors(t *testing.T) {
	t.Parallel()
	store := &fakeStore{listUsersError: errors.New("list failed"), findUserError: errors.New("find failed"), createUserError: errors.New("create failed"), updateAccessErr: errors.New("update failed"), resetError: errors.New("reset failed"), changeError: errors.New("change failed")}
	service, _ := testService(t, store, time.Now())
	driver := Actor{UserID: "driver", Role: RoleDriver}
	admin := Actor{UserID: "admin", Role: RoleAdmin}
	if _, err := service.ListUsers(t.Context(), driver); !errors.Is(err, ErrForbidden) {
		t.Fatalf("driver ListUsers() error = %v", err)
	}
	if _, err := service.ListUsers(t.Context(), admin); err == nil {
		t.Fatal("ListUsers() accepted store failure")
	}
	if _, err := service.FindUserForAdministration(t.Context(), admin, "user"); err == nil {
		t.Fatal("FindUserForAdministration() accepted store failure")
	}
	if _, err := service.CreateUser(t.Context(), driver, CreateUserInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("driver CreateUser() error = %v", err)
	}
	// #nosec G101 -- deterministic non-secret test fixture password.
	if _, err := service.CreateUser(t.Context(), admin, CreateUserInput{Username: "name", DisplayName: "Name", Role: Role("invalid"), Password: "Ein gutes Testpasswort 2026"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid CreateUser() error = %v", err)
	}
	// #nosec G101 -- deterministic non-secret test fixture password.
	if _, err := service.CreateUser(t.Context(), admin, CreateUserInput{Username: "name", DisplayName: "Name", Role: RoleDriver, Password: "Ein gutes Testpasswort 2026"}); err == nil {
		t.Fatal("CreateUser() accepted store failure")
	}
	if err := service.UpdateUserAccess(t.Context(), driver, UpdateAccessInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("driver UpdateUserAccess() error = %v", err)
	}
	if err := service.UpdateUserAccess(t.Context(), admin, UpdateAccessInput{Role: Role("invalid"), ExpectedVersion: 1}); err == nil {
		t.Fatal("UpdateUserAccess() accepted invalid role")
	}
	if err := service.UpdateUserAccess(t.Context(), admin, UpdateAccessInput{Role: RoleDriver, ExpectedVersion: 1}); err == nil {
		t.Fatal("UpdateUserAccess() accepted store failure")
	}
	if err := service.ResetPassword(t.Context(), driver, ResetPasswordInput{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("driver ResetPassword() error = %v", err)
	}
	// #nosec G101 -- deterministic non-secret test fixture password.
	if err := service.ResetPassword(t.Context(), admin, ResetPasswordInput{Password: "Ein gutes Testpasswort 2026"}); err == nil {
		t.Fatal("ResetPassword() accepted store failure")
	}
	if err := service.ChangeOwnPassword(t.Context(), Actor{}, "Ein gutes Testpasswort 2026", 1); !errors.Is(err, ErrForbidden) {
		t.Fatalf("anonymous ChangeOwnPassword() error = %v", err)
	}
	if err := service.ChangeOwnPassword(t.Context(), driver, "Ein gutes Testpasswort 2026", 1); err == nil {
		t.Fatal("ChangeOwnPassword() accepted store failure")
	}
}
