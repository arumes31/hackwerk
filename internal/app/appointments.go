package app

import (
	"fmt"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/notification"
	"github.com/jackc/pgx/v5/pgxpool"
)

func AppointmentService(cfg config.Config, pool *pgxpool.Pool, availability *driver.Service) (*appointment.Service, error) {
	tokens, err := notification.NewKeyRing(cfg.Confirmation.TokenKeys, cfg.Confirmation.CurrentKeyID)
	if err != nil {
		return nil, fmt.Errorf("app: creating confirmation keyring: %w", err)
	}
	store := postgres.NewAppointmentStore(pool, postgres.WithConfirmationPlanning(
		tokens, cfg.Confirmation.TokenTTL, cfg.Mail.MaxAttempts, cfg.SMS.MaxAttempts, cfg.Mail.Enabled, cfg.SMS.Enabled,
	))
	service, err := appointment.New(store, availability, time.Now)
	if err != nil {
		return nil, fmt.Errorf("app: creating appointment service: %w", err)
	}
	return service, nil
}
