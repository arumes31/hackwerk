package app

import (
	"fmt"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IdentityService wires the shared password/session service for HTTP, CLI, and seed flows.
func IdentityService(cfg config.Config, pool *pgxpool.Pool) (*auth.Service, error) {
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{
		MemoryKiB: cfg.Auth.Argon2MemoryKiB, Iterations: cfg.Auth.Argon2Iterations,
		Parallelism: cfg.Auth.Argon2Parallelism, SaltLength: 16, KeyLength: 32,
		MinLength: cfg.Auth.PasswordMinLength,
	})
	if err != nil {
		return nil, fmt.Errorf("app: creating password hasher: %w", err)
	}
	service, err := auth.NewService(
		postgres.NewIdentityStore(pool), hasher, time.Now,
		cfg.Auth.SessionIdleTTL, cfg.Auth.SessionAbsoluteTTL, cfg.Auth.LoginLimitPerMinute,
	)
	if err != nil {
		return nil, fmt.Errorf("app: creating identity service: %w", err)
	}
	securityKeys, err := auth.NewSecurityKeyRing(cfg.Auth.SecurityKeys, cfg.Auth.SecurityCurrentKeyID)
	if err != nil {
		return nil, fmt.Errorf("app: creating identity security keys: %w", err)
	}
	if err := service.ConfigureSecurity(auth.SecurityConfig{
		Keys: securityKeys, AppName: cfg.AppName, BaseURL: cfg.BaseURL,
		EmailVerificationTTL: cfg.Auth.EmailVerificationTTL, EmailResendInterval: cfg.Auth.EmailResendInterval,
		MFAChallengeTTL: cfg.Auth.MFAChallengeTTL, WebAuthnChallengeTTL: cfg.Auth.WebAuthnChallengeTTL,
		// #nosec G115 -- validated configuration restricts MAIL_MAX_ATTEMPTS to 1..50.
		MailMaxAttempts: int32(cfg.Mail.MaxAttempts),
	}); err != nil {
		return nil, fmt.Errorf("app: configuring identity security: %w", err)
	}
	return service, nil
}
