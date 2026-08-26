package app

import (
	"fmt"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/dashboard"
	"github.com/jackc/pgx/v5/pgxpool"
)

func DashboardService(cfg config.Config, pool *pgxpool.Pool) (*dashboard.Service, error) {
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("app: loading dashboard timezone: %w", err)
	}
	service, err := dashboard.New(postgres.NewDashboardStore(pool), dashboard.Config{
		Location: location, HorizonDays: cfg.Dashboard.HorizonDays, PendingAfter: cfg.Dashboard.PendingAfter,
		BusinessOpen: cfg.Dashboard.BusinessOpen, BusinessClose: cfg.Dashboard.BusinessClose,
	}, time.Now)
	if err != nil {
		return nil, fmt.Errorf("app: creating dashboard service: %w", err)
	}
	return service, nil
}
