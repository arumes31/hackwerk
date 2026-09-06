//go:build integration

package integration_test

import (
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/dashboard"
)

func TestDashboardStoreRemainsBoundedWithOperationalData(t *testing.T) {
	fixture := newCalendarFixture(t)
	if _, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO customers (first_name, last_name, street, postal_code, locality, email)
		SELECT 'Dashboard', 'Kunde ' || value, 'Waldweg ' || value, '4710', 'Grieskirchen',
		       'dashboard-' || value || '@example.test'
		FROM generate_series(1, 5000) AS value;

		INSERT INTO jobs (job_number, customer_id, job_type, volume_m3, estimated_hack_minutes,
		                  urgency, received_at, preferred_end_date)
		SELECT 'HW-DASH-' || lpad(row_number() OVER (ORDER BY c.email, series.value)::text, 5, '0'), c.id,
		       'chipping_only', 10 + (row_number() OVER (ORDER BY c.email, series.value) % 200), 120,
		       CASE WHEN row_number() OVER (ORDER BY c.email, series.value) % 17 = 0 THEN 'urgent' ELSE 'normal' END,
		       timestamptz '2026-08-25 08:00:00+00' - (row_number() OVER (ORDER BY c.email, series.value) * interval '30 minutes'),
		       CASE WHEN row_number() OVER (ORDER BY c.email, series.value) % 19 = 0 THEN date '2026-08-28' END
		FROM customers c CROSS JOIN generate_series(1, 2) AS series(value)
		WHERE c.email::text LIKE 'dashboard-%';

		INSERT INTO waitlist_entries (job_id)
		SELECT id FROM jobs WHERE job_number LIKE 'HW-DASH-%' ORDER BY job_number LIMIT 500;

		WITH archived_customer AS (
			INSERT INTO customers (first_name,last_name,locality,email,archived_at)
			VALUES ('Archiviert','Dashboard','Linz','dashboard-archived@example.test',now()) RETURNING id
		), archived_job AS (
			INSERT INTO jobs (job_number,customer_id,job_type,volume_m3,estimated_hack_minutes,urgency)
			SELECT 'HW-DASH-ARCHIVED',id,'chipping_only',25,120,'normal' FROM archived_customer RETURNING id
		)
		INSERT INTO waitlist_entries (job_id) SELECT id FROM archived_job;

		INSERT INTO appointments (job_id, lifecycle_status, starts_at, ends_at)
		SELECT j.id, 'proposal',
		       timestamptz '2026-08-25 05:00:00+00' + ((row_number() OVER (ORDER BY j.job_number) % 120) - 30) * interval '1 day',
		       timestamptz '2026-08-25 07:00:00+00' + ((row_number() OVER (ORDER BY j.job_number) % 120) - 30) * interval '1 day'
		FROM jobs j WHERE j.job_number LIKE 'HW-DASH-%' ORDER BY j.job_number LIMIT 2000;

		UPDATE customers SET latitude=48.21, longitude=14.21
		WHERE email::text LIKE 'dashboard-%';
		INSERT INTO drivers (display_name, availability_policy)
		SELECT 'Lastfahrer ' || value, 'legacy_rules' FROM generate_series(3,6) AS value;
		INSERT INTO availability_rules (driver_id,iso_weekday,local_start,local_end,valid_from,status)
		SELECT d.id, weekday, '07:00', '17:00', '2026-01-01', 'available'
		FROM drivers d CROSS JOIN generate_series(1,5) AS weekday
		WHERE NOT EXISTS (SELECT 1 FROM availability_rules ar WHERE ar.driver_id=d.id AND ar.iso_weekday=weekday);
		INSERT INTO resources (resource_type,name,exclusive)
		SELECT 'chipper', 'Last-Hackmaschine ' || value, true FROM generate_series(3,5) AS value;
		ANALYZE customers; ANALYZE jobs; ANALYZE waitlist_entries; ANALYZE appointments;
	`); err != nil {
		t.Fatal(err)
	}

	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	localDate := time.Date(2026, 8, 25, 0, 0, 0, 0, location)
	window := dashboard.Window{
		LocalDate: localDate, DayStart: localDate.UTC(), DayEnd: localDate.AddDate(0, 0, 1).UTC(),
		HorizonEnd: localDate.AddDate(0, 0, 14).UTC(), BusinessStart: time.Date(2026, 8, 25, 7, 0, 0, 0, location).UTC(),
		BusinessEnd: time.Date(2026, 8, 25, 17, 0, 0, 0, location).UTC(), PendingBefore: time.Now().Add(-15 * time.Minute),
		CapacityEnd: time.Date(2026, 9, 1, 17, 0, 0, 0, location).UTC(),
		OldBefore:   time.Date(2026, 7, 26, 0, 0, 0, 0, location).UTC(), PreferredBefore: localDate.AddDate(0, 0, 14), ISOWeekday: 2,
	}
	started := time.Now()
	snapshot, err := postgres.NewDashboardStore(fixture.pool).Load(fixture.ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("bounded dashboard load took %s", elapsed)
	}
	if snapshot.Counts.Waitlist != 500 || len(snapshot.Appointments) == 0 || len(snapshot.Appointments) > 500 {
		t.Fatalf("bounded snapshot counts = waitlist %d, appointments %d", snapshot.Counts.Waitlist, len(snapshot.Appointments))
	}
	var expectedUnplanned int64
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM waitlist_entries w
		JOIN jobs j ON j.id=w.job_id JOIN customers c ON c.id=j.customer_id
		WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND c.archived_at IS NULL
		AND NOT EXISTS (SELECT 1 FROM appointments a WHERE a.job_id=j.id AND a.lifecycle_status IN ('proposal','fixed'))`).Scan(&expectedUnplanned); err != nil {
		t.Fatal(err)
	}
	if snapshot.Counts.Unplanned != expectedUnplanned {
		t.Fatalf("unplanned dashboard count = %d, want %d active visible jobs", snapshot.Counts.Unplanned, expectedUnplanned)
	}
	if len(snapshot.UrgentJobs) != 20 {
		t.Fatalf("urgent result limit = %d", len(snapshot.UrgentJobs))
	}
	if len(snapshot.Drivers) != 6 || len(snapshot.Bookings) != 5 {
		t.Fatalf("drivers/bookings = %d/%d", len(snapshot.Drivers), len(snapshot.Bookings))
	}
	customerService, err := customers.NewService(postgres.NewCustomerStore(fixture.pool))
	if err != nil {
		t.Fatal(err)
	}
	listStarted := time.Now()
	customerPage, err := customerService.ListCustomers(fixture.ctx, fixture.admin, customers.CustomerListFilter{
		Search: "Dashboard", Page: 1, PageSize: 25,
	})
	if err != nil || len(customerPage.Items) != 25 || customerPage.Total != 5000 {
		t.Fatalf("customer page items/total/error=%d/%d/%v", len(customerPage.Items), customerPage.Total, err)
	}
	waitlistPage, err := customerService.ListWaitlist(fixture.ctx, fixture.admin, customers.WaitlistFilter{})
	if err != nil || len(waitlistPage.Items) == 0 || len(waitlistPage.Items) > 50 || waitlistPage.Total != 500 {
		t.Fatalf("waitlist page items/total/error=%d/%d/%v", len(waitlistPage.Items), waitlistPage.Total, err)
	}
	incompletePage, err := customerService.ListWaitlist(fixture.ctx, fixture.admin, customers.WaitlistFilter{Incomplete: true})
	if err != nil || len(incompletePage.Items) == 0 || incompletePage.Total != 500 || incompletePage.UnfilteredTotal != 500 {
		t.Fatalf("incomplete waitlist items/total/unfiltered/error=%d/%d/%d/%v", len(incompletePage.Items), incompletePage.Total, incompletePage.UnfilteredTotal, err)
	}
	searchResults, err := customerService.SearchWorkspace(fixture.ctx, fixture.admin, "HW-DASH-000")
	if err != nil || len(searchResults) == 0 || len(searchResults) > 24 {
		t.Fatalf("workspace search results/error=%d/%v", len(searchResults), err)
	}
	if elapsed := time.Since(listStarted); elapsed > 2*time.Second {
		t.Fatalf("bounded customer/waitlist pages took %s", elapsed)
	}
	var targetJob string
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT job_id::text FROM waitlist_entries ORDER BY entered_at,id LIMIT 1`).Scan(&targetJob); err != nil {
		t.Fatal(err)
	}
	planningStarted := time.Now()
	if run, err := planningFixtureService(t, fixture, time.Date(2026, 8, 31, 4, 0, 0, 0, time.UTC)).Suggest(fixture.ctx, fixture.admin, targetJob); err != nil || len(run.Suggestions) == 0 {
		t.Fatalf("planning smoke suggestions/error=%d/%v", len(run.Suggestions), err)
	}
	if elapsed := time.Since(planningStarted); elapsed > 4*time.Second {
		t.Fatalf("planning smoke took %s", elapsed)
	}

	tx, err := fixture.pool.Begin(fixture.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(fixture.ctx) }()
	if _, err := tx.Exec(fixture.ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatal(err)
	}
	var plan string
	if err := tx.QueryRow(fixture.ctx, `
		EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
		SELECT id FROM appointments
		WHERE starts_at < $1 AND ends_at > $2 AND lifecycle_status IN ('proposal','fixed')
		ORDER BY starts_at, id LIMIT 500`, window.HorizonEnd, window.DayStart).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "appointments_calendar_range_idx") {
		t.Fatalf("calendar range query did not use its index: %s", plan)
	}
	for name, query := range map[string]string{
		"incomplete waitlist": `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
			SELECT w.id FROM waitlist_entries w JOIN jobs j ON j.id=w.job_id JOIN customers c ON c.id=j.customer_id
			WHERE w.removed_at IS NULL AND j.archived_at IS NULL AND c.archived_at IS NULL
			  AND (j.pile_latitude IS NULL OR j.pile_longitude IS NULL OR COALESCE(j.pile_location_source, '')='')
			ORDER BY w.entered_at,w.id LIMIT 25`,
		"workspace search": `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)
			SELECT j.id FROM jobs j JOIN customers c ON c.id=j.customer_id
			WHERE j.archived_at IS NULL AND c.archived_at IS NULL
			  AND concat_ws(' ',j.job_number,c.first_name,c.last_name,c.company_name,c.locality) ILIKE '%HW-DASH-000%'
			ORDER BY j.updated_at DESC,j.id LIMIT 8`,
	} {
		var explain string
		if err := tx.QueryRow(fixture.ctx, query).Scan(&explain); err != nil {
			t.Fatalf("%s EXPLAIN: %v", name, err)
		}
		if !strings.Contains(explain, `"Plan"`) || !strings.Contains(explain, `"Actual Total Time"`) {
			t.Fatalf("%s EXPLAIN lacks analyzed bounded plan: %s", name, explain)
		}
	}
}
