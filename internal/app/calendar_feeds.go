package app

import (
	"fmt"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/calendarfeed"
	"example.invalid/hackplan/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

func CalendarFeedService(cfg config.Config, pool *pgxpool.Pool) (*calendarfeed.Service, error) {
	service, err := calendarfeed.New(postgres.NewCalendarFeedStore(pool), calendarfeed.Config{
		BaseURL: cfg.BaseURL, UIDDomain: cfg.CalendarFeed.UIDDomain, CalendarName: cfg.CalendarFeed.Name,
		ExportMaxDays: cfg.CalendarFeed.ExportMaxDays, HistoryDays: cfg.CalendarFeed.HistoryDays, FutureDays: cfg.CalendarFeed.FutureDays,
	}, time.Now, auth.NewToken)
	if err != nil {
		return nil, fmt.Errorf("app: creating calendar feed service: %w", err)
	}
	return service, nil
}
