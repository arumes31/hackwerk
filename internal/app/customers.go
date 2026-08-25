package app

import (
	"fmt"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/customers"
	"github.com/jackc/pgx/v5/pgxpool"
)

func CustomerService(pool *pgxpool.Pool) (*customers.Service, error) {
	service, err := customers.NewService(postgres.NewCustomerStore(pool))
	if err != nil {
		return nil, fmt.Errorf("app: creating customer service: %w", err)
	}
	return service, nil
}
