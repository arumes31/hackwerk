// Package cli implements HackWerk's stable command-line contract.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/adapters/postgres/migrate"
	"example.invalid/hackplan/internal/app"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/buildinfo"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/logging"
	"example.invalid/hackplan/internal/resource"
	"example.invalid/hackplan/internal/web"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ExitSuccess = 0
	ExitFailure = 1
	ExitUsage   = 2
)

// IO contains process streams for deterministic command testing.
type IO struct {
	Input  io.Reader
	Output io.Writer
	Error  io.Writer
}

// Run dispatches one command and returns a stable process exit code.
func Run(ctx context.Context, arguments []string, streams IO) int {
	if len(arguments) == 0 {
		writeHelp(streams.Output)
		return ExitUsage
	}
	if arguments[0] == "help" || arguments[0] == "--help" || arguments[0] == "-h" {
		writeHelp(streams.Output)
		return ExitSuccess
	}
	if arguments[0] == "version" {
		if len(arguments) != 1 {
			_, _ = fmt.Fprintln(streams.Error, "Verwendung: hackwerk version")
			return ExitUsage
		}
		writeVersion(streams.Output)
		return ExitSuccess
	}
	if len(arguments) == 2 && isHelp(arguments[1]) {
		if writeCommandHelp(streams.Output, arguments[0]) {
			return ExitSuccess
		}
	}
	if !knownConfiguredCommand(arguments[0]) {
		_, _ = fmt.Fprintf(streams.Error, "Unbekannter Befehl %q.\n", arguments[0])
		writeHelp(streams.Error)
		return ExitUsage
	}

	cfg, err := config.Load()
	if err != nil {
		_, _ = fmt.Fprintln(streams.Error, "Konfiguration ist ungültig:", err)
		return ExitFailure
	}
	logger := logging.New(cfg, streams.Error, arguments[0], buildinfo.Version)
	slog.SetDefault(logger)

	switch arguments[0] {
	case "serve":
		if len(arguments) != 1 {
			return usage(streams.Error, "Verwendung: hackwerk serve")
		}
		return runProcess(streams.Error, logger, func() error { return app.Serve(ctx, cfg, logger) })
	case "worker":
		if len(arguments) != 1 {
			return usage(streams.Error, "Verwendung: hackwerk worker")
		}
		return runProcess(streams.Error, logger, func() error { return app.Worker(ctx, cfg, logger) })
	case "migrate":
		return runMigrate(ctx, arguments[1:], cfg, streams, logger)
	case "seed-dev":
		return runSeed(ctx, arguments[1:], cfg, streams, logger)
	case "admin":
		return runAdmin(ctx, arguments[1:], cfg, streams, logger)
	case "healthcheck":
		if len(arguments) != 1 {
			return usage(streams.Error, "Verwendung: hackwerk healthcheck")
		}
		return runProcess(streams.Error, logger, func() error {
			return web.Healthcheck(ctx, strings.TrimRight(cfg.BaseURL, "/"), 5*time.Second)
		})
	}
	return ExitUsage
}

func knownConfiguredCommand(command string) bool {
	switch command {
	case "serve", "worker", "migrate", "seed-dev", "admin", "healthcheck":
		return true
	default:
		return false
	}
}

func isHelp(argument string) bool {
	return argument == "help" || argument == "--help" || argument == "-h"
}

func writeCommandHelp(output io.Writer, command string) bool {
	help := map[string]string{
		"serve":       "Verwendung: hackwerk serve\nStartet den HTTP-Webdienst.",
		"worker":      "Verwendung: hackwerk worker\nStartet Hintergrundprozesse.",
		"migrate":     "Verwendung: hackwerk migrate up|down|status\nVerwaltet das Datenbankschema.",
		"seed-dev":    "Verwendung: hackwerk seed-dev\nErzeugt ausschließlich lokale Entwicklungsdaten.",
		"admin":       adminHelp,
		"healthcheck": "Verwendung: hackwerk healthcheck\nPrüft die Readiness des Webdienstes.",
	}
	message, ok := help[command]
	if ok {
		_, _ = fmt.Fprintln(output, message)
	}
	return ok
}

