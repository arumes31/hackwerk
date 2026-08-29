// Package dashboard provides the bounded, read-only operational overview.
package dashboard

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
)

var ErrInvalidDate = errors.New("dashboard: invalid date")

type Config struct {
	Location                    *time.Location
	HorizonDays                 int
	PendingAfter                time.Duration
	BusinessOpen, BusinessClose string
}

type Window struct {
	OwnerUserID                               string
	LocalDate                                 time.Time
	DayStart, DayEnd, HorizonEnd              time.Time
	BusinessStart, BusinessEnd, PendingBefore time.Time
	OldBefore                                 time.Time
	PreferredBefore                           time.Time
	ISOWeekday                                int16
}

type Counts struct {
	Waitlist, Appointments, Attention, NotificationIssues, Overrides, ActiveDrivers, VoiceDrafts int64
}

type Appointment struct {
	ID, JobID, CustomerID, JobNumber, Lifecycle, Confirmation string
	JobType, VolumeM3, CustomerName, Locality                 string
	Drivers, Resources, Chippers, LatestNote                  string
	MapsURL, OverrideReason                                   string
	StartsAt, EndsAt                                          time.Time
	Version                                                   int32
}

type DriverAvailability struct {
	ID, UserID, Name, State string
	Own                     bool
}

type Booking struct {
	ResourceID, ResourceName string
	StartsAt, EndsAt         time.Time
	Valid                    bool
}

type UrgentJob struct {
	ID, CustomerID, Number, Urgency, VolumeM3, CustomerName, Locality string
	ReceivedAt                                                        time.Time
	PreferredEnd                                                      time.Time
}

type Snapshot struct {
	Counts       Counts
	Appointments []Appointment
	Drivers      []DriverAvailability
	Bookings     []Booking
	UrgentJobs   []UrgentJob
}

type Store interface {
	Load(context.Context, Window) (Snapshot, error)
}

type Slot struct{ StartsAt, EndsAt time.Time }

type Capacity struct {
	ResourceID, ResourceName string
	Free                     []Slot
}

type AppointmentGroup struct {
	ResourceName string
	Appointments []Appointment
}

type View struct {
	Date, PreviousDate, NextDate string
	DateLabel, UpdatedLabel      string
	Admin                        bool
	Counts                       Counts
	Today, Upcoming              []Appointment
	Groups                       []AppointmentGroup
	Drivers                      []DriverAvailability
	OwnAvailability              *DriverAvailability
	Capacities                   []Capacity
	UrgentJobs                   []UrgentJob
	GeneratedAt                  time.Time
}

type Service struct {
	store                  Store
	location               *time.Location
	horizonDays            int
	pendingAfter           time.Duration
	openHour, openMinute   int
	closeHour, closeMinute int
	now                    func() time.Time
}

