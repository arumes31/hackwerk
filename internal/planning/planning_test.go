package planning

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig(location)
	cfg.Depot = Point{48.2, 14.2}
	cfg.HorizonDays = 7
	cfg.CandidateLimit = 1000
	return cfg
}
func testSnapshot(now time.Time) Snapshot {
	return Snapshot{Job: Job{ID: "job", Type: "chipping_only", TransportMode: "none", Urgency: "urgent", Version: 1, WaitlistVersion: 1, HackMinutes: 180, EnteredAt: now.AddDate(0, -2, 0), PreferredStart: now.In(time.FixedZone("x", 3600)).Format(time.DateOnly), PreferredEnd: now.AddDate(0, 0, 2).Format(time.DateOnly), Location: Point{48.21, 14.21}}, Drivers: []Driver{{ID: "b-driver", Name: "Berger", Availability: []Interval{{StartsAt: now.Add(-time.Hour), EndsAt: now.AddDate(0, 0, 8), Status: "available"}}}, {ID: "a-driver", Name: "Anna", Availability: []Interval{{StartsAt: now.Add(-time.Hour), EndsAt: now.AddDate(0, 0, 8), Status: "available"}}}}, Resources: []Resource{{ID: "chipper-1", Name: "Hackmaschine 1", Type: "chipper", Exclusive: true}}}
}

func TestGenerateIsDeterministicConflictFreeAndBounded(t *testing.T) {
	cfg := testConfig(t)
	now := time.Date(2026, 9, 1, 5, 1, 0, 0, time.UTC)
	snapshot := testSnapshot(now)
	snapshot.Reservations = []Reservation{{ID: "busy", StartsAt: time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC), DriverIDs: []string{"a-driver", "b-driver"}, ResourceIDs: []string{"chipper-1"}, Location: Point{48.22, 14.22}}}
	router := NewHaversineRouter(1.3, 55)
	first, err := Generate(context.Background(), snapshot, router, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(context.Background(), snapshot, router, cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("identical input did not produce identical suggestions")
	}
	if len(first) == 0 || len(first) > 3 {
		t.Fatalf("unexpected suggestion count %d", len(first))
	}
	for _, value := range first {
		if value.Score < 0 || value.Score > 100 {
			t.Fatalf("score out of range: %f", value.Score)
		}
		if value.StartsAt.Before(snapshot.Reservations[0].EndsAt) && value.EndsAt.After(snapshot.Reservations[0].StartsAt) {
			t.Fatal("suggestion overlaps reservation")
		}
		if got := value.EndsAt.Sub(value.StartsAt); got != 195*time.Minute {
			t.Fatalf("duration=%s", got)
		}
	}
	if first[0].DriverID != "a-driver" {
		t.Fatalf("tie breaker driver=%s", first[0].DriverID)
	}
}

func TestGenerateTransportAndCoordinatesPolicies(t *testing.T) {
	cfg := testConfig(t)
	now := time.Date(2026, 9, 1, 5, 1, 0, 0, time.UTC)
	snapshot := testSnapshot(now)
	snapshot.Job.Type = "chipping_with_transport"
	snapshot.Job.TransportMode = "internal"
	if _, err := Generate(context.Background(), snapshot, NewHaversineRouter(1.3, 55), cfg, now); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("expected transport gate, got %v", err)
	}
	snapshot.Resources = append(snapshot.Resources, Resource{ID: "vehicle", Name: "LKW", Type: "transport_vehicle", Exclusive: true})
	snapshot.Job.Location = Point{}
	values, err := Generate(context.Background(), snapshot, NewHaversineRouter(1.3, 55), cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	if values[0].RoutingSource != "unavailable" || len(values[0].Warnings) == 0 {
		t.Fatal("missing coordinates were not transparent")
	}
}

func TestGenerateUsesAnotherFreeChipper(t *testing.T) {
	cfg := testConfig(t)
	now := time.Date(2026, 9, 1, 5, 1, 0, 0, time.UTC)
	snapshot := testSnapshot(now)
	snapshot.Resources = append(snapshot.Resources, Resource{ID: "chipper-2", Name: "Hackmaschine 2", Type: "chipper", Exclusive: true})
	snapshot.Reservations = []Reservation{{ID: "busy-first", StartsAt: time.Date(2026, 9, 1, 5, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 9, 1, 16, 0, 0, 0, time.UTC), ResourceIDs: []string{"chipper-1"}}}
	values, err := Generate(context.Background(), snapshot, NewHaversineRouter(1.3, 55), cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values[0].ResourceIDs, []string{"chipper-2"}) {
		t.Fatalf("resource IDs=%v", values[0].ResourceIDs)
	}
}