func usage(output io.Writer, message string) int {
	_, _ = fmt.Fprintln(output, message)
	return ExitUsage
}

func runMigrate(ctx context.Context, arguments []string, cfg config.Config, streams IO, logger *slog.Logger) int {
	if len(arguments) != 1 {
		_, _ = fmt.Fprintln(streams.Error, "Verwendung: hackwerk migrate up|down|status")
		return ExitUsage
	}
	direction := migrate.Direction(arguments[0])
	return runProcess(streams.Error, logger, func() error {
		return migrate.Run(ctx, cfg.Database.URL, direction, streams.Output)
	})
}

func runSeed(ctx context.Context, arguments []string, cfg config.Config, streams IO, logger *slog.Logger) int {
	if len(arguments) > 0 {
		_, _ = fmt.Fprintln(streams.Error, "Verwendung: hackwerk seed-dev")
		return ExitUsage
	}
	if cfg.Environment != config.EnvironmentDevelopment && cfg.Environment != config.EnvironmentTest {
		_, _ = fmt.Fprintln(streams.Error, "seed-dev ist nur in Entwicklung oder Test erlaubt.")
		return ExitFailure
	}
	code := runProcess(streams.Error, logger, func() error {
		return migrate.Run(ctx, cfg.Database.URL, migrate.DirectionUp, streams.Output)
	})
	if code != ExitSuccess {
		return code
	}
	return runProcess(streams.Error, logger, func() error { return seedIdentity(ctx, cfg, streams.Output) })
}

func runAdmin(ctx context.Context, arguments []string, cfg config.Config, streams IO, logger *slog.Logger) int {
	if len(arguments) == 0 {
		return usage(streams.Error, adminHelp)
	}
	return runProcess(streams.Error, logger, func() error {
		pool, err := postgres.Open(ctx, cfg.Database)
		if err != nil {
			return err
		}
		defer pool.Close()
		identity, err := app.IdentityService(cfg, pool)
		if err != nil {
			return err
		}
		actor := auth.Actor{Role: auth.RoleAdmin, System: true, DisplayName: "Admin-CLI"}
		switch arguments[0] {
		case "list":
			if len(arguments) != 1 {
				return errors.New("admin: list accepts no arguments")
			}
			users, listErr := identity.ListUsers(ctx, actor)
			if listErr != nil {
				return listErr
			}
			for _, user := range users {
				_, _ = fmt.Fprintf(streams.Output, "%s\t%s\t%s\t%t\n", user.Username, user.DisplayName, user.Role, user.Active)
			}
			return nil
		case "create":
			return adminCreate(ctx, identity, actor, arguments[1:], streams)
		case "reset-password":
			return adminResetPassword(ctx, identity, actor, arguments[1:], streams)
		default:
			return fmt.Errorf("admin: unknown subcommand %q", arguments[0])
		}
	})
}

const adminHelp = `Verwendung:
  hackwerk admin create --username NAME --display-name NAME [--role admin|driver] [--email MAIL] [--driver] [--password-file DATEI]
  hackwerk admin reset-password --username NAME [--password-file DATEI]
  hackwerk admin list

Ohne --password-file wird das Passwort aus stdin gelesen. Passwörter sind nie Kommandozeilenargumente.`

func adminCreate(ctx context.Context, identity *auth.Service, actor auth.Actor, arguments []string, streams IO) error {
	flags := flag.NewFlagSet("admin create", flag.ContinueOnError)
	flags.SetOutput(streams.Error)
	username := flags.String("username", "", "Benutzername")
	displayName := flags.String("display-name", "", "Anzeigename")
	role := flags.String("role", "driver", "admin oder driver")
	email := flags.String("email", "", "optionale E-Mail")
	createDriver := flags.Bool("driver", false, "Fahrerprofil anlegen")
	passwordFile := flags.String("password-file", "", "Datei mit Passwort")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	password, err := readPassword(streams.Input, *passwordFile)
	if err != nil {
		return err
	}
	_, err = identity.CreateUser(ctx, actor, auth.CreateUserInput{
		Username: *username, DisplayName: *displayName, Email: *email, Role: auth.Role(*role),
		Password: password, CreateDriver: *createDriver, RequestID: "admin-cli",
	})
	return err
}

