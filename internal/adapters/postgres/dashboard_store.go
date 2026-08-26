package postgres

import (
	"context"
	"strconv"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/dashboard"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DashboardStore struct{ queries *dbgen.Queries }

func NewDashboardStore(pool *pgxpool.Pool) *DashboardStore {
	return &DashboardStore{queries: dbgen.New(pool)}
}

func (store *DashboardStore) Load(ctx context.Context, window dashboard.Window) (dashboard.Snapshot, error) {
	counts, err := store.queries.GetDashboardCounts(ctx, dbgen.GetDashboardCountsParams{
		DayStart: timestamp(window.DayStart), DayEnd: timestamp(window.DayEnd), HorizonEnd: timestamp(window.HorizonEnd), PendingBefore: timestamp(window.PendingBefore),
	})
	if err != nil {
		return dashboard.Snapshot{}, err
	}
	appointmentRows, err := store.queries.ListDashboardAppointments(ctx, dbgen.ListDashboardAppointmentsParams{
		RangeStart: timestamp(window.DayStart), RangeEnd: timestamp(window.HorizonEnd), ResultLimit: 500,
	})
	if err != nil {
		return dashboard.Snapshot{}, err
	}
	driverRows, err := store.queries.ListDashboardDriverAvailability(ctx, dbgen.ListDashboardDriverAvailabilityParams{
		IsoWeekday: window.ISOWeekday, LocalDate: pgtype.Date{Time: window.LocalDate, Valid: true}, DayStart: timestamp(window.DayStart), DayEnd: timestamp(window.DayEnd),
	})
	if err != nil {
		return dashboard.Snapshot{}, err
	}
	bookingRows, err := store.queries.ListDashboardChipperBookings(ctx, dbgen.ListDashboardChipperBookingsParams{
		BusinessStart: timestamp(window.BusinessStart), BusinessEnd: timestamp(window.BusinessEnd),
	})
	if err != nil {
		return dashboard.Snapshot{}, err
	}
	urgentRows, err := store.queries.ListDashboardUrgentJobs(ctx, dbgen.ListDashboardUrgentJobsParams{
		OldBefore: timestamp(window.OldBefore), PreferredBefore: pgtype.Date{Time: window.PreferredBefore, Valid: true}, ResultLimit: 20,
	})
	if err != nil {
		return dashboard.Snapshot{}, err
	}

	result := dashboard.Snapshot{Counts: dashboard.Counts{
		Waitlist: counts.WaitlistCount, Appointments: counts.AppointmentCount, Attention: counts.AttentionCount,
		NotificationIssues: counts.NotificationIssueCount, Overrides: counts.OverrideCount, ActiveDrivers: counts.ActiveDriverCount,
	}}
	result.Appointments = make([]dashboard.Appointment, 0, len(appointmentRows))
	for _, row := range appointmentRows {
		latitude, longitude := optionalCoordinate(row.Latitude), optionalCoordinate(row.Longitude)
		result.Appointments = append(result.Appointments, dashboard.Appointment{
			ID: row.AID, JobID: row.AJobID, CustomerID: row.CustomerID, JobNumber: row.JobNumber, Lifecycle: row.LifecycleStatus, Confirmation: row.ConfirmationStatus,
			StartsAt: row.StartsAt.Time.UTC(), EndsAt: row.EndsAt.Time.UTC(), Version: row.Version, JobType: row.JobType, VolumeM3: row.JVolumeM3,
			CustomerName: row.CustomerName, Locality: row.Locality, Drivers: row.DriverNames, Resources: row.ResourceNames, Chippers: row.ChipperNames,
			LatestNote: row.LatestNote, OverrideReason: row.AvailabilityOverrideReason,
			MapsURL: customers.MapsURL(customers.CustomerInput{Street: row.Street, PostalCode: row.PostalCode, Locality: row.Locality, CountryCode: row.CountryCode, Latitude: latitude, Longitude: longitude}),
		})
	}
	result.Drivers = make([]dashboard.DriverAvailability, 0, len(driverRows))
	for _, row := range driverRows {
		state := "nicht gepflegt"
		switch {
		case row.HasAvailableOverride:
			state = "verfügbar"
		case row.HasUnavailableException:
			state = "nicht verfügbar"
		case row.HasLimitedRule:
			state = "eingeschränkt"
		case row.HasRule:
			state = "verfügbar"
		}
		result.Drivers = append(result.Drivers, dashboard.DriverAvailability{ID: row.DID, UserID: row.UserID, Name: row.DisplayName, State: state})
	}
	result.Bookings = make([]dashboard.Booking, 0, len(bookingRows))
	for _, row := range bookingRows {
		result.Bookings = append(result.Bookings, dashboard.Booking{
			ResourceID: row.ResourceID, ResourceName: row.ResourceName, StartsAt: row.ReservedStartsAt.Time.UTC(), EndsAt: row.ReservedEndsAt.Time.UTC(),
			Valid: row.ReservedStartsAt.Valid && row.ReservedEndsAt.Valid,
		})
	}
	result.UrgentJobs = make([]dashboard.UrgentJob, 0, len(urgentRows))
	for _, row := range urgentRows {
		item := dashboard.UrgentJob{
			ID: row.JID, CustomerID: row.CustomerID, Number: row.JobNumber, Urgency: row.Urgency, VolumeM3: row.JVolumeM3, ReceivedAt: row.ReceivedAt.Time.UTC(),
			CustomerName: row.CustomerName, Locality: row.Locality,
		}
		if row.PreferredEndDate.Valid {
			item.PreferredEnd = row.PreferredEndDate.Time
		}
		result.UrgentJobs = append(result.UrgentJobs, item)
	}
	return result, nil
}

func optionalCoordinate(value string) *float64 {
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil
	}
	return &parsed
}
