//go:build integration

package integration_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/config"
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
	if _, err := service.CreateUser(ctx, system, auth.CreateUserInput{Username: "driver", DisplayName: "Franz Fahrer", Role: auth.RoleDriver, Password: "Ein sicheres Fahrerpasswort 2026", CreateDriver: true}); err != nil {
		t.Fatal(err)
	}
	users, err := service.ListUsers(ctx, system)
	if err != nil || len(users) != 2 || users[1].DriverID == "" {
		t.Fatalf("ListUsers() len = %d, err = %v", len(users), err)
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