func adminResetPassword(ctx context.Context, identity *auth.Service, actor auth.Actor, arguments []string, streams IO) error {
	flags := flag.NewFlagSet("admin reset-password", flag.ContinueOnError)
	flags.SetOutput(streams.Error)
	username := flags.String("username", "", "Benutzername")
	passwordFile := flags.String("password-file", "", "Datei mit Passwort")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	user, err := identity.FindUserForAdministration(ctx, actor, *username)
	if err != nil {
		return err
	}
	password, err := readPassword(streams.Input, *passwordFile)
	if err != nil {
		return err
	}
	return identity.ResetPassword(ctx, actor, auth.ResetPasswordInput{
		UserID: user.ID, Password: password, ExpectedVersion: user.Version, RequestID: "admin-cli",
	})
}

func readPassword(input io.Reader, passwordFile string) (string, error) {
	if passwordFile != "" {
		content, err := os.ReadFile(passwordFile)
		if err != nil {
			return "", fmt.Errorf("admin: reading password file: %w", err)
		}
		if len(content) > 4096 {
			return "", errors.New("admin: password file is too large")
		}
		return strings.TrimSpace(string(content)), nil
	}
	if input == nil {
		return "", errors.New("admin: password is required via stdin or --password-file")
	}
	reader := bufio.NewReader(io.LimitReader(input, 4097))
	password, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("admin: reading password: %w", err)
	}
	if len(password) > 4096 {
		return "", errors.New("admin: password input is too large")
	}
	return strings.TrimSpace(password), nil
}

func seedIdentity(ctx context.Context, cfg config.Config, output io.Writer) error {
	pool, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	identity, err := app.IdentityService(cfg, pool)
	if err != nil {
		return err
	}
	actor := auth.Actor{Role: auth.RoleAdmin, System: true, DisplayName: "Development Seed"}
	accounts := []struct {
		username    string
		displayName string
		role        auth.Role
	}{
		{"admin", "Anna Admin", auth.RoleAdmin}, {"anna", "Anna Fahrerin", auth.RoleDriver},
		{"bernd", "Bernd Fahrer", auth.RoleDriver}, {"christian", "Christian Fahrer", auth.RoleDriver},
		{"doris", "Doris Fahrerin", auth.RoleDriver}, {"emil", "Emil Fahrer", auth.RoleDriver},
	}
	created := 0
	for _, account := range accounts {
		if _, findErr := identity.FindUserForAdministration(ctx, actor, account.username); findErr == nil {
			continue
		} else if !errors.Is(findErr, auth.ErrNotFound) {
			return findErr
		}
		raw, tokenErr := auth.NewToken()
		if tokenErr != nil {
			return tokenErr
		}
		password := "Dev-2026-" + raw[:18]
		if _, createErr := identity.CreateUser(ctx, actor, auth.CreateUserInput{
			Username: account.username, DisplayName: account.displayName, Role: account.role,
			Password: password, CreateDriver: account.role == auth.RoleDriver, RequestID: "seed-dev",
		}); createErr != nil {
			return createErr
		}
		_, _ = fmt.Fprintf(output, "%s: %s\n", account.username, password)
		created++
	}
	if created == 0 {
		_, _ = fmt.Fprintln(output, "Alle sechs Development-Zugänge existieren bereits; keine Passwörter wurden verändert.")
	} else {
		_, _ = fmt.Fprintln(output, "Temporäre Passwörter wurden nur dieses eine Mal ausgegeben und müssen beim ersten Login geändert werden.")
	}
	if err := seedCustomerScenarios(ctx, pool, actor, output); err != nil {
		return err
	}
	if err := seedOperations(ctx, pool, identity, actor, output); err != nil {
		return err
	}
	if err := seedCalendar(ctx, pool, actor, output); err != nil {
		return err
	}
	return nil
}

