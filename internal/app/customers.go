package app

import (
	"fmt"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/customers"
	"github.com/jackc/pgx/v5/pgxpool"
)

func CustomerService(pool *pgxpool.Pool, durationReviewThresholds ...int32) (*customers.Service, error) {
	options := make([]customers.ServiceOption, 0, 1)
	if len(durationReviewThresholds) == 2 {
		options = append(options, customers.WithDurationReviewThresholds(durationReviewThresholds[0], durationReviewThresholds[1]))
	} else if len(durationReviewThresholds) != 0 {
		return nil, fmt.Errorf("app: duration review thresholds require minimum and maximum")
	}
	service, err := customers.NewService(postgres.NewCustomerStore(pool), options...)
	if err != nil {
		return nil, fmt.Errorf("app: creating customer service: %w", err)
	}
	return service, nil
}
