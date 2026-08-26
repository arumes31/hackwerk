//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/resource"
	"example.invalid/hackplan/internal/web"
	"github.com/chromedp/cdproto/emulation"
	cdpinput "github.com/chromedp/cdproto/input"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTask04CalendarBrowserJourney(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is required for browser tests")
	}
	pool, identity, drivers, resources, appointments, driverID, chipperID, jobID, dragJobID, adminPassword, driverPassword := task04Application(t, databaseURL)
	customerService, err := customers.NewService(postgres.NewCustomerStore(pool))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		AppName: "HackWerk", BaseURL: "http://127.0.0.1:18533", Database: config.Database{ReadinessTimeout: 2 * time.Second},
		Auth: config.Auth{SessionCookieName: "hackplan_session", CSRFCookieName: "hackplan_csrf", SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 8 * time.Hour},
		Mail: config.Mail{Enabled: true},
	}
	router, err := web.NewRouter(web.Dependencies{
		Config: cfg, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Database: pool, Build: buildinfo.Info{Version: "e2e"},
		Identity: identity, Customers: customerService, Drivers: drivers, Resources: resources, Appointments: appointments, Dashboard: e2eDashboard(t, pool),
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	userDataDir := browserProfileDir(t)
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(browserExecutable(t)), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox, chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.UserDataDir(userDataDir), chromedp.WindowSize(1280, 900))
	allocatorContext, cancelAllocator := chromedp.NewExecAllocator(context.Background(), options...)
	t.Cleanup(cancelAllocator)
	browserContext, cancelBrowser := chromedp.NewContext(allocatorContext)
	t.Cleanup(cancelBrowser)
	browserContext, cancelTimeout := context.WithTimeout(browserContext, 180*time.Second)
	t.Cleanup(cancelTimeout)
	var browserCleanupOnce sync.Once
	cleanupBrowser := func() {
		browserCleanupOnce.Do(func() {
			_ = chromedp.Cancel(browserContext)
			cancelTimeout()
			cancelBrowser()
			cancelAllocator()
			deadline := time.Now().Add(3 * time.Second)
			for {
				if err := os.RemoveAll(userDataDir); err == nil || time.Now().After(deadline) {
					return
				}
				time.Sleep(25 * time.Millisecond)
			}
		})
	}
	t.Cleanup(cleanupBrowser)

	var exceptionLock sync.Mutex
	exceptions := make([]string, 0)
	chromedp.ListenTarget(browserContext, func(event any) {
		if value, ok := event.(*cdpruntime.EventExceptionThrown); ok {
			exceptionLock.Lock()
			exceptions = append(exceptions, value.ExceptionDetails.Text)
			exceptionLock.Unlock()
		}
	})

	if err := chromedp.Run(browserContext,
		chromedp.Navigate(server.URL+"/login"), chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("open login: %s", browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "admin login",
		chromedp.SetValue("#username", "admin-task04", chromedp.ByQuery), chromedp.SetValue("#password", adminPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible("a[href='/calendar']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "open calendar",
		chromedp.Navigate(server.URL+"/calendar?date=2026-08-25"),
		chromedp.WaitVisible("[data-calendar]", chromedp.ByQuery), chromedp.WaitVisible("[data-plan-job='"+jobID+"']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var calendarTimezone string
	var dragReady bool
	var toolbarText string
	var adminReadOnlyNotices, calendarFeedButtons int
	if err := chromedp.Run(browserContext,
		chromedp.Evaluate(`window.hackWerkCalendar.getOption('timeZone')`, &calendarTimezone),
		chromedp.Evaluate(`document.querySelector('[data-calendar-waitlist]').dataset.dragReady === 'true'`, &dragReady),
		chromedp.Text("[data-calendar]", &toolbarText, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('[data-calendar-read-only]').length`, &adminReadOnlyNotices),
		chromedp.Evaluate(`document.querySelectorAll('.calendar-page .calendar-feed-button[href="/calendar/feeds"]').length`, &calendarFeedButtons),
	); err != nil {
		t.Fatal(err)
	}
	if calendarTimezone != "Europe/Vienna" || !dragReady || !strings.Contains(toolbarText, "Heute") || !strings.Contains(toolbarText, "Woche") || !strings.Contains(toolbarText, "Monat") || adminReadOnlyNotices != 0 || calendarFeedButtons != 1 {
		t.Fatalf("calendar timezone/drag/toolbar/admin read-only/feed button = %q/%v/%q/%d/%d", calendarTimezone, dragReady, toolbarText, adminReadOnlyNotices, calendarFeedButtons)
	}
	var calendarLoadMessage string
	if err := runBrowserStep(browserContext, "calendar load failure is recoverable",
		chromedp.Evaluate(`window.__hackWerkFetch=window.fetch;window.fetch=(input,...args)=>String(input).includes('/api/v1/calendar?')?Promise.resolve(new Response('',{status:503})):window.__hackWerkFetch(input,...args);window.hackWerkCalendar.refetchEvents()`, nil),
		chromedp.Poll(`document.querySelector('[data-calendar-message]').textContent.includes('konnten nicht geladen werden')`, nil),
		chromedp.Text("[data-calendar-message]", &calendarLoadMessage, chromedp.ByQuery),
		chromedp.Evaluate(`window.fetch=window.__hackWerkFetch;delete window.__hackWerkFetch;window.hackWerkCalendar.refetchEvents()`, nil),
		chromedp.Poll(`document.querySelector('[data-calendar-message]').textContent.includes('wieder geladen')`, nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !strings.Contains(calendarLoadMessage, "Kalenderstand neu laden") {
		t.Fatalf("calendar failure missed recovery action: %q", calendarLoadMessage)
	}
	if err := dragWaitlistJob(browserContext, dragJobID, "2026-08-26", "10:00:00"); err != nil {
		t.Fatal(browserDiagnostics(browserContext, fmt.Errorf("external waitlist drag: %w", err)))
	}
	var draggedJob string
	if err := chromedp.Run(browserContext,
		chromedp.WaitVisible("[data-planning-dialog]", chromedp.ByQuery),
		chromedp.Value("[data-planning-form] input[name='job_id']", &draggedJob, chromedp.ByQuery),
		chromedp.Click("[data-planning-dialog] [data-dialog-close]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if draggedJob != dragJobID {
		t.Fatalf("external drag opened job %q, want %q", draggedJob, dragJobID)
	}
	if err := runBrowserStep(browserContext, "open mobile proposal form",
		chromedp.EmulateViewport(360, 820),
		chromedp.ActionFunc(func(ctx context.Context) error { return emulation.SetTimezoneOverride("UTC").Do(ctx) }),
		chromedp.Click("[data-plan-job='"+jobID+"']", chromedp.ByQuery),
		chromedp.WaitVisible("[data-planning-dialog]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var defaultPlanningStart string
	if err := chromedp.Run(browserContext,
		chromedp.Value("[data-planning-start]", &defaultPlanningStart, chromedp.ByQuery),
	); err != nil {
		t.Fatal(err)
	}
	vienna, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		t.Fatal(err)
	}
	wantDefaultStart := time.Now().In(vienna).AddDate(0, 0, 1).Format("2006-01-02") + "T08:00"
	if defaultPlanningStart != wantDefaultStart {
		t.Fatalf("UTC-device Vienna default = %q, want %q", defaultPlanningStart, wantDefaultStart)
	}
	var submittedForm string
	if err := runBrowserStep(browserContext, "submit mobile proposal form",
		chromedp.SetValue("[data-planning-start]", "2026-08-25T08:00", chromedp.ByQuery),
		chromedp.SetValue("[data-planning-duration]", "180", chromedp.ByQuery),
		chromedp.Click("input[name='driver_id'][value='"+driverID+"']", chromedp.ByQuery),
		chromedp.SetValue("select[name='primary_driver_id']", driverID, chromedp.ByQuery),
		chromedp.SetValue("select[name='chipper_resource_id']", chipperID, chromedp.ByQuery),
		chromedp.Evaluate(`JSON.stringify([...new FormData(document.querySelector('[data-planning-form]')).entries()])`, &submittedForm),
		chromedp.Click("[data-planning-form] button[type='submit']", chromedp.ByQuery),
		chromedp.Poll(`!document.querySelector('[data-planning-dialog]').open || !document.querySelector('[data-planning-error]').hidden`, nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var planningError string
	var planningOpen bool
	if err := chromedp.Run(browserContext,
		chromedp.Text("[data-planning-error]", &planningError, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-planning-dialog]').open`, &planningOpen),
	); err != nil {
		t.Fatal(err)
	}
	if planningOpen {
		t.Fatalf("planning form stayed open: %s; form=%s", planningError, submittedForm)
	}
	if err := runBrowserStep(browserContext, "proposal appears",
		chromedp.WaitVisible("[data-calendar] .calendar-event-content", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "mobile month view",
		chromedp.Navigate(server.URL+"/calendar?date=2026-08-25&view=month"),
		chromedp.WaitVisible("[data-calendar] .calendar-event-content--month", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var monthView, monthURL, monthToolbar string
	var monthRangeDays int
	var monthHorizontalOverflow bool
	if err := chromedp.Run(browserContext,
		chromedp.Evaluate(`window.hackWerkCalendar.view.type`, &monthView),
		chromedp.Evaluate(`Math.round((window.hackWerkCalendar.view.activeEnd-window.hackWerkCalendar.view.activeStart)/86400000)`, &monthRangeDays),
		chromedp.Evaluate(`window.location.search`, &monthURL),
		chromedp.Text("[data-calendar]", &monthToolbar, chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &monthHorizontalOverflow),
	); err != nil {
		t.Fatal(err)
	}
	if monthView != "dayGridMonth" || monthRangeDays < 28 || !strings.Contains(monthURL, "view=month") ||
		!strings.Contains(monthToolbar, "Monat") || !strings.Contains(monthToolbar, "Tag") || monthHorizontalOverflow {
		t.Fatalf("mobile month view/range/url/toolbar/overflow = %q/%d/%q/%q/%v", monthView, monthRangeDays, monthURL, monthToolbar, monthHorizontalOverflow)
	}
	awaitPromise := func(params *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
		return params.WithAwaitPromise(true)
	}
	var appointmentID, lifecycle string
	if err := pool.QueryRow(t.Context(), "SELECT id::text, lifecycle_status FROM appointments WHERE job_id=$1", jobID).Scan(&appointmentID, &lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "proposal" {
		t.Fatalf("planned lifecycle = %q", lifecycle)
	}
	var adminUserID string
	if err := pool.QueryRow(t.Context(), "SELECT id::text FROM users WHERE username='admin-task04'").Scan(&adminUserID); err != nil {
		t.Fatal(err)
	}
	adminActor := auth.Actor{UserID: adminUserID, Role: auth.RoleAdmin, DisplayName: "Anna Admin"}
	secondAppointment, err := appointments.CreateDraftFromWaitlist(t.Context(), adminActor, appointment.CreateDraftInput{
		JobID: dragJobID, RequestID: "e2e-detail-race-create",
		Time: appointment.TimeInput{StartsAt: time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC), EndsAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondAppointment, err = appointments.AssignDriversAndResources(t.Context(), adminActor, appointment.AssignInput{
		MutateInput: appointment.MutateInput{ID: secondAppointment.ID, ExpectedVersion: secondAppointment.Version, RequestID: "e2e-detail-race-assign"},
		Assignments: appointment.AssignmentInput{
			DriverIDs: []string{driverID}, PrimaryDriverID: driverID,
			Resources: []appointment.ResourceAssignment{{ID: chipperID, Purpose: appointment.PurposeChipping}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondAppointment, err = appointments.ProposeAppointment(t.Context(), adminActor, appointment.MutateInput{
		ID: secondAppointment.ID, ExpectedVersion: secondAppointment.Version, RequestID: "e2e-detail-race-propose",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	appointmentEventSelector := fmt.Sprintf("[data-calendar] [data-job-id='%s'] .calendar-event-content", jobID)
	appointmentRootSelector := fmt.Sprintf("[data-calendar] [data-job-id='%s']", jobID)
	secondEventSelector := fmt.Sprintf("[data-calendar] [data-job-id='%s'] .calendar-event-content", dragJobID)
	clickCurrent := func(selector string) chromedp.Action {
		return chromedp.Evaluate(fmt.Sprintf(`(()=>{const element=document.querySelector(%q);if(!element)throw new Error('element unavailable');element.click()})()`, selector), nil)
	}
	if err := runBrowserStep(browserContext, "return from month to day view",
		chromedp.Navigate(server.URL+"/calendar?date=2026-08-25&view=day"),
		chromedp.WaitVisible(appointmentEventSelector, chromedp.ByQuery),
		chromedp.WaitVisible(secondEventSelector, chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var titleAfterStaleResponse string
	if err := runBrowserStep(browserContext, "latest appointment detail request wins",
		chromedp.Evaluate(fmt.Sprintf(`window.__detailRaceFetch=window.fetch;window.fetch=(input,...args)=>{const url=String(input);if(!url.includes('/api/v1/appointments/'))return window.__detailRaceFetch(input,...args);return window.__detailRaceFetch(input,...args).then(async response=>{const body=await response.json();const delayed=url.includes(%q);body.title=delayed?'Verspäteter Termin':'Aktueller Termin';await new Promise(resolve=>setTimeout(resolve,delayed?300:10));return new Response(JSON.stringify(body),{status:response.status,headers:{'Content-Type':'application/json'}})})}`, appointmentID), nil),
		clickCurrent(appointmentEventSelector),
		clickCurrent(secondEventSelector),
		chromedp.Poll(`document.querySelector('[data-appointment-title]').textContent==='Aktueller Termin'`, nil),
		chromedp.Sleep(400*time.Millisecond),
		chromedp.Text("[data-appointment-title]", &titleAfterStaleResponse, chromedp.ByQuery),
		chromedp.Evaluate(`window.fetch=window.__detailRaceFetch;delete window.__detailRaceFetch`, nil),
		chromedp.Click("[data-appointment-close]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if titleAfterStaleResponse != "Aktueller Termin" {
		t.Fatalf("stale appointment response overwrote latest detail: %q", titleAfterStaleResponse)
	}
	var alternativeStart string
	if err := runBrowserStep(browserContext, "conflict alternatives use Vienna time on UTC device",
		clickCurrent(appointmentEventSelector),
		chromedp.WaitVisible("[data-appointment-reschedule]", chromedp.ByQuery),
		chromedp.SetValue("[data-appointment-start]", "2026-08-25T08:30", chromedp.ByQuery),
		chromedp.Evaluate(fmt.Sprintf(`window.__timeFetch=window.fetch;window.fetch=(input,...args)=>{const url=String(input);if(url.endsWith(%q))return Promise.resolve(new Response(JSON.stringify({error:{code:'reservation_conflict',message:'E2E conflict'}}),{status:409,headers:{'Content-Type':'application/json'}}));if(url.includes('/alternatives?')){window.__alternativeURL=url;return Promise.resolve(new Response(JSON.stringify({conflicts:[],alternatives:[]}),{status:200,headers:{'Content-Type':'application/json'}}));}return window.__timeFetch(input,...args)}`, "/api/v1/appointments/"+appointmentID+"/move"), nil),
		chromedp.Click("[data-appointment-reschedule-submit]", chromedp.ByQuery),
		chromedp.Poll(`Boolean(window.__alternativeURL)`, nil),
		chromedp.Evaluate(`new URL(window.__alternativeURL,location.origin).searchParams.get('starts_at')`, &alternativeStart),
		chromedp.Evaluate(`window.fetch=window.__timeFetch;delete window.__timeFetch;delete window.__alternativeURL`, nil),
		chromedp.Click("[data-appointment-close]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if alternativeStart != "2026-08-25T06:30:00.000Z" {
		t.Fatalf("UTC-device alternative start = %q, want Vienna 06:30Z", alternativeStart)
	}
	var resizeEnabled, resizeHandleVisible bool
	var resizeDebug string
	if err := chromedp.Run(browserContext,
		chromedp.Evaluate(`window.hackWerkCalendar.getOption("eventDurationEditable") === true && window.hackWerkCalendar.getOption("eventResizableFromStart") === true`, &resizeEnabled),
		chromedp.Evaluate(`Array.from(document.querySelectorAll(".calendar-resize-handle")).some((element) => { const box = element.getBoundingClientRect(); return box.width > 0 && box.height >= 10; })`, &resizeHandleVisible),
		chromedp.Evaluate(`JSON.stringify(Array.from(document.querySelectorAll("[data-calendar] *")).filter((element) => ["n-resize", "s-resize"].includes(getComputedStyle(element).cursor)).map((element) => { const box=element.getBoundingClientRect(); const style=getComputedStyle(element); return {html:element.outerHTML,width:box.width,height:box.height,cssWidth:style.width,cssHeight:style.height,display:style.display,position:style.position}; }))`, &resizeDebug),
	); err != nil {
		t.Fatal(err)
	}
	if !resizeEnabled || !resizeHandleVisible {
		t.Fatalf("calendar resize enabled/visible = %v/%v; handles=%s", resizeEnabled, resizeHandleVisible, resizeDebug)
	}

	var staleReasonValues []string
	if err := runBrowserStep(browserContext, "appointment dialog clears prior reasons",
		clickCurrent(appointmentEventSelector),
		chromedp.WaitVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.SetValue("[data-appointment-move-override]", "Nur für Termin A", chromedp.ByQuery),
		chromedp.SetValue("[data-without-notification-reason]", "Nicht versenden", chromedp.ByQuery),
		chromedp.SetValue("[data-confirmation-admin-reason]", "Nicht bestätigen", chromedp.ByQuery),
		chromedp.SetValue("[data-appointment-cancel-reason]", "Nicht übernehmen", chromedp.ByQuery),
		chromedp.SetValue("[data-appointment-reopen-reason]", "Nicht wiederverwenden", chromedp.ByQuery),
		chromedp.SetValue("[data-appointment-reopen-override]", "Nicht freigeben", chromedp.ByQuery),
		chromedp.SetValue("[data-appointment-complete-override-reason]", "Nicht erledigen", chromedp.ByQuery),
		chromedp.Click("[data-appointment-close]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
		clickCurrent(appointmentEventSelector),
		chromedp.WaitVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.Evaluate(`['[data-appointment-move-override]','[data-without-notification-reason]','[data-confirmation-admin-reason]','[data-appointment-cancel-reason]','[data-appointment-reopen-reason]','[data-appointment-reopen-override]','[data-appointment-complete-override-reason]'].map(selector=>document.querySelector(selector).value)`, &staleReasonValues),
		chromedp.Click("[data-appointment-close]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if strings.Join(staleReasonValues, "") != "" {
		t.Fatalf("appointment dialog retained reasons: %v", staleReasonValues)
	}

	if err := runBrowserStep(browserContext, "extend duration from appointment dialog",
		clickCurrent(appointmentEventSelector),
		chromedp.WaitVisible("[data-appointment-reschedule]", chromedp.ByQuery),
		chromedp.Click("[data-appointment-duration-adjust='15']", chromedp.ByQuery),
		chromedp.Click("[data-appointment-reschedule-submit]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var extendedStart, extendedEnd time.Time
	var extendedVersion int32
	if err := pool.QueryRow(t.Context(), "SELECT starts_at, ends_at, version FROM appointments WHERE id=$1", appointmentID).Scan(&extendedStart, &extendedEnd, &extendedVersion); err != nil {
		t.Fatal(err)
	}
	if extendedEnd.Sub(extendedStart) != 195*time.Minute {
		t.Fatalf("extended duration=%s want 195m", extendedEnd.Sub(extendedStart))
	}
	if err := chromedp.Run(browserContext,
		chromedp.Poll(fmt.Sprintf(`Number(window.hackWerkCalendar.getEventById(%q)?.extendedProps.version) === %d && document.querySelector(%q) !== null`, appointmentID, extendedVersion, appointmentEventSelector), nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}

	if err := runBrowserStep(browserContext, "local-time reschedule on UTC device",
		clickCurrent(appointmentEventSelector),
		chromedp.WaitVisible("[data-appointment-reschedule]", chromedp.ByQuery),
		chromedp.SetValue("[data-appointment-start]", "2026-08-25T08:30", chromedp.ByQuery),
		chromedp.SetValue("[data-appointment-duration]", "180", chromedp.ByQuery),
		chromedp.Click("[data-appointment-reschedule-submit]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var movedStart, movedEnd time.Time
	var movedVersion int32
	if err := pool.QueryRow(t.Context(), "SELECT starts_at, ends_at, version FROM appointments WHERE id=$1", appointmentID).Scan(&movedStart, &movedEnd, &movedVersion); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 8, 25, 6, 30, 0, 0, time.UTC); !movedStart.Equal(want) {
		t.Fatalf("keyboard move start=%s want %s", movedStart, want)
	}
	if movedEnd.Sub(movedStart) != 180*time.Minute {
		t.Fatalf("keyboard move duration=%s want 180m", movedEnd.Sub(movedStart))
	}
	if err := chromedp.Run(browserContext,
		chromedp.Poll(fmt.Sprintf(`Number(window.hackWerkCalendar.getEventById(%q)?.extendedProps.version) === %d`, appointmentID, movedVersion), nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}

	if err := runBrowserStep(browserContext, "stale keyboard move stays in dialog",
		clickCurrent("[data-calendar] .calendar-event-content"),
		chromedp.WaitVisible("[data-appointment-reschedule]", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := pool.Exec(ctx, "UPDATE appointments SET version=version+1 WHERE id=$1", appointmentID)
			return err
		}),
		chromedp.Click("[data-appointment-reschedule-submit]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-appointment-error]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var staleError string
	var staleDialogOpen, errorFocused bool
	if err := chromedp.Run(browserContext,
		chromedp.Text("[data-appointment-error]", &staleError, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-appointment-dialog]').open`, &staleDialogOpen),
		chromedp.Evaluate(`document.activeElement === document.querySelector('[data-appointment-error]')`, &errorFocused),
		chromedp.Click("[data-appointment-close]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.Evaluate(`window.hackWerkCalendar.refetchEvents()`, nil),
	); err != nil {
		t.Fatal(err)
	}
	if !staleDialogOpen || !errorFocused || staleError == "" {
		t.Fatalf("stale move dialog/error/focus = %v/%q/%v", staleDialogOpen, staleError, errorFocused)
	}

	var horizontalOverflow bool
	var screenshot []byte
	var appointmentDetailText string
	if err := runBrowserStep(browserContext, "explicit fix",
		clickCurrent("[data-calendar] .calendar-event-content"),
		chromedp.WaitVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-appointment-fix]", chromedp.ByQuery),
		chromedp.Text("[data-appointment-detail]", &appointmentDetailText, chromedp.ByQuery),
		chromedp.Evaluate(`window.confirm=()=>true`, nil),
		chromedp.Click("[data-appointment-fix]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &horizontalOverflow),
		chromedp.FullScreenshot(&screenshot, 90),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if horizontalOverflow {
		t.Fatal("calendar overflows horizontally at 360 px")
	}
	if !strings.Contains(appointmentDetailText, "franz.huber@example.test") || !strings.Contains(appointmentDetailText, "Versand bei Fixierung") || !strings.Contains(appointmentDetailText, "E-Mail") {
		t.Fatalf("appointment detail missing protected contact/channel preview: %q", appointmentDetailText)
	}
	artifact := filepath.Join(t.ArtifactDir(), "task04-mobile-calendar.png")
	if err := os.WriteFile(artifact, screenshot, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("mobile calendar screenshot: %s", artifact)

	var confirmation, workflow string
	var outbox int
	if err := pool.QueryRow(t.Context(), "SELECT a.lifecycle_status, a.confirmation_status, j.workflow_status FROM appointments a JOIN jobs j ON j.id=a.job_id WHERE a.id=$1", appointmentID).Scan(&lifecycle, &confirmation, &workflow); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='appointment.fixed'", appointmentID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "fixed" || confirmation != "pending" || workflow != "scheduled" || outbox != 1 {
		t.Fatalf("fixed lifecycle/confirmation/workflow/outbox = %s/%s/%s/%d", lifecycle, confirmation, workflow, outbox)
	}

	if err := runBrowserStep(browserContext, "cancel and reopen as proposal",
		clickCurrent("[data-calendar] .calendar-event-content"),
		chromedp.WaitVisible("[data-appointment-cancel]", chromedp.ByQuery),
		chromedp.SetValue("[data-appointment-cancel-reason]", "Kunde bittet um Neuplanung", chromedp.ByQuery),
		chromedp.Click("[data-appointment-cancel]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-calendar] .calendar-event--cancelled .calendar-event-content", chromedp.ByQuery),
		clickCurrent("[data-calendar] .calendar-event--cancelled .calendar-event-content"),
		chromedp.WaitVisible("[data-appointment-reopen-panel]", chromedp.ByQuery),
		chromedp.SetValue("[data-appointment-reopen-reason]", "Kunde hat einen neuen Termin angefragt", chromedp.ByQuery),
		chromedp.Click("[data-appointment-reopen]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := pool.QueryRow(t.Context(), "SELECT a.lifecycle_status, a.confirmation_status, j.workflow_status FROM appointments a JOIN jobs j ON j.id=a.job_id WHERE a.id=$1", appointmentID).Scan(&lifecycle, &confirmation, &workflow); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1", appointmentID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	var reopenOutbox int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND event_type='appointment.reopened'", appointmentID).Scan(&reopenOutbox); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "proposal" || confirmation != "not_requested" || workflow != "planning" || outbox != 2 || reopenOutbox != 0 {
		t.Fatalf("reopened lifecycle/confirmation/workflow/outbox/reopen-outbox = %s/%s/%s/%d/%d", lifecycle, confirmation, workflow, outbox, reopenOutbox)
	}
	if err := runBrowserStep(browserContext, "fix reopened proposal",
		clickCurrent("[data-calendar] .calendar-event-content"),
		chromedp.WaitVisible("[data-appointment-fix]", chromedp.ByQuery),
		chromedp.Click("[data-appointment-fix]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-calendar] .calendar-event--fixed .calendar-event-content", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := pool.QueryRow(t.Context(), "SELECT lifecycle_status FROM appointments WHERE id=$1", appointmentID).Scan(&lifecycle); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "fixed" {
		t.Fatalf("refixed lifecycle = %q", lifecycle)
	}
	var keyboardStart, keyboardEnd time.Time
	var keyboardVersion int32
	if err := runBrowserStep(browserContext, "keyboard-only duration mutation",
		chromedp.Focus(appointmentRootSelector, chromedp.ByQuery),
		chromedp.KeyEvent("\r"),
		chromedp.WaitVisible("[data-appointment-reschedule]", chromedp.ByQuery),
		chromedp.Focus("[data-appointment-duration-adjust='15']", chromedp.ByQuery),
		chromedp.KeyEvent("\r"),
		chromedp.Focus("[data-appointment-reschedule-submit]", chromedp.ByQuery),
		chromedp.KeyEvent("\r"),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := pool.QueryRow(t.Context(), "SELECT starts_at, ends_at, version FROM appointments WHERE id=$1", appointmentID).Scan(&keyboardStart, &keyboardEnd, &keyboardVersion); err != nil {
		t.Fatal(err)
	}
	if keyboardEnd.Sub(keyboardStart) != 195*time.Minute {
		t.Fatalf("keyboard-only duration=%s want 195m", keyboardEnd.Sub(keyboardStart))
	}
	if err := chromedp.Run(browserContext,
		chromedp.Poll(fmt.Sprintf(`Number(window.hackWerkCalendar.getEventById(%q)?.extendedProps.version) === %d && document.querySelector(%q) !== null`, appointmentID, keyboardVersion, appointmentEventSelector), nil),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}

	futureDay := time.Now().In(vienna).AddDate(0, 0, 2)
	futureStart := time.Date(futureDay.Year(), futureDay.Month(), futureDay.Day(), 10, 0, 0, 0, vienna).UTC()
	futureEnd := futureStart.Add(2 * time.Hour)
	futureDate := futureStart.In(vienna).Format("2006-01-02")
	if _, err := pool.Exec(t.Context(), `UPDATE appointments SET lifecycle_status='fixed', confirmation_status='not_requested', starts_at=$2, ends_at=$3, fixed_by_user_id=$4, fixed_at=now(), version=version+1 WHERE id=$1`, secondAppointment.ID, futureStart, futureEnd, adminUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET workflow_status='scheduled', version=version+1 WHERE id=$1`, dragJobID); err != nil {
		t.Fatal(err)
	}
	var earlyCompletionError string
	var earlyDialogOpen, earlyReasonFocused bool
	if err := runBrowserStep(browserContext, "admin early completion requires reason",
		chromedp.Navigate(server.URL+"/calendar?date="+futureDate),
		chromedp.WaitVisible(secondEventSelector, chromedp.ByQuery),
		clickCurrent(secondEventSelector),
		chromedp.WaitVisible("[data-appointment-complete-panel]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-appointment-complete-override]", chromedp.ByQuery),
		chromedp.Click("[data-appointment-complete]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-appointment-error]", chromedp.ByQuery),
		chromedp.Text("[data-appointment-error]", &earlyCompletionError, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-appointment-dialog]').open`, &earlyDialogOpen),
		chromedp.Evaluate(`document.activeElement === document.querySelector('[data-appointment-complete-override-reason]')`, &earlyReasonFocused),
		chromedp.Click("[data-appointment-close]", chromedp.ByQuery),
		chromedp.Navigate(server.URL+"/calendar?date=2026-08-25"),
		chromedp.WaitVisible(appointmentEventSelector, chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !earlyDialogOpen || !earlyReasonFocused || !strings.Contains(earlyCompletionError, "vor dem geplanten Terminbeginn") {
		t.Fatalf("early completion dialog/focus/error = %v/%v/%q", earlyDialogOpen, earlyReasonFocused, earlyCompletionError)
	}

	if err := runBrowserStep(browserContext, "driver login",
		chromedp.Evaluate(`document.querySelector("header form[action='/logout']").requestSubmit()`, nil),
		chromedp.WaitVisible("form[action='/login']", chromedp.ByQuery),
		chromedp.SetValue("#username", "driver-task04", chromedp.ByQuery), chromedp.SetValue("#password", driverPassword, chromedp.ByQuery),
		chromedp.Click("form[action='/login'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitVisible(".mobile-bottom-nav a[href='/calendar']", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	var futureCompleteVisible bool
	var futureForbiddenStatus int
	if err := runBrowserStep(browserContext, "driver cannot complete future appointment",
		chromedp.Navigate(server.URL+"/calendar?date="+futureDate),
		chromedp.WaitVisible(secondEventSelector, chromedp.ByQuery),
		clickCurrent(secondEventSelector),
		chromedp.WaitVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.Evaluate(`!document.querySelector('[data-appointment-complete-panel]').hidden`, &futureCompleteVisible),
		chromedp.Evaluate(fmt.Sprintf(`fetch(%q,{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf_token:document.querySelector('[data-appointment-csrf]').value,version:document.querySelector('[data-appointment-dialog]').dataset.version})}).then(response=>response.status)`, "/api/v1/appointments/"+secondAppointment.ID+"/complete"), &futureForbiddenStatus, awaitPromise),
		chromedp.Click("[data-appointment-close]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if futureCompleteVisible || futureForbiddenStatus != 403 {
		t.Fatalf("future driver completion visible/status = %v/%d", futureCompleteVisible, futureForbiddenStatus)
	}

	if _, err := pool.Exec(t.Context(), "UPDATE drivers SET can_complete_jobs=false WHERE id=$1", driverID); err != nil {
		t.Fatal(err)
	}
	var unauthorizedCompleteVisible bool
	var unauthorizedCompleteStatus int
	if err := runBrowserStep(browserContext, "driver without completion right is blocked",
		chromedp.Navigate(server.URL+"/calendar?date=2026-08-25"),
		chromedp.WaitVisible(appointmentEventSelector, chromedp.ByQuery),
		clickCurrent(appointmentEventSelector),
		chromedp.WaitVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.Evaluate(`!document.querySelector('[data-appointment-complete-panel]').hidden`, &unauthorizedCompleteVisible),
		chromedp.Evaluate(fmt.Sprintf(`fetch(%q,{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf_token:document.querySelector('[data-appointment-csrf]').value,version:document.querySelector('[data-appointment-dialog]').dataset.version})}).then(response=>response.status)`, "/api/v1/appointments/"+appointmentID+"/complete"), &unauthorizedCompleteStatus, awaitPromise),
		chromedp.Click("[data-appointment-close]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if unauthorizedCompleteVisible || unauthorizedCompleteStatus != 403 {
		t.Fatalf("unauthorized driver completion visible/status = %v/%d", unauthorizedCompleteVisible, unauthorizedCompleteStatus)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE drivers SET can_complete_jobs=true WHERE id=$1", driverID); err != nil {
		t.Fatal(err)
	}

	var jobLink string
	var customerNoteText, customerNoteFooter string
	if err := runBrowserStep(browserContext, "driver opens assigned job",
		chromedp.Focus(appointmentRootSelector, chromedp.ByQuery),
		chromedp.KeyEvent("\r"),
		chromedp.WaitVisible("[data-appointment-job-link]", chromedp.ByQuery),
		chromedp.AttributeValue("[data-appointment-job-link]", "href", &jobLink, nil, chromedp.ByQuery),
		chromedp.Click("[data-appointment-job-link]", chromedp.ByQuery),
		chromedp.WaitVisible("#job-"+jobID+" > summary", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "driver opens job note editor",
		chromedp.Evaluate(fmt.Sprintf(`document.querySelector(%q).open=true`, "#job-"+jobID), nil),
		chromedp.WaitVisible("#notes-"+jobID+" summary", chromedp.ByQuery),
		chromedp.Evaluate(fmt.Sprintf(`document.querySelector(%q).open=true`, "#notes-"+jobID), nil),
		chromedp.SetValue("form[action='/jobs/"+jobID+"/notes'] textarea[name='body']", "Arbeit vor Ort abgeschlossen", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "driver submits job note",
		chromedp.Evaluate(`document.documentElement.dataset.e2eNavigationMarker='pending'`, nil),
		chromedp.Click("form[action='/jobs/"+jobID+"/notes'] button[type='submit']", chromedp.ByQuery),
		chromedp.WaitNotPresent("html[data-e2e-navigation-marker='pending']", chromedp.ByQuery),
		chromedp.WaitVisible("#job-"+jobID+" > summary", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "driver verifies job note",
		chromedp.Evaluate(fmt.Sprintf(`document.querySelector(%q).open=true;document.querySelector(%q).open=true`, "#job-"+jobID, "#notes-"+jobID), nil),
		chromedp.WaitVisible("#notes-"+jobID+" blockquote", chromedp.ByQuery),
		chromedp.Text("#notes-"+jobID+" blockquote p", &customerNoteText, chromedp.ByQuery),
		chromedp.Text("#notes-"+jobID+" blockquote footer", &customerNoteFooter, chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := runBrowserStep(browserContext, "driver returns to calendar",
		chromedp.Navigate(server.URL+"/calendar?date=2026-08-25"),
		chromedp.WaitVisible(appointmentEventSelector, chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !strings.Contains(jobLink, "/customers/") || !strings.Contains(jobLink, "#job-"+jobID) || customerNoteText != "Arbeit vor Ort abgeschlossen" || !strings.Contains(customerNoteFooter, "Franz Fahrer") {
		t.Fatalf("job link/note/footer = %q/%q/%q", jobLink, customerNoteText, customerNoteFooter)
	}
	var noteBody, noteAuthor string
	var noteCreatedAt time.Time
	if err := pool.QueryRow(t.Context(), `SELECT n.body, u.display_name, n.created_at FROM job_notes n JOIN users u ON u.id=n.author_user_id WHERE n.job_id=$1 ORDER BY n.created_at DESC LIMIT 1`, jobID).Scan(&noteBody, &noteAuthor, &noteCreatedAt); err != nil {
		t.Fatal(err)
	}
	if noteBody != "Arbeit vor Ort abgeschlossen" || noteAuthor != "Franz Fahrer" || noteCreatedAt.IsZero() {
		t.Fatalf("persisted note body/author/time = %q/%q/%s", noteBody, noteAuthor, noteCreatedAt)
	}

	var focusReturned bool
	var dialogDetailText string
	if err := runBrowserStep(browserContext, "keyboard appointment detail",
		chromedp.Focus(appointmentRootSelector, chromedp.ByQuery),
		chromedp.KeyEvent("\r"),
		chromedp.WaitVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.Text("[data-appointment-detail]", &dialogDetailText, chromedp.ByQuery),
		chromedp.KeyEvent("\x1b"),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.Evaluate(fmt.Sprintf(`document.activeElement.matches(%q)`, appointmentRootSelector), &focusReturned),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if !focusReturned || !strings.Contains(dialogDetailText, "Arbeit vor Ort abgeschlossen") || !strings.Contains(dialogDetailText, "Franz Fahrer") {
		t.Fatalf("appointment dialog focus/details = %v/%q", focusReturned, dialogDetailText)
	}
	var planningControls int
	var forbiddenStatus int
	var driverReadOnlyNotice string
	var driverHorizontalOverflow bool
	expression := fmt.Sprintf(`fetch(%q,{method:'POST',headers:{'Content-Type':'application/x-www-form-urlencoded'},body:new URLSearchParams({csrf_token:document.querySelector('[data-calendar]').dataset.csrf,version:'4',starts_at:'2026-09-02T06:00:00Z',ends_at:'2026-09-02T09:00:00Z'})}).then(r=>r.status)`, "/api/v1/appointments/"+appointmentID+"/move")
	if err := chromedp.Run(browserContext,
		chromedp.Evaluate(`document.querySelectorAll('[data-calendar-waitlist],[data-planning-dialog],[data-appointment-fix]').length`, &planningControls),
		chromedp.Text("[data-calendar-read-only]", &driverReadOnlyNotice, chromedp.ByQuery),
		chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &driverHorizontalOverflow),
		chromedp.Evaluate(expression, &forbiddenStatus, awaitPromise),
	); err != nil {
		t.Fatal(err)
	}
	if planningControls != 0 || forbiddenStatus != 403 || driverReadOnlyNotice != "Nur lesen – Planung nur durch Administration" || driverHorizontalOverflow {
		t.Fatalf("driver planning controls/direct status/read-only/overflow = %d/%d/%q/%v", planningControls, forbiddenStatus, driverReadOnlyNotice, driverHorizontalOverflow)
	}
	if err := runBrowserStep(browserContext, "assigned driver completes started appointment",
		clickCurrent(appointmentEventSelector),
		chromedp.WaitVisible("[data-appointment-complete-panel]", chromedp.ByQuery),
		chromedp.Evaluate(`window.confirm=()=>true`, nil),
		chromedp.Click("[data-appointment-complete]", chromedp.ByQuery),
		chromedp.WaitNotVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.WaitVisible("[data-calendar] .calendar-event--completed .calendar-event-content", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if err := pool.QueryRow(t.Context(), "SELECT body FROM job_notes WHERE job_id=$1 ORDER BY created_at DESC LIMIT 1", jobID).Scan(&noteBody); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT a.lifecycle_status, j.workflow_status FROM appointments a JOIN jobs j ON j.id=a.job_id WHERE a.id=$1", appointmentID).Scan(&lifecycle, &workflow); err != nil {
		t.Fatal(err)
	}
	if lifecycle != "completed" || workflow != "completed" || noteBody != "Arbeit vor Ort abgeschlossen" {
		t.Fatalf("driver completion lifecycle/workflow/note = %q/%q/%q", lifecycle, workflow, noteBody)
	}
	var completedActionVisible bool
	if err := runBrowserStep(browserContext, "completed appointment is immutable",
		clickCurrent("[data-calendar] .calendar-event--completed .calendar-event-content"),
		chromedp.WaitVisible("[data-appointment-dialog]", chromedp.ByQuery),
		chromedp.Evaluate(`!document.querySelector('[data-appointment-complete-panel]').hidden`, &completedActionVisible),
		chromedp.Click("[data-appointment-close]", chromedp.ByQuery),
	); err != nil {
		t.Fatal(browserDiagnostics(browserContext, err))
	}
	if completedActionVisible {
		t.Fatal("completed appointment still exposes the completion action")
	}

	cleanupBrowser()
	exceptionLock.Lock()
	defer exceptionLock.Unlock()
	if len(exceptions) > 0 {
		t.Fatalf("browser JavaScript exceptions: %v", exceptions)
	}
}

func task04Application(t *testing.T, databaseURL string) (*pgxpool.Pool, *auth.Service, *driver.Service, *resource.Service, *appointment.Service, string, string, string, string, string, string) {
	t.Helper()
	ctx := t.Context()
	if err := migrate.Run(ctx, databaseURL, migrate.DirectionUp, io.Discard); err != nil {
		t.Fatal(err)
	}
	pool, err := postgres.Open(ctx, config.Database{URL: databaseURL, MaxConnections: 12, ConnectTimeout: 5 * time.Second, ReadinessTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "TRUNCATE outbox_events, appointments, waitlist_entries, jobs, customers, availability_exceptions, availability_rules, resources, audit_events, auth_rate_limits, sessions, drivers, users RESTART IDENTITY CASCADE"); err != nil {
		t.Fatal(err)
	}
	hasher, err := auth.NewPasswordHasher(auth.PasswordParameters{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16, MinLength: 14})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewService(postgres.NewIdentityStore(pool), hasher, time.Now, time.Hour, 8*time.Hour, 5)
	if err != nil {
		t.Fatal(err)
	}
	adminPassword, driverPassword := randomE2EPassword(t), randomE2EPassword(t)
	system := auth.Actor{Role: auth.RoleAdmin, System: true, DisplayName: "E2E Setup"}
	for _, account := range []auth.CreateUserInput{
		{Username: "admin-task04", DisplayName: "Anna Admin", Role: auth.RoleAdmin, Password: adminPassword, RequestID: "e2e-setup"},
		{Username: "driver-task04", DisplayName: "Franz Fahrer", Role: auth.RoleDriver, Password: driverPassword, CreateDriver: true, RequestID: "e2e-setup"},
	} {
		if _, err := identity.CreateUser(ctx, system, account); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET must_change_password=false"); err != nil {
		t.Fatal(err)
	}
	var driverID string
	if err := pool.QueryRow(ctx, "SELECT id::text FROM drivers WHERE display_name='Franz Fahrer'").Scan(&driverID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO availability_rules (driver_id, iso_weekday, local_start, local_end, valid_from, status) VALUES ($1,2,'06:00','20:00','2026-01-01','available')", driverID); err != nil {
		t.Fatal(err)
	}
	var chipperID string
	if err := pool.QueryRow(ctx, "INSERT INTO resources (resource_type,name,exclusive) VALUES ('chipper','Hackmaschine 1',true) RETURNING id::text").Scan(&chipperID); err != nil {
		t.Fatal(err)
	}
	var customerID, jobID, dragJobID string
	if err := pool.QueryRow(ctx, "INSERT INTO customers (first_name,last_name,street,postal_code,locality,email,notification_preference) VALUES ('Franz','Huber','Waldweg 1','4710','Grieskirchen','franz.huber@example.test','email') RETURNING id::text").Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO jobs (job_number,customer_id,job_type,volume_m3,estimated_hack_minutes) VALUES ('HW-2026-0401',$1,'chipping_only',80,180) RETURNING id::text", customerID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO waitlist_entries (job_id) VALUES ($1)", jobID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO jobs (job_number,customer_id,job_type,volume_m3,estimated_hack_minutes) VALUES ('HW-2026-0402',$1,'chipping_only',40,120) RETURNING id::text", customerID).Scan(&dragJobID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO waitlist_entries (job_id) VALUES ($1)", dragJobID); err != nil {
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
	appointments, err := appointment.New(postgres.NewAppointmentStore(pool), drivers, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return pool, identity, drivers, resources, appointments, driverID, chipperID, jobID, dragJobID, adminPassword, driverPassword
}

type dragCoordinates struct {
	SourceX float64 `json:"sourceX"`
	SourceY float64 `json:"sourceY"`
	TargetX float64 `json:"targetX"`
	TargetY float64 `json:"targetY"`
}

func dragWaitlistJob(ctx context.Context, jobID, date, localTime string) error {
	var points dragCoordinates
	expression := fmt.Sprintf(`(() => {
		const source = document.querySelector('[data-calendar-job=%q]');
		const day = [...document.querySelectorAll('[data-date=%q]')].sort((a,b) => b.getBoundingClientRect().height - a.getBoundingClientRect().height)[0];
		const slot = document.querySelector('[data-time=%q]');
		if (!source || !day || !slot) throw new Error('drag coordinates unavailable');
		const s = source.getBoundingClientRect(), d = day.getBoundingClientRect(), t = slot.getBoundingClientRect();
		return {sourceX:s.left+s.width/2, sourceY:s.top+s.height/2, targetX:d.left+d.width/2, targetY:t.top+Math.min(4,t.height/2)};
	})()`, jobID, date, localTime)
	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &points)); err != nil {
		return err
	}
	return chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			if err := cdpinput.DispatchMouseEvent(cdpinput.MouseMoved, points.SourceX, points.SourceY).Do(ctx); err != nil {
				return err
			}
			if err := cdpinput.DispatchMouseEvent(cdpinput.MousePressed, points.SourceX, points.SourceY).WithButton(cdpinput.Left).WithButtons(1).WithClickCount(1).Do(ctx); err != nil {
				return err
			}
			for step := 1; step <= 8; step++ {
				ratio := float64(step) / 8
				x := points.SourceX + (points.TargetX-points.SourceX)*ratio
				y := points.SourceY + (points.TargetY-points.SourceY)*ratio
				if err := cdpinput.DispatchMouseEvent(cdpinput.MouseMoved, x, y).WithButton(cdpinput.Left).WithButtons(1).Do(ctx); err != nil {
					return err
				}
			}
			return cdpinput.DispatchMouseEvent(cdpinput.MouseReleased, points.TargetX, points.TargetY).WithButton(cdpinput.Left).WithClickCount(1).Do(ctx)
		}),
	)
}