func seedCalendar(ctx context.Context, pool *pgxpool.Pool, actor auth.Actor, output io.Writer) error {
	var jobID, driverID, resourceID string
	if err := pool.QueryRow(ctx, `SELECT j.id::text FROM jobs j JOIN customers c ON c.id=j.customer_id
		WHERE c.last_name='Maier' AND j.workflow_status IN ('waitlist','planning') ORDER BY j.created_at LIMIT 1`).Scan(&jobID); err != nil {
		return fmt.Errorf("seed: finding calendar job: %w", err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM appointments WHERE job_id=$1 AND lifecycle_status IN ('draft','proposal','fixed'))", jobID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		_, _ = fmt.Fprintln(output, "Development-Kalendervorschlag existiert bereits.")
		return nil
	}
	if err := pool.QueryRow(ctx, "SELECT id::text FROM drivers WHERE display_name='Anna Fahrerin' AND active").Scan(&driverID); err != nil {
		return fmt.Errorf("seed: finding calendar driver: %w", err)
	}
	if err := pool.QueryRow(ctx, "SELECT id::text FROM resources WHERE name='Hackmaschine 1' AND active").Scan(&resourceID); err != nil {
		return fmt.Errorf("seed: finding calendar resource: %w", err)
	}
	driverService, err := app.DriverService(pool)
	if err != nil {
		return err
	}
	service, err := app.AppointmentService(pool, driverService)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		return err
	}
	now := time.Now().In(location)
	daysUntilThursday := (int(time.Thursday) - int(now.Weekday()) + 7) % 7
	if daysUntilThursday == 0 {
		daysUntilThursday = 7
	}
	day := now.AddDate(0, 0, daysUntilThursday)
	startsAt := time.Date(day.Year(), day.Month(), day.Day(), 8, 0, 0, 0, location).UTC()
	draft, err := service.CreateDraftFromWaitlist(ctx, actor, appointment.CreateDraftInput{
		JobID: jobID, RequestID: "seed-dev", Time: appointment.TimeInput{StartsAt: startsAt, EndsAt: startsAt.Add(6 * time.Hour)},
	})
	if err != nil {
		return err
	}
	assigned, err := service.AssignDriversAndResources(ctx, actor, appointment.AssignInput{
		MutateInput: appointment.MutateInput{ID: draft.ID, ExpectedVersion: draft.Version, RequestID: "seed-dev"},
		Assignments: appointment.AssignmentInput{
			DriverIDs: []string{driverID}, PrimaryDriverID: driverID,
			Resources: []appointment.ResourceAssignment{{ID: resourceID, Purpose: appointment.PurposeChipping}},
		},
	})
	if err != nil {
		return err
	}
	if _, err := service.ProposeAppointment(ctx, actor, appointment.MutateInput{ID: assigned.ID, ExpectedVersion: assigned.Version, RequestID: "seed-dev"}, ""); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(output, "1 Development-Kalendervorschlag neu angelegt.")
	return nil
}

