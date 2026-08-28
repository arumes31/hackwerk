package app

import (
	"testing"
	"time"

	"example.invalid/hackplan/internal/config"
)

func TestBuildPlanningRouterAcceptsExactInternalOSRM(t *testing.T) {
	cfg := config.Config{Planning: config.Planning{
		Router: "osrm-internal", RoutingURL: "http://osrm:5000",
		RoutingTimeout: time.Second, RoutingBackoff: time.Second,
		RoutingMaxResponseBytes: 1 << 20, RoutingCacheTTL: time.Minute,
		RoutingCacheEntries: 8, HaversineRoadFactor: 1.3, HaversineSpeedKMH: 55,
	}}
	if _, _, err := buildPlanningRouter(cfg); err != nil {
		t.Fatalf("buildPlanningRouter() error = %v", err)
	}
}
