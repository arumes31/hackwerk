//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/config"
	"github.com/pquerna/otp/totp"
)

func TestIdentityPersistenceAndLastAdmin(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, os.Stdout); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Database: config.Database{URL: databaseURL, MaxConnections: 5, MinConnections: 0, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second}}
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "TRUNCATE audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	store := postgres.NewIdentityStore(pool)
	service, err := auth.NewService(store, hasher, time.Now, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	system := auth.Actor{Role: auth.RoleAdmin, System: true}
	adminID, err := service.CreateUser(ctx, system, auth.CreateUserInput{Username: "Admin", DisplayName: "Anna Admin", Role: auth.RoleAdmin, Password: "Ein sicheres Adminpasswort 2026", RequestID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateUser(ctx, system, auth.CreateUserInput{Username: "admin", DisplayName: "Dublette", Role: auth.RoleDriver, Password: "Ein sicheres Fahrerpasswort 2026"}); !errors.Is(err, auth.ErrConflict) {
		t.Fatalf("case-insensitive duplicate error = %v", err)
	}
	admin, err := service.FindUserForAdministration(ctx, system, "ADMIN")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateUserAccess(ctx, system, auth.UpdateAccessInput{UserID: adminID, Role: auth.RoleDriver, Active: true, ExpectedVersion: admin.Version}); !errors.Is(err, auth.ErrLastAdmin) {
		t.Fatalf("last admin update error = %v", err)
	}
	driverUserID, err := service.CreateUser(ctx, system, auth.CreateUserInput{Username: "driver", DisplayName: "Franz Fahrer", Role: auth.RoleDriver, Password: "Ein sicheres Fahrerpasswort 2026", CreateDriver: true})
	if err != nil {
		t.Fatal(err)
	}
	users, err := service.ListUsers(ctx, system)
	if err != nil || len(users) != 2 || users[1].DriverID == "" {
		t.Fatalf("ListUsers() len = %d, err = %v", len(users), err)
	}
	driverUser, err := service.FindUserForAdministration(ctx, system, "driver")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateUserDetails(ctx, system, auth.UpdateUserDetailsInput{
		UserID: driverUserID, Username: "driver-neu", DisplayName: "Neuer Kontoname",
		Email: "konto@example.test", ExpectedVersion: driverUser.Version, RequestID: "details-request",
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := service.FindUserForAdministration(ctx, system, "DRIVER-NEU")
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Neuer Kontoname" || updated.Email != "konto@example.test" || updated.Version != driverUser.Version+1 {
		t.Fatalf("updated user = %#v", updated)
	}
	var driverDisplayName, driverEmail string
	if err := pool.QueryRow(ctx, `SELECT display_name, COALESCE(email::text, '') FROM drivers WHERE user_id = $1`, driverUserID).Scan(&driverDisplayName, &driverEmail); err != nil {
		t.Fatal(err)
	}
	if driverDisplayName != "Franz Fahrer" || driverEmail != "" {
		t.Fatalf("driver profile changed with user details: display=%q email=%q", driverDisplayName, driverEmail)
	}
	if err := service.UpdateUserDetails(ctx, system, auth.UpdateUserDetailsInput{
		UserID: driverUserID, Username: "nochmals", DisplayName: "Nochmals",
		ExpectedVersion: driverUser.Version,
	}); !errors.Is(err, auth.ErrConflict) {
		t.Fatalf("stale details update error = %v", err)
	}
	if err := service.UpdateUserDetails(ctx, system, auth.UpdateUserDetailsInput{
		UserID: driverUserID, Username: "ADMIN", DisplayName: "Dublette",
		ExpectedVersion: updated.Version,
	}); !errors.Is(err, auth.ErrConflict) {
		t.Fatalf("duplicate username update error = %v", err)
	}
	var auditMetadata string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM audit_events WHERE action = 'user.details_updated' AND object_id = $1`, driverUserID).Scan(&auditMetadata); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"username", "display_name", "email"} {
		if !strings.Contains(auditMetadata, field) {
			t.Fatalf("audit metadata %q does not contain changed field %q", auditMetadata, field)
		}
	}
	if strings.Contains(auditMetadata, "konto@example.test") || strings.Contains(auditMetadata, "Neuer Kontoname") {
		t.Fatalf("audit metadata contains user values: %q", auditMetadata)
	}
}

func TestLoginStoresHashAndResetRevokesSession(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, os.Stdout); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(ctx, config.Database{URL: databaseURL, MaxConnections: 5, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "TRUNCATE audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}
	hasher, _ := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	service, err := auth.NewService(postgres.NewIdentityStore(pool), hasher, time.Now, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	system := auth.Actor{Role: auth.RoleAdmin, System: true}
	_, err = service.CreateUser(ctx, system, auth.CreateUserInput{Username: "admin", DisplayName: "Admin", Role: auth.RoleAdmin, Password: "Ein sicheres Adminpasswort 2026"})
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := service.Login(ctx, "ADMIN", "Ein sicheres Adminpasswort 2026", "client", "request")
	if err != nil {
		t.Fatal(err)
	}
	var storedHash []byte
	if err := pool.QueryRow(ctx, "SELECT token_hash FROM sessions LIMIT 1").Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	if string(storedHash) == tokens.SessionToken || string(storedHash) != string(auth.TokenHash(tokens.SessionToken)) {
		t.Fatal("session token is not stored exclusively as its hash")
	}
	admin, err := service.FindUserForAdministration(ctx, system, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResetPassword(ctx, system, auth.ResetPasswordInput{UserID: admin.ID, Password: "Ein neues Adminpasswort 2026", ExpectedVersion: admin.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, tokens.SessionToken); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("revoked session Authenticate() error = %v", err)
	}
}

func TestPersonalProfileSecurityLifecycle(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, os.Stdout); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(ctx, config.Database{URL: databaseURL, MaxConnections: 5, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, "TRUNCATE user_webauthn_registration_challenges, auth_login_challenges, user_recovery_codes, user_webauthn_credentials, user_totp_credentials, user_email_verifications, outbox_events, audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	store := postgres.NewIdentityStore(pool)
	service, err := auth.NewService(store, hasher, func() time.Time { return now }, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := auth.NewSecurityKeyRing(map[string]string{"test-v1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))}, "test-v1")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureSecurity(auth.SecurityConfig{
		Keys: keyring, AppName: "HackWerk Test", BaseURL: "https://hackwerk.test",
		EmailVerificationTTL: 24 * time.Hour, EmailResendInterval: time.Minute,
		MFAChallengeTTL: 5 * time.Minute, WebAuthnChallengeTTL: 5 * time.Minute, MailMaxAttempts: 6,
	}); err != nil {
		t.Fatal(err)
	}
	system := auth.Actor{Role: auth.RoleAdmin, System: true}
	userID, err := service.CreateUser(ctx, system, auth.CreateUserInput{Username: "profile-driver", DisplayName: "Profil Fahrer", Role: auth.RoleDriver, Password: "Ein sicheres Fahrerpasswort 2026", CreateDriver: true})
	if err != nil {
		t.Fatal(err)
	}
	password := "Ein sicheres Fahrerpasswort 2026"
	first, err := service.LoginWithDevice(ctx, "profile-driver", password, "client-a", "Chrome auf Windows", "login-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.LoginWithDevice(ctx, "profile-driver", password, "client-b", "Safari auf iPhone/iPad", "login-b")
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionToken == "" {
		t.Fatal("second parallel session token is empty")
	}
	profile, err := service.Profile(ctx, first.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if profile.DriverID == "" || len(profile.Sessions) != 2 {
		t.Fatalf("profile driver=%q sessions=%d", profile.DriverID, len(profile.Sessions))
	}
	if err := service.UpdateOwnProfile(ctx, first.Actor, auth.UpdateOwnProfileInput{DisplayName: "Profil Fahrer Neu", Salutation: "Herr", WorkPhoneRaw: "0664 / 123 45 67", ExpectedVersion: profile.Version, RequestID: "profile-update"}); err != nil {
		t.Fatal(err)
	}
	profile, err = service.Profile(ctx, first.Actor)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Salutation != "herr" || profile.WorkPhoneNormalized != "+436641234567" {
		t.Fatalf("normalized profile = %#v", profile.User)
	}

	if err := service.RequestEmailChange(ctx, first.Actor, " Fahrer@EXAMPLE.AT ", "email-request"); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingEmailVerification(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Email != "Fahrer@example.at" {
		t.Fatalf("pending email = %q", pending.Email)
	}
	if err := service.ResendEmailChange(ctx, first.Actor, "too-soon"); !errors.Is(err, auth.ErrRateLimited) {
		t.Fatalf("early resend error = %v", err)
	}
	token, err := keyring.ReconstructEmailToken(pending.TokenKeyID, pending.ID, pending.UserID, pending.TokenVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyEmail(ctx, token, "email-verify"); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyEmail(ctx, token, "email-replay"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("email replay error = %v", err)
	}
	var storedEmail string
	if err := pool.QueryRow(ctx, "SELECT email::text FROM users WHERE id=$1", userID).Scan(&storedEmail); err != nil || storedEmail != "Fahrer@example.at" {
		t.Fatalf("stored email = %q, %v", storedEmail, err)
	}

	enrollment, err := service.BeginTOTPEnrollment(ctx, first.Actor, "Diensthandy", "totp-start")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(enrollment.Secret, now)
	if err != nil {
		t.Fatal(err)
	}
	recoveryCodes, err := service.ConfirmTOTPEnrollment(ctx, first.Actor, code, "totp-confirm")
	if err != nil || len(recoveryCodes) != 8 {
		t.Fatalf("TOTP confirm codes=%d err=%v", len(recoveryCodes), err)
	}
	challenge, err := service.LoginWithDevice(ctx, "profile-driver", password, "client-c", "Firefox auf Linux", "mfa-login")
	if err != nil || !challenge.MFARequired || challenge.SessionToken != "" {
		t.Fatalf("MFA challenge = %#v, %v", challenge, err)
	}
	completed, err := service.CompleteTOTPLogin(ctx, challenge.MFAChallengeToken, code, "Firefox auf Linux", "mfa-complete")
	if err != nil || completed.SessionToken == "" {
		t.Fatalf("TOTP login = %#v, %v", completed, err)
	}
	replayChallenge, err := service.LoginWithDevice(ctx, "profile-driver", password, "client-d", "Chrome auf Windows", "mfa-replay-start")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteTOTPLogin(ctx, replayChallenge.MFAChallengeToken, code, "Chrome auf Windows", "mfa-replay"); !errors.Is(err, auth.ErrInvalidMFA) {
		t.Fatalf("TOTP replay error = %v", err)
	}
	recoveryChallenge, err := service.LoginWithDevice(ctx, "profile-driver", password, "client-e", "Edge auf Windows", "recovery-start")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteRecoveryLogin(ctx, recoveryChallenge.MFAChallengeToken, recoveryCodes[0], "Edge auf Windows", "recovery-complete"); err != nil {
		t.Fatal(err)
	}
	recoveryReplay, err := service.LoginWithDevice(ctx, "profile-driver", password, "client-f", "Edge auf Windows", "recovery-replay-start")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteRecoveryLogin(ctx, recoveryReplay.MFAChallengeToken, recoveryCodes[0], "Edge auf Windows", "recovery-replay"); !errors.Is(err, auth.ErrInvalidMFA) {
		t.Fatalf("recovery replay error = %v", err)
	}

	otherID, err := service.CreateUser(ctx, system, auth.CreateUserInput{Username: "other-driver", DisplayName: "Other", Role: auth.RoleDriver, Password: "Ein anderes sicheres Passwort 2026"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeSession(ctx, auth.Actor{UserID: otherID, Role: auth.RoleDriver}, profile.Sessions[0].ID, "idor"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("foreign session revoke error = %v", err)
	}
	adminView, err := service.FindUserForAdministration(ctx, system, "profile-driver")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResetUserSecurity(ctx, system, userID, adminView.Version, "security-recovery"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, first.SessionToken); !errors.Is(err, auth.ErrInvalidSession) {
		t.Fatalf("security reset left session active: %v", err)
	}
	var totpCount, passkeyCount, recoveryCount, activeSessionCount int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM user_totp_credentials WHERE user_id=$1),
		(SELECT count(*) FROM user_webauthn_credentials WHERE user_id=$1),
		(SELECT count(*) FROM user_recovery_codes WHERE user_id=$1),
		(SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL)`, userID).Scan(&totpCount, &passkeyCount, &recoveryCount, &activeSessionCount); err != nil {
		t.Fatal(err)
	}
	if totpCount != 0 || passkeyCount != 0 || recoveryCount != 0 || activeSessionCount != 0 {
		t.Fatalf("security reset counts = %d/%d/%d/%d", totpCount, passkeyCount, recoveryCount, activeSessionCount)
	}
}
