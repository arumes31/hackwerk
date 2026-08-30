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
	if values[0].FreeMinutes != 420 || values[0].TotalMinutes != 600 || values[0].Largest.StartsAt != start.Add(4*time.Hour) {
		t.Fatalf("capacity totals/largest = %#v", values[0])
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

func TestNewRejectsIncompleteOrUnsafeConfiguration(t *testing.T) {
	location := time.UTC
	valid := Config{Location: location, HorizonDays: 7, PendingAfter: time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00"}
	tests := []struct {
		name  string
		store Store
		cfg   Config
	}{
		{name: "missing store", cfg: valid},
		{name: "missing location", store: &fakeStore{}, cfg: Config{HorizonDays: 7, PendingAfter: time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00"}},
		{name: "horizon too short", store: &fakeStore{}, cfg: Config{Location: location, HorizonDays: 0, PendingAfter: time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00"}},
		{name: "horizon too long", store: &fakeStore{}, cfg: Config{Location: location, HorizonDays: 32, PendingAfter: time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00"}},
		{name: "pending duration too short", store: &fakeStore{}, cfg: Config{Location: location, HorizonDays: 7, PendingAfter: 0, BusinessOpen: "07:00", BusinessClose: "17:00"}},
		{name: "invalid opening time", store: &fakeStore{}, cfg: Config{Location: location, HorizonDays: 7, PendingAfter: time.Minute, BusinessOpen: "morning", BusinessClose: "17:00"}},
		{name: "closing before opening", store: &fakeStore{}, cfg: Config{Location: location, HorizonDays: 7, PendingAfter: time.Minute, BusinessOpen: "17:00", BusinessClose: "07:00"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if service, err := New(test.store, test.cfg, nil); err == nil || service != nil {
				t.Fatalf("New() = %v, %v; want configuration error", service, err)
			}
		})
	}
}

func TestViewProjectsAdminAppointmentsGroupsAndCapacity(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 9, 1, 9, 0, 0, 0, location)
	store := &fakeStore{snapshot: Snapshot{
		Counts: Counts{NotificationIssues: 2, Overrides: 1},
		Appointments: []Appointment{
			{ID: "today-b", Chippers: "Maschine B", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour)},
			{ID: "today-none", StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour)},
			{ID: "future", Chippers: "Maschine A", StartsAt: now.AddDate(0, 0, 2), EndsAt: now.AddDate(0, 0, 2).Add(time.Hour)},
		},
		Bookings: []Booking{
			{ResourceID: "a", ResourceName: "Maschine A", StartsAt: now.Add(-3 * time.Hour), EndsAt: now.Add(-2 * time.Hour), Valid: true},
			{ResourceID: "b", ResourceName: "Maschine B", StartsAt: now.Add(2 * time.Hour), EndsAt: now.Add(3 * time.Hour), Valid: true},
		},
	}}
	service, err := New(store, Config{Location: location, HorizonDays: 7, PendingAfter: 15 * time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00"}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.View(t.Context(), auth.Actor{UserID: "admin", Role: auth.RoleAdmin}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !view.Admin || len(view.Today) != 2 || len(view.Upcoming) != 1 || len(view.Groups) != 2 || view.Groups[0].ResourceName != "Maschine B" || view.Groups[1].ResourceName != "Ohne Hackmaschine" {
		t.Fatalf("admin appointments = %#v", view)
	}
	if view.Counts.NotificationIssues != 2 || len(view.Capacities) != 2 || len(view.Capacities[0].Free) != 1 {
		t.Fatalf("admin data missing = %#v", view)
	}
}

func TestViewExceptionModeShowsOnlyActionableAppointmentsAndSevenDays(t *testing.T) {
	t.Parallel()
	location := time.UTC
	now := time.Date(2026, 9, 1, 9, 0, 0, 0, location)
	store := &fakeStore{snapshot: Snapshot{
		Appointments: []Appointment{
			{ID: "normal", Drivers: "Franz", Chippers: "Maschine", Confirmation: "confirmed", StartsAt: now, EndsAt: now.Add(time.Hour)},
			{ID: "missing", Chippers: "Maschine", Confirmation: "confirmed", StartsAt: now.Add(time.Hour), EndsAt: now.Add(2 * time.Hour)},
			{ID: "declined", Drivers: "Franz", Chippers: "Maschine", Confirmation: "declined", StartsAt: now.Add(2 * time.Hour), EndsAt: now.Add(3 * time.Hour)},
		},
		Drivers:  []DriverAvailability{{ID: "driver", BookedMinutes: 601}},
		Bookings: []Booking{{ResourceID: "machine", ResourceName: "Maschine"}},
	}}
	service, _ := New(store, Config{Location: location, HorizonDays: 7, PendingAfter: 15 * time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00"}, func() time.Time { return now })
	view, err := service.View(t.Context(), auth.Actor{UserID: "admin", Role: auth.RoleAdmin}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Today) != 2 || view.Today[0].ID != "missing" || view.Today[1].ID != "declined" || len(view.MissingAssignments) != 1 {
		t.Fatalf("exception projection = %#v", view)
	}
	if len(view.DailyCapacities) != 7 || !view.Drivers[0].OvertimeRisk {
		t.Fatalf("forecast/overtime = %#v / %#v", view.DailyCapacities, view.Drivers)
	}
}

func TestViewRejectsActorWithoutDashboardPermissionAndGroupsDefaultMachine(t *testing.T) {
	location := time.UTC
	service, err := New(&fakeStore{}, Config{Location: location, HorizonDays: 7, PendingAfter: time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00"}, func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, location) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.View(t.Context(), auth.Actor{}, ""); err == nil {
		t.Fatal("View() accepted actor without dashboard permission")
	}
	groups := groupAppointments([]Appointment{{ID: "one"}, {ID: "two", Chippers: "Maschine A"}, {ID: "three"}})
	if len(groups) != 2 || groups[0].ResourceName != "Maschine A" || len(groups[1].Appointments) != 2 {
		t.Fatalf("groupAppointments() = %#v", groups)
	}
}