func TestPreferredWindowAndUrgencyAffectExplanation(t *testing.T) {
	cfg := testConfig(t)
	now := time.Date(2026, 9, 1, 5, 1, 0, 0, time.UTC)
	snapshot := testSnapshot(now)
	values, err := Generate(context.Background(), snapshot, NewHaversineRouter(1.3, 55), cfg, now)
	if err != nil {
		t.Fatal(err)
	}
	if values[0].Components.Preference != 1 || values[0].Components.Urgency < .8 {
		t.Fatalf("components=%+v", values[0].Components)
	}
	joined := ""
	for _, reason := range values[0].Reasons {
		joined += reason
	}
	if joined == "" {
		t.Fatal("missing reasons")
	}
}

func TestClustersDeterministic(t *testing.T) {
	values := []ClusterEntry{{JobID: "c", Region: "Linz", Location: Point{48.300, 14.300}}, {JobID: "a", Region: "Linz", Location: Point{48.301, 14.301}}, {JobID: "b", Region: "Linz", Location: Point{48.302, 14.302}}, {JobID: "far", Region: "Wien", Location: Point{48.20, 16.37}}}
	clusters := Clusters(values, 5, 3)
	if len(clusters) != 1 || clusters[0].Count != 3 || !reflect.DeepEqual(clusters[0].JobIDs, []string{"a", "b", "c"}) {
		t.Fatalf("clusters=%+v", clusters)
	}
}

func TestTravelTimeIsHardGate(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Vienna")
	starts := time.Date(2026, 9, 1, 9, 0, 0, 0, location).UTC()
	ends := starts.Add(3 * time.Hour)
	reservation := Reservation{ID: "maier", StartsAt: starts.Add(-3 * time.Hour), EndsAt: starts.Add(-5 * time.Minute), Location: Point{48.8, 15.2}}
	matrix, _ := NewHaversineRouter(1.3, 55).Matrix(context.Background(), []Point{{48.2, 14.2}, reservation.Location})
	if travelFeasible([]Reservation{reservation}, starts, ends, matrix, map[string]int{"maier": 1}, location) {
		t.Fatal("unreachable adjacent appointment passed hard gate")
	}
	reservation.EndsAt = starts.Add(-3 * time.Hour)
	if !travelFeasible([]Reservation{reservation}, starts, ends, matrix, map[string]int{"maier": 1}, location) {
		t.Fatal("reachable adjacent appointment was rejected")
	}
}

func TestGenerationAcrossDSTKeepsLocalBusinessTime(t *testing.T) {
	cfg := testConfig(t)
	for _, now := range []time.Time{time.Date(2026, 3, 27, 5, 1, 0, 0, time.UTC), time.Date(2026, 10, 23, 5, 1, 0, 0, time.UTC)} {
		snapshot := testSnapshot(now)
		snapshot.Drivers[0].Availability = []Interval{{StartsAt: now.Add(-time.Hour), EndsAt: now.AddDate(0, 0, 10), Status: "available"}}
		snapshot.Drivers = snapshot.Drivers[:1]
		values, err := Generate(context.Background(), snapshot, NewHaversineRouter(1.3, 55), cfg, now)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values {
			local := value.StartsAt.In(cfg.Location)
			if local.Hour() < cfg.BusinessOpen/60 || local.Hour() >= cfg.BusinessClose/60 {
				t.Fatalf("DST candidate outside business hours: %s", local)
			}
		}
	}
}

func FuzzGenerateNeverOverlaps(f *testing.F) {
	f.Add(int64(0))
	f.Fuzz(func(t *testing.T, offset int64) {
		offset %= 240
		cfg := testConfig(t)
		now := time.Date(2026, 9, 1, 5, 1, 0, 0, time.UTC)
		snapshot := testSnapshot(now)
		busyStart := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC).Add(time.Duration(offset) * time.Minute)
		snapshot.Reservations = []Reservation{{ID: "busy", StartsAt: busyStart, EndsAt: busyStart.Add(3 * time.Hour), DriverIDs: []string{"a-driver", "b-driver"}, ResourceIDs: []string{"chipper-1"}}}
		values, err := Generate(context.Background(), snapshot, NewHaversineRouter(1.3, 55), cfg, now)
		if err != nil && !errors.Is(err, ErrNoCapacity) {
			t.Fatal(err)
		}
		for _, value := range values {
			if value.StartsAt.Before(snapshot.Reservations[0].EndsAt) && value.EndsAt.After(snapshot.Reservations[0].StartsAt) {
				t.Fatal("overlap")
			}
		}
	})
}
