package app

import (
	"fmt"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
	"github.com/jackc/pgx/v5/pgxpool"
)

func DriverService(pool *pgxpool.Pool) (*driver.Service, error) {
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		return nil, fmt.Errorf("app: loading Vienna location: %w", err)
	}
	service, err := driver.New(postgres.NewDriverStore(pool), location)
	if err != nil {
		return nil, fmt.Errorf("app: creating driver service: %w", err)
	}
	return service, nil
}

func ResourceService(pool *pgxpool.Pool) (*resource.Service, error) {
	service, err := resource.New(postgres.NewResourceStore(pool))
	if err != nil {
		return nil, fmt.Errorf("app: creating resource service: %w", err)
	}
	return service, nil
}
