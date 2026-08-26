package dashboard

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
)

type fakeStore struct {
	window   Window
	snapshot Snapshot
	err      error
}

func (store *fakeStore) Load(_ context.Context, window Window) (Snapshot, error) {
	store.window = window
	return store.snapshot, store.err
}

func TestViewUsesViennaDSTBoundaryAndProjectsDriverData(t *testing.T) {
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{snapshot: Snapshot{
		Counts:     Counts{NotificationIssues: 3, Overrides: 2},
		Drivers:    []DriverAvailability{{ID: "own", Name: "Franz Fahrer", State: "verfügbar"}},
		Bookings:   []Booking{{ResourceID: "machine-1", ResourceName: "Hackmaschine 1"}},
		UrgentJobs: []UrgentJob{{ID: "job-1"}},
	}}
	service, err := New(store, Config{Location: location, HorizonDays: 14, PendingAfter: 15 * time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00"}, func() time.Time {
		return time.Date(2026, 3, 28, 12, 0, 0, 0, location)
	})
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.View(t.Context(), auth.Actor{UserID: "user", Role: auth.RoleDriver, DriverID: "own"}, "2026-03-29")
	if err != nil {
		t.Fatal(err)
	}
	if duration := store.window.DayEnd.Sub(store.window.DayStart); duration != 23*time.Hour {
		t.Fatalf("DST day duration = %s, want 23h", duration)
	}
	if store.window.ISOWeekday != 7 || view.DateLabel != "Sonntag, 29.03.2026" {
		t.Fatalf("weekday/label = %d/%q", store.window.ISOWeekday, view.DateLabel)
	}
	if view.OwnAvailability == nil || view.OwnAvailability.Name != "Franz Fahrer" {
		t.Fatalf("own availability = %#v", view.OwnAvailability)
	}
	if view.Counts.NotificationIssues != 0 || view.Counts.Overrides != 0 || len(view.Capacities) != 0 || len(view.UrgentJobs) != 0 {
		t.Fatalf("driver projection leaked admin data: %#v", view)
	}
}

func TestFreeCapacityMergesReservationsForEveryResource(t *testing.T) {
	start := time.Date(2026, 9, 1, 7, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Hour)
	values := freeCapacity([]Booking{
		{ResourceID: "one", ResourceName: "Maschine 1", StartsAt: start.Add(time.Hour), EndsAt: start.Add(3 * time.Hour), Valid: true},
		{ResourceID: "one", ResourceName: "Maschine 1", StartsAt: start.Add(2 * time.Hour), EndsAt: start.Add(4 * time.Hour), Valid: true},
		{ResourceID: "two", ResourceName: "Maschine 2", Valid: false},
	}, start, end)
	if len(values) != 2 || len(values[0].Free) != 2 || values[0].Free[0].EndsAt != start.Add(time.Hour) || values[0].Free[1].StartsAt != start.Add(4*time.Hour) {
		t.Fatalf("free capacity = %#v", values)
	}
	if len(values[1].Free) != 1 || values[1].Free[0].StartsAt != start || values[1].Free[0].EndsAt != end {
		t.Fatalf("second resource free capacity = %#v", values[1])
	}
}

func TestViewRejectsInvalidOrUnboundedDateAndStoreFailure(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Vienna")
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, location)
	store := &fakeStore{}
	service, _ := New(store, Config{Location: location, HorizonDays: 14, PendingAfter: 15 * time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00"}, func() time.Time { return now })
	actor := auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
	for _, value := range []string{"morgen", "2028-01-01"} {
		if _, err := service.View(t.Context(), actor, value); !errors.Is(err, ErrInvalidDate) {
			t.Fatalf("date %q error = %v", value, err)
		}
	}
	store.err = errors.New("database unavailable")
	if _, err := service.View(t.Context(), actor, ""); err == nil {
		t.Fatal("store failure was ignored")
	}
}
