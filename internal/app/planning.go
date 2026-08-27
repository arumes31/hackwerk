package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/planning"
	"example.invalid/hackplan/internal/routelocation"
	"github.com/jackc/pgx/v5/pgxpool"
)

type planningAvailability struct{ service *driver.Service }

type planningDefaultStart struct{ store *postgres.RouteLocationStore }

func (p planningDefaultStart) DefaultStart(ctx context.Context) (planning.Point, error) {
	location, err := p.store.DefaultStart(ctx)
	if errors.Is(err, routelocation.ErrNotFound) {
		return planning.Point{}, planning.ErrConfiguration
	}
	if err != nil {
		return planning.Point{}, err
	}
	return planning.Point{Latitude: location.Latitude, Longitude: location.Longitude}, nil
}

func (a planningAvailability) Resolve(ctx context.Context, actor auth.Actor, driverID string, from, to time.Time) ([]planning.Interval, error) {
	values, err := a.service.ResolveAvailability(ctx, actor, driverID, from, to)
	if err != nil {
		return nil, err
	}
	result := make([]planning.Interval, 0, len(values))
	for _, value := range values {
		result = append(result, planning.Interval{StartsAt: value.StartsAt, EndsAt: value.EndsAt, Status: string(value.Status)})
	}
	return result, nil
}

func PlanningService(cfg config.Config, pool *pgxpool.Pool, drivers *driver.Service, starts *postgres.RouteLocationStore, observers ...planning.Observer) (*planning.Service, error) {
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	open, err := driver.ParseLocalTime(cfg.Planning.BusinessOpen)
	if err != nil {
		return nil, err
	}
	closeMinute, err := driver.ParseLocalTime(cfg.Planning.BusinessClose)
	if err != nil {
		return nil, err
	}
	router, _, err := buildPlanningRouter(cfg)
	if err != nil {
		return nil, err
	}
	options := make([]planning.Option, 0, len(observers)+1)
	options = append(options, planning.WithDefaultStartProvider(planningDefaultStart{store: starts}))
	for _, observer := range observers {
		options = append(options, planning.WithObserver(observer))
	}
	store := postgres.NewPlanningStore(pool)
	service, err := planning.New(store, planningAvailability{service: drivers}, router, planning.Config{Location: location, RouterName: cfg.Planning.Router, BusinessOpen: open, BusinessClose: closeMinute, SlotMinutes: cfg.Planning.SlotMinutes, HorizonDays: cfg.Planning.HorizonDays, BufferMinutes: cfg.Planning.BufferMinutes, CandidateLimit: cfg.Planning.CandidateLimit, SuggestionTTL: cfg.Planning.SuggestionTTL, Weights: planning.Weights{Preference: cfg.Planning.WeightPreference, Travel: cfg.Planning.WeightTravel, Driver: cfg.Planning.WeightDriver, Resource: cfg.Planning.WeightResource, Utilization: cfg.Planning.WeightUtilization, Urgency: cfg.Planning.WeightUrgency, Region: cfg.Planning.WeightRegion}}, time.Now, options...)
	if err != nil {
		return nil, fmt.Errorf("app: creating planning service: %w", err)
	}
	return service, nil
}

func RoutePlanningService(cfg config.Config, pool *pgxpool.Pool) (*planning.RouteService, error) {
	matrix, directions, err := buildPlanningRouter(cfg)
	if err != nil {
		return nil, err
	}
	service, err := planning.NewRouteService(postgres.NewRouteStore(pool), matrix, directions, planning.DefaultRouteConfig())
	if err != nil {
		return nil, fmt.Errorf("app: creating route service: %w", err)
	}
	return service, nil
}

func buildPlanningRouter(cfg config.Config) (planning.Router, planning.DirectionsRouter, error) {
	haversine := planning.NewHaversineRouter(cfg.Planning.HaversineRoadFactor, cfg.Planning.HaversineSpeedKMH)
	var router planning.Router = haversine
	if cfg.Planning.Router == "osrm" {
		primary, err := planning.NewOSRMRouter(planning.OSRMConfig{
			BaseURL: cfg.Planning.RoutingURL, Timeout: cfg.Planning.RoutingTimeout,
			Backoff: cfg.Planning.RoutingBackoff, MaxResponseBytes: cfg.Planning.RoutingMaxResponseBytes,
		})
		if err != nil {
			return nil, nil, err
		}
		router = planning.FallbackRouter{Primary: primary, Fallback: haversine}
	}
	router = planning.NewCachedRouter(router, cfg.Planning.RoutingCacheTTL, cfg.Planning.RoutingCacheEntries)
	directions, ok := router.(planning.DirectionsRouter)
	if !ok {
		return nil, nil, planning.ErrValidation
	}
	return router, directions, nil
}
