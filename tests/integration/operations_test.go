//go:build integration

package integration_test

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOperationsPersistence(t *testing.T) {
	t.Run("resources are generic typed and optimistic", func(t *testing.T) {
		ctx, _, _, resources, admin, _, _ := operationsFixture(t)
		volume := 180.5
		id, err := resources.Create(ctx, admin, resource.Input{Type: resource.TypeChipper, Name: "Hackmaschine 2", IsExclusive: true, Capacity: resource.Capacity{VolumeM3: &volume}, InternalNote: "nur intern"}, "resource-create")
		if err != nil {
			t.Fatal(err)
		}
		items, err := resources.List(ctx, admin)
		if err != nil || len(items) != 1 || items[0].ID != id || items[0].Capacity.VolumeM3 == nil || *items[0].Capacity.VolumeM3 != volume {
			t.Fatalf("List() = %#v, error = %v", items, err)
		}
		update := resource.Input{Type: resource.TypeChipper, Name: "Hackmaschine 2A", IsExclusive: true, Capacity: resource.Capacity{VolumeM3: &volume}}
		if err := resources.Update(ctx, admin, id, 1, update, "resource-update"); err != nil {
			t.Fatal(err)
		}
		if err := resources.Update(ctx, admin, id, 1, update, "resource-stale"); !errors.Is(err, resource.ErrConflict) {
			t.Fatalf("stale Update() error = %v, want conflict", err)
		}
	})

	t.Run("user link is unique and driver cannot mutate foreign availability", func(t *testing.T) {
		ctx, _, drivers, _, admin, owner, foreignID := operationsFixture(t)
		_, err := drivers.CreateProfile(ctx, admin, driver.ProfileInput{UserID: owner.UserID, DisplayName: "Doppeltes Profil", CanCompleteJobs: true}, "duplicate-driver")
		if !errors.Is(err, driver.ErrConflict) {
			t.Fatalf("duplicate user link error = %v, want conflict", err)
		}
		_, err = drivers.CreateRule(ctx, owner, foreignID, standardRule(1, "08:00", "17:00"), "foreign-rule")
		if !errors.Is(err, auth.ErrForbidden) {
			t.Fatalf("foreign CreateRule() error = %v, want forbidden", err)
		}
	})

	t.Run("overlapping rules are rejected under concurrency", func(t *testing.T) {
		ctx, _, drivers, _, _, owner, _ := operationsFixture(t)
		start := make(chan struct{})
		results := make(chan error, 2)
		var group sync.WaitGroup
		for _, rule := range []driver.RuleInput{standardRule(1, "08:00", "13:00"), standardRule(1, "12:00", "17:00")} {
			group.Add(1)
			go func(input driver.RuleInput) {
				defer group.Done()
				<-start
				_, err := drivers.CreateRule(ctx, owner, owner.DriverID, input, "concurrent-rule")
				results <- err
			}(rule)
		}
		close(start)
		group.Wait()
		close(results)
		successes, conflicts := 0, 0
		for err := range results {
			if err == nil {
				successes++
			} else if errors.Is(err, driver.ErrConflict) {
				conflicts++
			} else {
				t.Fatalf("concurrent CreateRule() error = %v", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("concurrent results successes/conflicts = %d/%d", successes, conflicts)
		}
	})

	t.Run("stale rule edit and private exception note stay protected", func(t *testing.T) {
		ctx, pool, drivers, _, _, owner, _ := operationsFixture(t)
		ruleID, err := drivers.CreateRule(ctx, owner, owner.DriverID, standardRule(2, "08:00", "17:00"), "rule-create")
		if err != nil {
			t.Fatal(err)
		}
		changed := standardRule(2, "09:00", "17:00")
		if err := drivers.UpdateRule(ctx, owner, owner.DriverID, ruleID, 1, changed, "rule-update"); err != nil {
			t.Fatal(err)
		}
		if err := drivers.UpdateRule(ctx, owner, owner.DriverID, ruleID, 1, changed, "rule-stale"); !errors.Is(err, driver.ErrConflict) {
			t.Fatalf("stale UpdateRule() error = %v, want conflict", err)
		}
		secret := "private Diagnose 4711"
		if _, err := drivers.CreateException(ctx, owner, owner.DriverID, driver.ExceptionInput{Type: driver.ExceptionSick, IsAllDay: true, LocalDate: "2026-09-02", InternalNote: secret}, "sick-create"); err != nil {
			t.Fatal(err)
		}
		var auditText string
		if err := pool.QueryRow(ctx, "SELECT COALESCE(string_agg(metadata::text, ' '), '') FROM audit_events").Scan(&auditText); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(auditText, secret) || strings.Contains(auditText, "Diagnose") {
			t.Fatalf("private exception note leaked into audit: %s", auditText)
		}
	})
}

func operationsFixture(t *testing.T) (context.Context, *pgxpool.Pool, *driver.Service, *resource.Service, auth.Actor, auth.Actor, string) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for integration tests")
	}
	ctx := t.Context()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionDown, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(ctx, config.Database{URL: databaseURL, MaxConnections: 12, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "TRUNCATE availability_exceptions, availability_rules, resources, audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}
	admin := auth.Actor{Role: auth.RoleAdmin, DisplayName: "Admin"}
	owner := auth.Actor{Role: auth.RoleDriver, DisplayName: "Franz Fahrer"}
	if err := pool.QueryRow(ctx, "INSERT INTO users (username, display_name, role, password_hash, must_change_password) VALUES ('operations-admin', 'Admin', 'admin', 'not-used', false) RETURNING id::text").Scan(&admin.UserID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO users (username, display_name, role, password_hash, must_change_password) VALUES ('operations-owner', 'Franz Fahrer', 'driver', 'not-used', false) RETURNING id::text").Scan(&owner.UserID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO drivers (user_id, display_name, availability_policy) VALUES ($1, 'Franz Fahrer', 'legacy_rules') RETURNING id::text", owner.UserID).Scan(&owner.DriverID); err != nil {
		t.Fatal(err)
	}
	var foreignID string
	if err := pool.QueryRow(ctx, "INSERT INTO drivers (display_name, availability_policy) VALUES ('Maria ohne Login', 'legacy_rules') RETURNING id::text").Scan(&foreignID); err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	drivers, err := driver.New(postgres.NewDriverStore(pool), location)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := resource.New(postgres.NewResourceStore(pool))
	if err != nil {
		t.Fatal(err)
	}
	return ctx, pool, drivers, resources, admin, owner, foreignID
}

func standardRule(weekday int, start string, end string) driver.RuleInput {
	return driver.RuleInput{Weekday: weekday, LocalStart: start, LocalEnd: end, ValidFrom: "2026-01-01", Status: driver.RuleAvailable}
}