func seedOperations(ctx context.Context, pool *pgxpool.Pool, identity *auth.Service, actor auth.Actor, output io.Writer) error {
	resourceService, err := app.ResourceService(pool)
	if err != nil {
		return err
	}
	existingResources, err := resourceService.List(ctx, actor)
	if err != nil {
		return err
	}
	resourceNames := make(map[string]bool, len(existingResources))
	for _, item := range existingResources {
		resourceNames[item.Name] = true
	}
	createdResources := 0
	for _, input := range resourceSeedInputs() {
		if resourceNames[input.Name] {
			continue
		}
		if _, err = resourceService.Create(ctx, actor, input, "seed-dev"); err != nil {
			return err
		}
		createdResources++
	}

	driverService, err := app.DriverService(pool)
	if err != nil {
		return err
	}
	profiles, err := driverService.ListProfiles(ctx, actor)
	if err != nil {
		return err
	}
	byUserID := make(map[string]driver.Profile, len(profiles))
	for _, profile := range profiles {
		byUserID[profile.UserID] = profile
	}
	for _, account := range driverSeedAccounts() {
		user, findErr := identity.FindUserForAdministration(ctx, actor, account.username)
		if findErr != nil {
			return findErr
		}
		profile, ok := byUserID[user.ID]
		if !ok {
			id, createErr := driverService.CreateProfile(ctx, actor, driver.ProfileInput{UserID: user.ID, DisplayName: account.displayName, CanCompleteJobs: true}, "seed-dev")
			if createErr != nil {
				return createErr
			}
			profile = driver.Profile{ID: id, UserID: user.ID, DisplayName: account.displayName, IsActive: true}
		}
		schedule, scheduleErr := driverService.Schedule(ctx, actor, profile.ID)
		if scheduleErr != nil {
			return scheduleErr
		}
		if len(schedule.Rules) == 0 {
			for _, rule := range account.rules {
				if _, createErr := driverService.CreateRule(ctx, actor, profile.ID, rule, "seed-dev"); createErr != nil {
					return createErr
				}
			}
		}
		if len(schedule.Exceptions) == 0 && account.exception != nil {
			if _, createErr := driverService.CreateException(ctx, actor, profile.ID, *account.exception, "seed-dev"); createErr != nil {
				return createErr
			}
		}
	}
	_, _ = fmt.Fprintf(output, "%d Development-Ressourcen neu angelegt; Fahrer-Verfügbarkeiten geprüft.\n", createdResources)
	return nil
}

func resourceSeedInputs() []resource.Input {
	volume := 120.0
	payload := int32(3500)
	seats := int32(3)
	return []resource.Input{
		{Type: resource.TypeChipper, Name: "Hackmaschine 1", IsExclusive: true, Capacity: resource.Capacity{VolumeM3: &volume}},
		{Type: resource.TypeTransportVehicle, Name: "Transporter 1", IsExclusive: true, Capacity: resource.Capacity{PayloadKG: &payload, Seats: &seats}},
		{Type: resource.TypeTrailer, Name: "Anhänger 1", IsExclusive: true, Capacity: resource.Capacity{PayloadKG: &payload}},
	}
}

type driverSeedAccount struct {
	username    string
	displayName string
	rules       []driver.RuleInput
	exception   *driver.ExceptionInput
}

func driverSeedAccounts() []driverSeedAccount {
	rule := func(weekday int, from string, to string) driver.RuleInput {
		return driver.RuleInput{Weekday: weekday, LocalStart: from, LocalEnd: to, ValidFrom: "2026-01-01", Status: driver.RuleAvailable}
	}
	berndVacation := driver.ExceptionInput{Type: driver.ExceptionVacation, IsAllDay: true, LocalDate: "2026-09-02"}
	dorisUnavailable := driver.ExceptionInput{Type: driver.ExceptionUnavailable, IsAllDay: true, LocalDate: "2026-08-28"}
	return []driverSeedAccount{
		{username: "anna", displayName: "Anna Fahrerin", rules: []driver.RuleInput{rule(1, "08:00", "17:00"), rule(2, "08:00", "17:00"), rule(4, "08:00", "17:00")}},
		{username: "bernd", displayName: "Bernd Fahrer", rules: []driver.RuleInput{rule(1, "07:00", "16:00"), rule(2, "07:00", "16:00"), rule(3, "07:00", "16:00"), rule(4, "07:00", "16:00"), rule(5, "07:00", "16:00")}, exception: &berndVacation},
		{username: "christian", displayName: "Christian Fahrer", rules: []driver.RuleInput{rule(2, "12:00", "18:00"), rule(4, "12:00", "18:00")}},
		{username: "doris", displayName: "Doris Fahrerin", exception: &dorisUnavailable},
		{username: "emil", displayName: "Emil Fahrer"},
	}
}

