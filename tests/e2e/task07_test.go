//go:build e2e

package e2e_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/calendarfeed"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/web"
	"github.com/chromedp/chromedp"
)

func TestTask07PrivateCalendarFeedBrowserJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, driverID, chipperID, jobID, _, adminPassword, _ := task04Application(t, databaseURL)
	admin := auth.Actor{Role: auth.RoleAdmin, DisplayName: "Anna Admin"}
	if err := pool.QueryRow(t.Context(), "SELECT id::text FROM users WHERE username='admin-task04'").Scan(&admin.UserID); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC)
	draft, err := appointments.CreateDraftFromWaitlist(t.Context(), admin, appointment.CreateDraftInput{JobID: jobID, RequestID: "feed-draft", Time: appointment.TimeInput{StartsAt: start, EndsAt: start.Add(3 * time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := appointments.AssignDriversAndResources(t.Context(), admin, appointment.AssignInput{MutateInput: appointment.MutateInput{ID: draft.ID, ExpectedVersion: draft.Version, RequestID: "feed-assign"}, Assignments: appointment.AssignmentInput{DriverIDs: []string{driverID}, PrimaryDriverID: driverID, Resources: []appointment.ResourceAssignment{{ID: chipperID, Purpose: appointment.PurposeChipping}}}})
	if err != nil {
		t.Fatal(err)
	}
	proposed, err := appointments.ProposeAppointment(t.Context(), admin, appointment.MutateInput{ID: assigned.ID, ExpectedVersion: assigned.Version, RequestID: "feed-propose"}, "")
	if err != nil {
		t.Fatal(err)
	}
	fixed, err := appointments.FixAppointment(t.Context(), admin, appointment.FixInput{MutateInput: appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "feed-fix"}})
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewUnstartedServer(nil)
	feedService, err := calendarfeed.New(postgres.NewCalendarFeedStore(pool), calendarfeed.Config{BaseURL: "http://" + server.Listener.Addr().String(), UIDDomain: "hackwerk.example", CalendarName: "HackWerk Termine", ExportMaxDays: 366, HistoryDays: 90, FutureDays: 366}, func() time.Time { return start }, auth.NewToken)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{AppName: "HackWerk", BaseURL: "http://" + server.Listener.Addr().String(), Timezone: "Europe/Vienna", Database: config.Database{ReadinessTimeout: 2 * time.Second}, Auth: config.Auth{SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf", SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour}, CalendarFeed: config.CalendarFeed{RateLimit: 120}}
	router, err := web.NewRouter(web.Dependencies{Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"}, Identity: identity, Drivers: drivers, Resources: resources, Appointments: appointments, Dashboard: e2eDashboard(t, pool), CalendarFeeds: feedService})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = router
	server.Start()
	t.Cleanup(server.Close)

	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.UserDataDir(browserProfileDir(t)), chromedp.WindowSize(360, 800))
	allocator, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browser, cancelBrowser := chromedp.NewContext(allocator)
	t.Cleanup(cancelBrowser)
	browser, cancelTimeout := context.WithTimeout(browser, 180*time.Second)
	t.Cleanup(cancelTimeout)
	t.Cleanup(func() { _ = chromedp.Cancel(browser) })
	var feedURL string
	var overflow bool
	if err := chromedp.Run(browser,
		chromedp.Navigate(server.URL+"/login"), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery), chromedp.SetValue("#username", "admin-task04", chromedp.ByQuery), chromedp.SetValue("#password", adminPassword, chromedp.ByQuery), chromedp.Click("form[action='/login'] button", chromedp.ByQuery), chromedp.WaitVisible("main.dashboard-page", chromedp.ByQuery),
		chromedp.Navigate(server.URL+"/calendar/feeds"), chromedp.WaitVisible("form[action='/calendar/feeds']", chromedp.ByQuery), chromedp.SetValue("form[action='/calendar/feeds'] [name='name']", "Privater Testfeed", chromedp.ByQuery), chromedp.SetValue("form[action='/calendar/feeds'] [name='detail']", "minimal", chromedp.ByQuery), chromedp.Click("form[action='/calendar/feeds'] button[type='submit']", chromedp.ByQuery), chromedp.WaitVisible("#new-feed-url", chromedp.ByQuery), chromedp.Value("#new-feed-url", &feedURL, chromedp.ByQuery), chromedp.Evaluate(`document.documentElement.scrollWidth>window.innerWidth`, &overflow),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if overflow || !strings.Contains(feedURL, "/feeds/") {
		t.Fatalf("feed URL/overflow = %q/%v", feedURL, overflow)
	}
	firstBody, firstStatus, firstCookies := fetchICS(t, feedURL)
	if firstStatus != http.StatusOK || firstCookies != "" || strings.Contains(firstBody, "Franz Huber") {
		t.Fatalf("first feed = %d cookies=%q\n%s", firstStatus, firstCookies, firstBody)
	}
	uid := icsLine(firstBody, "UID")
	sequence := icsLine(firstBody, "SEQUENCE")
	if uid == "" || sequence == "" {
		t.Fatalf("missing UID/sequence: %s", firstBody)
	}

	moved, err := appointments.MoveAppointment(t.Context(), admin, appointment.MoveInput{MutateInput: appointment.MutateInput{ID: fixed.ID, ExpectedVersion: fixed.Version, RequestID: "feed-move"}, StartsAt: start.AddDate(0, 0, 7), EndsAt: start.AddDate(0, 0, 7).Add(3 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	_ = moved
	secondBody, secondStatus, _ := fetchICS(t, feedURL)
	if secondStatus != http.StatusOK || icsLine(secondBody, "UID") != uid || icsLine(secondBody, "SEQUENCE") == sequence || !strings.Contains(secondBody, "DTSTART:20260908T060000Z") {
		t.Fatalf("updated feed = %d\n%s", secondStatus, secondBody)
	}

	raw := feedURL[strings.Index(feedURL, "/feeds/")+len("/feeds/") : strings.LastIndex(feedURL, "/calendar.ics")]
	if err := chromedp.Run(browser,
		chromedp.Navigate(server.URL+"/calendar/feeds"),
		chromedp.WaitVisible(".feed-card", chromedp.ByQuery),
		chromedp.Evaluate(`window.confirm=()=>false`, nil),
		chromedp.Click(".feed-card form[action$='/rotate'] button", chromedp.ByQuery),
		chromedp.WaitVisible(".feed-card form[action$='/rotate'] button", chromedp.ByQuery),
		chromedp.Click(".feed-card form[action$='/revoke'] button", chromedp.ByQuery),
		chromedp.WaitVisible(".feed-card form[action$='/revoke'] button", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	if _, statusAfterCancel, _ := fetchICS(t, feedURL); statusAfterCancel != http.StatusOK {
		t.Fatalf("feed changed despite cancelled confirmation: status=%d", statusAfterCancel)
	}
	if err := chromedp.Run(browser,
		chromedp.Evaluate(`window.confirm=()=>true`, nil),
		chromedp.Click(".feed-card form[action$='/revoke'] button", chromedp.ByQuery),
		chromedp.WaitNotPresent(".feed-card form[action$='/revoke']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browser, err))
	}
	var pageText string
	if err := chromedp.Run(browser, chromedp.Text("main", &pageText, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pageText, raw) || !strings.Contains(pageText, "Widerrufen") {
		t.Fatalf("feed list leaked token or missed revoke: %s", pageText)
	}
	_, revokedStatus, _ := fetchICS(t, feedURL)
	if revokedStatus != http.StatusNotFound {
		t.Fatalf("revoked status = %d", revokedStatus)
	}
}

func fetchICS(t *testing.T, url string) (string, int, string) {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload), response.StatusCode, response.Header.Get("Set-Cookie")
}
func icsLine(value, name string) string {
	match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `:(.+)\r$`).FindStringSubmatch(value)
	if len(match) != 2 {
		return ""
	}
	return match[1]
}
