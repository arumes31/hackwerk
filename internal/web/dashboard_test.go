package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/dashboard"
	"example.invalid/hackplan/web/templates"
)

type dashboardStoreStub struct {
	snapshot dashboard.Snapshot
	err      error
}

func (store dashboardStoreStub) Load(context.Context, dashboard.Window) (dashboard.Snapshot, error) {
	return store.snapshot, store.err
}

func TestDashboardPageSeparatesAdminAndDriverDetails(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Vienna")
	service, err := dashboard.New(dashboardStoreStub{snapshot: dashboard.Snapshot{
		Counts:     dashboard.Counts{Waitlist: 4, NotificationIssues: 2, Overrides: 1},
		Drivers:    []dashboard.DriverAvailability{{ID: "driver", Name: "Franz Fahrer", State: "nicht verfügbar"}},
		UrgentJobs: []dashboard.UrgentJob{{ID: "job", Number: "HW-1", CustomerName: "Testkunde", VolumeM3: "80", Urgency: "urgent"}},
	}}, dashboard.Config{Location: location, HorizonDays: 14, PendingAfter: 15 * time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00"}, func() time.Time {
		return time.Date(2026, 8, 25, 10, 0, 0, 0, location)
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	page := templates.PageData{AppName: "HackWerk", Version: "test"}
	for _, test := range []struct {
		name, role, driverID string
		want, forbidden      []string
	}{
		{name: "admin", role: "admin", want: []string{"Versandprobleme", "Dringende Aufträge", "Testkunde", "Keine Einsätze"}},
		{name: "driver", role: "driver", driverID: "driver", want: []string{"Meine Verfügbarkeit", "nicht verfügbar"}, forbidden: []string{"Versandprobleme", "Testkunde", "Overrides"}},
		{name: "driver without profile", role: "driver", want: []string{"Kein Fahrerprofil ist diesem Zugang zugeordnet."}, forbidden: []string{`href="/availability">Meine Verfügbarkeit</a>`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/dashboard", nil)
			actor := auth.Actor{UserID: "user", DisplayName: "Interner Nutzer", Role: auth.Role(test.role), DriverID: test.driverID}
			request = request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, auth.Session{Actor: actor}))
			response := httptest.NewRecorder()
			dashboardPage(service, nil, page, "csrf", logger).ServeHTTP(response, request)
			body := response.Body.String()
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", response.Code, body)
			}
			for _, wanted := range test.want {
				if !strings.Contains(body, wanted) {
					t.Fatalf("dashboard missing %q", wanted)
				}
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(body, forbidden) {
					t.Fatalf("driver dashboard leaked %q", forbidden)
				}
			}
		})
	}
}

func TestDashboardPageRendersRecoverableReadFailure(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Vienna")
	service, _ := dashboard.New(dashboardStoreStub{err: errors.New("database unavailable")}, dashboard.Config{
		Location: location, HorizonDays: 14, PendingAfter: 15 * time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00",
	}, time.Now)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/dashboard", nil)
	request = request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, auth.Session{Actor: auth.Actor{UserID: "admin", DisplayName: "Admin", Role: auth.RoleAdmin}}))
	response := httptest.NewRecorder()
	dashboardPage(service, nil, templates.PageData{AppName: "HackWerk"}, "csrf", slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))).ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), "Heute erneut laden") || strings.Contains(response.Body.String(), "database unavailable") {
		t.Fatalf("recoverable dashboard failure = %d %s", response.Code, response.Body.String())
	}
}

func TestDashboardPageRejectsInvalidDate(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Vienna")
	service, _ := dashboard.New(dashboardStoreStub{}, dashboard.Config{Location: location, HorizonDays: 14, PendingAfter: 15 * time.Minute, BusinessOpen: "07:00", BusinessClose: "17:00"}, func() time.Time {
		return time.Date(2026, 8, 25, 10, 0, 0, 0, location)
	})
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/dashboard?date=unbegrenzt", nil)
	request = request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, auth.Session{Actor: auth.Actor{UserID: "admin", DisplayName: "Admin", Role: auth.RoleAdmin}}))
	response := httptest.NewRecorder()
	dashboardPage(service, nil, templates.PageData{AppName: "HackWerk"}, "csrf", slog.Default()).ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "außerhalb") {
		t.Fatalf("invalid date response = %d %s", response.Code, response.Body.String())
	}
}