func seedCustomerScenarios(ctx context.Context, pool *pgxpool.Pool, actor auth.Actor, output io.Writer) error {
	service, err := app.CustomerService(pool)
	if err != nil {
		return err
	}
	scenarios := customerSeedScenarios()
	created := 0
	for _, scenario := range scenarios {
		name := strings.TrimSpace(scenario.Customer.FirstName + " " + scenario.Customer.LastName)
		page, listErr := service.ListCustomers(ctx, actor, name, 1)
		if listErr != nil {
			return listErr
		}
		if len(page.Items) > 0 {
			continue
		}
		if _, createErr := service.CreateIntake(ctx, actor, scenario, "seed-dev"); createErr != nil {
			return createErr
		}
		created++
	}
	_, _ = fmt.Fprintf(output, "%d Development-Kundenszenarien neu angelegt.\n", created)
	return nil
}

func customerSeedScenarios() []customers.IntakeInput {
	return []customers.IntakeInput{
		{
			Customer: customers.CustomerInput{FirstName: "Franz", LastName: "Huber", Street: "Unterneukirchen 15", Locality: "Unterneukirchen", Region: "Unterneukirchen", CountryCode: "AT", NotificationPreference: customers.NotifyNone},
			Job:      customers.JobInput{JobType: customers.JobTypeChippingWithTransport, VolumeM3: "80", EstimatedHackMinutes: 180, TransportMode: customers.TransportUndecided, PreferenceText: "Anfang September", Urgency: customers.UrgencyNormal, Region: "Unterneukirchen", Source: customers.SourcePhone},
		},
		{
			Customer: customers.CustomerInput{FirstName: "Maria", LastName: "Maier", Street: "Beispielweg 4", Locality: "Musterort", Region: "Musterort", CountryCode: "AT", NotificationPreference: customers.NotifyNone},
			Job:      customers.JobInput{JobType: customers.JobTypeChippingOnly, VolumeM3: "150", EstimatedHackMinutes: 360, TransportMode: customers.TransportNone, Urgency: customers.UrgencyUrgent, Region: "Musterort", Source: customers.SourcePhone},
		},
		{
			Customer: customers.CustomerInput{FirstName: "Johann", LastName: "Berger", Street: "Waldstraße 9", Locality: "Forsttal", Region: "Forsttal", CountryCode: "AT", NotificationPreference: customers.NotifyNone},
			Job:      customers.JobInput{JobType: customers.JobTypeChippingOnly, VolumeM3: "40", EstimatedHackMinutes: 120, TransportMode: customers.TransportNone, PreferenceText: "Oktober", Urgency: customers.UrgencyNormal, Region: "Forsttal", Source: customers.SourcePhone},
		},
	}
}

func runProcess(errorOutput io.Writer, logger *slog.Logger, operation func() error) int {
	if err := operation(); err != nil {
		logger.Error(
			"command failed",
			slog.String("error_code", "command_failed"),
			slog.String("error_type", fmt.Sprintf("%T", err)),
		)
		_, _ = fmt.Fprintln(errorOutput, "Befehl fehlgeschlagen. Details stehen im strukturierten Fehlerlog.")
		return ExitFailure
	}
	return ExitSuccess
}

func writeVersion(output io.Writer) {
	info := buildinfo.Current()
	_, _ = fmt.Fprintf(output, "hackwerk %s (commit %s, gebaut %s)\n", info.Version, info.Commit, info.BuildTime)
}

func writeHelp(output io.Writer) {
	_, _ = fmt.Fprintln(output, `HackWerk – Einsatzplanung für Hackaufträge

Verwendung:
  hackwerk serve                 Webdienst starten
  hackwerk worker                Hintergrundprozess starten
  hackwerk migrate up|down|status
  hackwerk seed-dev              Entwicklungsschema und Demodaten vorbereiten
  hackwerk admin --help          Benutzer-CLI
  hackwerk healthcheck           Readiness des Webdienstes prüfen
  hackwerk version               Buildversion anzeigen`)
}