func New(store Store, cfg Config, now func() time.Time) (*Service, error) {
	if store == nil || cfg.Location == nil || cfg.HorizonDays < 1 || cfg.HorizonDays > 31 || cfg.PendingAfter < time.Minute {
		return nil, errors.New("dashboard: invalid dependencies")
	}
	open, openErr := time.Parse("15:04", cfg.BusinessOpen)
	closeAt, closeErr := time.Parse("15:04", cfg.BusinessClose)
	if openErr != nil || closeErr != nil || !closeAt.After(open) {
		return nil, errors.New("dashboard: invalid business hours")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{
		store: store, location: cfg.Location, horizonDays: cfg.HorizonDays, pendingAfter: cfg.PendingAfter,
		openHour: open.Hour(), openMinute: open.Minute(), closeHour: closeAt.Hour(), closeMinute: closeAt.Minute(), now: now,
	}, nil
}

func (service *Service) View(ctx context.Context, actor auth.Actor, requestedDate string) (View, error) {
	if err := actor.Require(auth.PermissionDashboardView); err != nil {
		return View{}, err
	}
	now := service.now().In(service.location)
	localDate, err := service.localDate(requestedDate, now)
	if err != nil {
		return View{}, err
	}
	dayStart := time.Date(localDate.Year(), localDate.Month(), localDate.Day(), 0, 0, 0, 0, service.location)
	dayEnd := dayStart.AddDate(0, 0, 1)
	businessStart := time.Date(localDate.Year(), localDate.Month(), localDate.Day(), service.openHour, service.openMinute, 0, 0, service.location)
	businessEnd := time.Date(localDate.Year(), localDate.Month(), localDate.Day(), service.closeHour, service.closeMinute, 0, 0, service.location)
	window := Window{
		OwnerUserID: actor.UserID,
		LocalDate:   localDate, DayStart: dayStart.UTC(), DayEnd: dayEnd.UTC(), HorizonEnd: dayStart.AddDate(0, 0, service.horizonDays).UTC(),
		BusinessStart: businessStart.UTC(), BusinessEnd: businessEnd.UTC(), PendingBefore: now.Add(-service.pendingAfter).UTC(),
		OldBefore: now.AddDate(0, 0, -30).UTC(), PreferredBefore: localDate.AddDate(0, 0, service.horizonDays), ISOWeekday: isoWeekday(localDate.Weekday()),
	}
	if window.ISOWeekday == 0 {
		window.ISOWeekday = 7
	}
	snapshot, err := service.store.Load(ctx, window)
	if err != nil {
		return View{}, err
	}
	view := View{
		Date: localDate.Format(time.DateOnly), PreviousDate: localDate.AddDate(0, 0, -1).Format(time.DateOnly), NextDate: localDate.AddDate(0, 0, 1).Format(time.DateOnly),
		DateLabel: germanDateLabel(localDate), UpdatedLabel: now.Format("15:04"), Admin: actor.Role == auth.RoleAdmin,
		Counts: snapshot.Counts, Drivers: snapshot.Drivers, UrgentJobs: snapshot.UrgentJobs, GeneratedAt: now.UTC(),
	}
	for index := range view.Drivers {
		view.Drivers[index].Own = view.Drivers[index].ID == actor.DriverID
		if view.Drivers[index].Own {
			ownAvailability := view.Drivers[index]
			view.OwnAvailability = &ownAvailability
		}
	}
	for _, item := range snapshot.Appointments {
		if item.StartsAt.Before(window.DayEnd) && item.EndsAt.After(window.DayStart) {
			view.Today = append(view.Today, item)
		} else {
			view.Upcoming = append(view.Upcoming, item)
		}
	}
	view.Groups = groupAppointments(view.Today)
	view.Capacities = freeCapacity(snapshot.Bookings, window.BusinessStart, window.BusinessEnd)
	if !view.Admin {
		view.Counts.NotificationIssues = 0
		view.Counts.Overrides = 0
		view.UrgentJobs = nil
		view.Capacities = nil
		for index := range view.Today {
			view.Today[index].OverrideReason = ""
		}
	}
	return view, nil
}

func isoWeekday(day time.Weekday) int16 {
	switch day {
	case time.Monday:
		return 1
	case time.Tuesday:
		return 2
	case time.Wednesday:
		return 3
	case time.Thursday:
		return 4
	case time.Friday:
		return 5
	case time.Saturday:
		return 6
	case time.Sunday:
		return 7
	default:
		return 0
	}
}

func (service *Service) localDate(value string, now time.Time) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, service.location), nil
	}
	parsed, err := time.ParseInLocation(time.DateOnly, value, service.location)
	if err != nil || parsed.Before(now.AddDate(-1, 0, 0)) || parsed.After(now.AddDate(1, 0, 0)) {
		return time.Time{}, ErrInvalidDate
	}
	return parsed, nil
}

func groupAppointments(values []Appointment) []AppointmentGroup {
	groups := make(map[string][]Appointment)
	for _, value := range values {
		name := value.Chippers
		if name == "" {
			name = "Ohne Hackmaschine"
		}
		groups[name] = append(groups[name], value)
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]AppointmentGroup, 0, len(names))
	for _, name := range names {
		result = append(result, AppointmentGroup{ResourceName: name, Appointments: groups[name]})
	}
	return result
}

func freeCapacity(bookings []Booking, start, end time.Time) []Capacity {
	byResource := make(map[string][]Slot)
	names := make(map[string]string)
	for _, booking := range bookings {
		names[booking.ResourceID] = booking.ResourceName
		if booking.Valid {
			byResource[booking.ResourceID] = append(byResource[booking.ResourceID], Slot{StartsAt: maxTime(booking.StartsAt, start), EndsAt: minTime(booking.EndsAt, end)})
		}
	}
	ids := make([]string, 0, len(names))
	for id := range names {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return names[ids[left]] < names[ids[right]] })
	result := make([]Capacity, 0, len(ids))
	for _, id := range ids {
		reserved := byResource[id]
		sort.Slice(reserved, func(left, right int) bool { return reserved[left].StartsAt.Before(reserved[right].StartsAt) })
		cursor := start
		free := make([]Slot, 0, len(reserved)+1)
		for _, slot := range reserved {
			if slot.EndsAt.Before(cursor) || !slot.EndsAt.After(slot.StartsAt) {
				continue
			}
			if slot.StartsAt.After(cursor) {
				free = append(free, Slot{StartsAt: cursor, EndsAt: slot.StartsAt})
			}
			if slot.EndsAt.After(cursor) {
				cursor = slot.EndsAt
			}
		}
		if cursor.Before(end) {
			free = append(free, Slot{StartsAt: cursor, EndsAt: end})
		}
		result = append(result, Capacity{ResourceID: id, ResourceName: names[id], Free: free})
	}
	return result
}

func maxTime(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}
func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func germanDateLabel(value time.Time) string {
	weekdays := [...]string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}
	return weekdays[value.Weekday()] + ", " + value.Format("02.01.2006")
}
