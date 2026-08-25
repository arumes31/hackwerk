package app

import (
	"fmt"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/driver"
	"github.com/jackc/pgx/v5/pgxpool"
)

func AppointmentService(pool *pgxpool.Pool, availability *driver.Service) (*appointment.Service, error) {
	service, err := appointment.New(postgres.NewAppointmentStore(pool), availability, time.Now)
	if err != nil {
		return nil, fmt.Errorf("app: creating appointment service: %w", err)
	}
	return service, nil
}
