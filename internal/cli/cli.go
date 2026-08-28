// Package cli implements HackWerk's stable command-line contract.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
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
	"example.invalid/hackplan/internal/notification"
	"example.invalid/hackplan/internal/planning"
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
	if len(arguments) == 2 {
		// #nosec G602 -- the slice length is checked immediately above.
		if isHelp(arguments[1]) {
			if writeCommandHelp(streams.Output, arguments[0]) {
				return ExitSuccess
			}
		}
	}
	if arguments[0] == "version" {
		if len(arguments) != 1 {
			_, _ = fmt.Fprintln(streams.Error, "Verwendung: hackwerk version")
			return ExitUsage
		}
		writeVersion(streams.Output)
		return ExitSuccess
	}
	if arguments[0] == "schema-version" {
		if len(arguments) != 1 {
			_, _ = fmt.Fprintln(streams.Error, "Verwendung: hackwerk schema-version")
			return ExitUsage
		}
		_, _ = fmt.Fprintln(streams.Output, config.CurrentSchemaVersion)
		return ExitSuccess
	}
	if !knownConfiguredCommand(arguments[0]) {
		_, _ = fmt.Fprintf(streams.Error, "Unbekannter Befehl %q.\n", arguments[0])
		writeHelp(streams.Error)
		return ExitUsage
	}

	configuredCommand := arguments[0]
	if configuredCommand == "config-check" {
		var ok bool
		configuredCommand, ok = configCheckTarget(arguments[1:])
		if !ok {
			return usage(streams.Error, "Verwendung: hackwerk config-check [serve|worker]")
		}
	}
	cfg, err := config.LoadForCommand(configuredCommand)
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
		if len(arguments) > 2 {
			return usage(streams.Error, "Verwendung: hackwerk healthcheck [worker]")
		}
		if len(arguments) == 2 {
			// #nosec G602 -- the slice length is checked immediately above.
			if arguments[1] != "worker" {
				return usage(streams.Error, "Verwendung: hackwerk healthcheck [worker]")
			}
			return runProcess(streams.Error, logger, func() error { return app.WorkerHealthcheck(ctx, cfg) })
		}
		return runProcess(streams.Error, logger, func() error {
			return web.Healthcheck(ctx, cfg.ListenAddr, strings.TrimRight(cfg.BaseURL, "/"), 5*time.Second)
		})
	case "config-check":
		encoder := json.NewEncoder(streams.Output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(cfg.Diagnostic()); err != nil {
			return ExitFailure
		}
		return ExitSuccess
	}
	return ExitUsage
}

func knownConfiguredCommand(command string) bool {
	switch command {
	case "serve", "worker", "migrate", "seed-dev", "admin", "healthcheck", "config-check":
		return true
	default:
		return false
	}
}

func configCheckTarget(arguments []string) (string, bool) {
	if len(arguments) == 0 {
		return "serve", true
	}
	if len(arguments) == 1 && (arguments[0] == "serve" || arguments[0] == "worker") {
		return arguments[0], true
	}
	return "", false
}

func isHelp(argument string) bool {
	return argument == "help" || argument == "--help" || argument == "-h"
}

func writeCommandHelp(output io.Writer, command string) bool {
	help := map[string]string{
		"serve":          "Verwendung: hackwerk serve\nStartet den HTTP-Webdienst.",
		"worker":         "Verwendung: hackwerk worker\nStartet Hintergrundprozesse.",
		"migrate":        "Verwendung: hackwerk migrate up|down|status\nVerwaltet das Datenbankschema.",
		"seed-dev":       "Verwendung: hackwerk seed-dev\nErzeugt ausschließlich lokale Entwicklungsdaten.",
		"admin":          adminHelp,
		"healthcheck":    "Verwendung: hackwerk healthcheck [worker]\nPrüft lokal die Web-Readiness oder direkt Datenbank, Schema und Worker-Heartbeat.",
		"config-check":   "Verwendung: hackwerk config-check [serve|worker]\nValidiert standardmäßig die Web- oder ausdrücklich die Worker-Konfiguration und zeigt nur redigierte Diagnosedaten.",
		"schema-version": "Verwendung: hackwerk schema-version\nGibt die vom Binary erwartete Schemaversion aus.",
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
	return runProcess(streams.Error, logger, func() error { return SeedDevelopment(ctx, cfg, streams.Output) })
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
		case "disable-user":
			return adminDisableUser(ctx, identity, actor, arguments[1:], streams)
		default:
			return fmt.Errorf("admin: unknown subcommand %q", arguments[0])
		}
	})
}

const adminHelp = `Verwendung:
  hackwerk admin create --username NAME --display-name NAME [--role admin|driver] [--email MAIL] [--driver] [--password-file DATEI]
  hackwerk admin reset-password --username NAME [--password-file DATEI]
  hackwerk admin disable-user --username NAME
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

func adminDisableUser(ctx context.Context, identity *auth.Service, actor auth.Actor, arguments []string, streams IO) error {
	flags := flag.NewFlagSet("admin disable-user", flag.ContinueOnError)
	flags.SetOutput(streams.Error)
	username := flags.String("username", "", "Benutzername")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*username) == "" {
		return errors.New("admin: username is required")
	}
	user, err := identity.FindUserForAdministration(ctx, actor, *username)
	if err != nil {
		return err
	}
	return identity.UpdateUserAccess(ctx, actor, auth.UpdateAccessInput{UserID: user.ID, Role: user.Role, Active: false, ExpectedVersion: user.Version, RequestID: "admin-cli"})
}

func readPassword(input io.Reader, passwordFile string) (string, error) {
	if passwordFile != "" {
		// #nosec G304 -- an administrator explicitly selects this local password file.
		file, err := os.Open(passwordFile)
		if err != nil {
			return "", fmt.Errorf("admin: reading password file: %w", err)
		}
		content, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil {
			return "", fmt.Errorf("admin: reading password file: %w", readErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("admin: closing password file: %w", closeErr)
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

// SeedDevelopment creates or reconciles deterministic, synthetic demo data.
// It never runs implicitly and is rejected by the CLI in production.
func SeedDevelopment(ctx context.Context, cfg config.Config, output io.Writer) error {
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
	adminUser, err := identity.FindUserForAdministration(ctx, actor, "admin")
	if err != nil {
		return err
	}
	actor.UserID = adminUser.ID
	actor.DisplayName = adminUser.DisplayName
	if err := seedCustomerScenarios(ctx, pool, actor, output); err != nil {
		return err
	}
	if err := seedOperations(ctx, pool, identity, actor, output); err != nil {
		return err
	}
	if err := seedCalendar(ctx, cfg, pool, actor, output); err != nil {
		return err
	}
	return nil
}

func seedCalendar(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, actor auth.Actor, output io.Writer) error {
	var driverID, chipperID, transportID string
	if err := pool.QueryRow(ctx, "SELECT id::text FROM drivers WHERE display_name='Anna Fahrerin' AND active").Scan(&driverID); err != nil {
		return fmt.Errorf("seed: finding calendar driver: %w", err)
	}
	if err := pool.QueryRow(ctx, "SELECT id::text FROM resources WHERE name='Hackmaschine 1' AND active").Scan(&chipperID); err != nil {
		return fmt.Errorf("seed: finding calendar resource: %w", err)
	}
	if err := pool.QueryRow(ctx, "SELECT id::text FROM resources WHERE name='Transporter 1' AND active").Scan(&transportID); err != nil {
		return fmt.Errorf("seed: finding transport resource: %w", err)
	}
	driverService, err := app.DriverService(pool)
	if err != nil {
		return err
	}
	demoConfig := cfg
	demoConfig.Mail.Enabled = true
	if demoConfig.Mail.MaxAttempts < 1 {
		demoConfig.Mail.MaxAttempts = 1
	}
	service, err := app.AppointmentService(demoConfig, pool, driverService)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		return err
	}
	now := time.Now().In(location)
	scenarios := []struct {
		lastName  string
		weekday   time.Weekday
		weekShift int
		state     string
		transport bool
	}{
		{lastName: "Maier", weekday: time.Thursday, state: "proposal"},
		{lastName: "Huber", weekday: time.Monday, state: "pending", transport: true},
		{lastName: "Berger", weekday: time.Tuesday, state: "confirmed"},
		{lastName: "Waldner", weekday: time.Thursday, weekShift: 1, state: "declined"},
		{lastName: "Gruber", weekday: time.Monday, weekShift: 2, state: "failed"},
		{lastName: "Forster", weekday: time.Monday, state: "completed"},
	}
	created := 0
	for _, scenario := range scenarios {
		startsAt := nextSeedStart(now, scenario.weekday, scenario.weekShift)
		if scenario.state == "completed" {
			startsAt = previousSeedStart(now, time.Monday)
		}
		wasCreated, seedErr := seedAppointmentScenario(ctx, pool, service, demoConfig, actor, scenario.lastName, scenario.state, startsAt, driverID, chipperID, transportID, scenario.transport)
		if seedErr != nil {
			return seedErr
		}
		if wasCreated {
			created++
		}
	}
	_, _ = fmt.Fprintf(output, "%d Development-Termine neu angelegt; Proposal/offen/bestätigt/abgelehnt/erledigt/Versandfehler geprüft.\n", created)
	return nil
}

func nextSeedStart(now time.Time, weekday time.Weekday, weekShift int) time.Time {
	days := (int(weekday) - int(now.Weekday()) + 7) % 7
	if days == 0 {
		days = 7
	}
	day := now.AddDate(0, 0, days+7*weekShift)
	return time.Date(day.Year(), day.Month(), day.Day(), 8, 0, 0, 0, now.Location()).UTC()
}

func previousSeedStart(now time.Time, weekday time.Weekday) time.Time {
	days := (int(now.Weekday()) - int(weekday) + 7) % 7
	if days == 0 {
		days = 7
	}
	day := now.AddDate(0, 0, -days)
	return time.Date(day.Year(), day.Month(), day.Day(), 8, 0, 0, 0, now.Location()).UTC()
}

func seedAppointmentScenario(
	ctx context.Context,
	pool *pgxpool.Pool,
	service *appointment.Service,
	cfg config.Config,
	actor auth.Actor,
	lastName, state string,
	startsAt time.Time,
	driverID, chipperID, transportID string,
	withTransport bool,
) (bool, error) {
	var jobID string
	var durationMinutes int
	if err := pool.QueryRow(ctx, `SELECT j.id::text, j.estimated_hack_minutes + j.estimated_transport_minutes
		FROM jobs j JOIN customers c ON c.id=j.customer_id
		WHERE c.last_name=$1 ORDER BY j.created_at LIMIT 1`, lastName).Scan(&jobID, &durationMinutes); err != nil {
		return false, fmt.Errorf("seed: finding %s calendar job: %w", lastName, err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM appointments WHERE job_id=$1)", jobID).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	endsAt := startsAt.Add(time.Duration(durationMinutes) * time.Minute)
	draft, err := service.CreateDraftFromWaitlist(ctx, actor, appointment.CreateDraftInput{
		JobID: jobID, RequestID: "seed-dev", Time: appointment.TimeInput{StartsAt: startsAt, EndsAt: endsAt},
	})
	if err != nil {
		return false, err
	}
	resources := []appointment.ResourceAssignment{{ID: chipperID, Purpose: appointment.PurposeChipping}}
	if withTransport {
		resources = append(resources, appointment.ResourceAssignment{ID: transportID, Purpose: appointment.PurposeTransport})
	}
	assigned, err := service.AssignDriversAndResources(ctx, actor, appointment.AssignInput{
		MutateInput: appointment.MutateInput{ID: draft.ID, ExpectedVersion: draft.Version, RequestID: "seed-dev"},
		Assignments: appointment.AssignmentInput{
			DriverIDs: []string{driverID}, PrimaryDriverID: driverID, Resources: resources,
		},
	})
	if err != nil {
		return false, err
	}
	proposed, err := service.ProposeAppointment(ctx, actor, appointment.MutateInput{ID: assigned.ID, ExpectedVersion: assigned.Version, RequestID: "seed-dev"}, "")
	if err != nil {
		return false, err
	}
	if state == "proposal" {
		return true, nil
	}
	fixed, err := service.FixAppointment(ctx, actor, appointment.FixInput{MutateInput: appointment.MutateInput{ID: proposed.ID, ExpectedVersion: proposed.Version, RequestID: "seed-dev"}})
	if err != nil {
		return false, err
	}
	providerErr := error(nil)
	if state == "failed" {
		providerErr = fmt.Errorf("%w: synthetic development failure", notification.ErrPermanent)
	}
	if err := runSeedNotification(ctx, pool, cfg, providerErr); err != nil {
		return false, err
	}
	if state == "confirmed" || state == "declined" {
		response := notification.ResponseConfirmed
		if state == "declined" {
			response = notification.ResponseDeclined
		}
		if err := respondSeedConfirmation(ctx, pool, cfg, fixed.ID, response); err != nil {
			return false, err
		}
	}
	if state == "completed" {
		if _, err := service.CompleteAppointment(ctx, actor, appointment.CompleteInput{MutateInput: appointment.MutateInput{ID: fixed.ID, ExpectedVersion: fixed.Version, RequestID: "seed-dev"}}); err != nil {
			return false, err
		}
	}
	return true, nil
}

func runSeedNotification(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, providerErr error) error {
	tokens, err := notification.NewKeyRing(cfg.Confirmation.TokenKeys, cfg.Confirmation.CurrentKeyID)
	if err != nil {
		return err
	}
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return err
	}
	processor, err := notification.NewProcessor(
		postgres.NewNotificationWorkerStore(pool),
		map[notification.Channel]notification.Provider{notification.ChannelEmail: notification.NewFakeProvider(providerErr)},
		tokens,
		location,
		notification.ProcessorConfig{
			BaseURL: cfg.BaseURL, BusinessName: cfg.Business.Name, BusinessAddress: cfg.Business.Address,
			BusinessPhone: cfg.Business.Phone, WorkerID: "seed-dev", Lease: time.Minute, BatchSize: 20,
		},
		time.Now,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		return err
	}
	_, err = processor.RunOnce(ctx)
	return err
}

func respondSeedConfirmation(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, appointmentID string, response notification.Response) error {
	var requestID, keyID string
	var tokenVersion int32
	if err := pool.QueryRow(ctx, `SELECT id::text, token_key_id, token_version FROM confirmation_requests
		WHERE appointment_id=$1 AND status='active'`, appointmentID).Scan(&requestID, &keyID, &tokenVersion); err != nil {
		return fmt.Errorf("seed: finding confirmation request: %w", err)
	}
	tokens, err := notification.NewKeyRing(cfg.Confirmation.TokenKeys, cfg.Confirmation.CurrentKeyID)
	if err != nil {
		return err
	}
	material, err := tokens.Reconstruct(keyID, requestID, appointmentID, tokenVersion)
	if err != nil {
		return err
	}
	confirmationService, err := notification.NewConfirmationService(postgres.NewNotificationStore(pool), tokens, time.Now)
	if err != nil {
		return err
	}
	view, err := confirmationService.View(ctx, material.Raw)
	if err != nil {
		return err
	}
	_, err = confirmationService.Respond(ctx, material.Raw, view.FormNonce, response, "seed-dev")
	return err
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
		page, listErr := service.ListCustomers(ctx, actor, customers.CustomerListFilter{
			Search: name, Sort: "recent", Direction: "desc", Page: 1, PageSize: 25,
		})
		if listErr != nil {
			return listErr
		}
		if len(page.Items) > 0 {
			detail, detailErr := service.CustomerDetail(ctx, actor, page.Items[0].ID)
			if detailErr != nil {
				return detailErr
			}
			if detail.Customer.Email != scenario.Customer.Email || detail.Customer.NotificationPreference != scenario.Customer.NotificationPreference {
				current := detail.Customer
				if updateErr := service.UpdateCustomer(ctx, actor, customers.UpdateCustomerInput{
					ID: current.ID, ExpectedVersion: current.Version, RequestID: "seed-dev",
					Customer: customers.CustomerInput{
						FirstName: current.FirstName, LastName: current.LastName, CompanyName: current.CompanyName,
						Street: current.Street, PostalCode: current.PostalCode, Locality: current.Locality, Region: current.Region,
						CountryCode: current.CountryCode, AddressFreeform: current.AddressFreeform, PhoneRaw: current.PhoneRaw,
						Email: scenario.Customer.Email, NotificationPreference: scenario.Customer.NotificationPreference,
						Latitude: current.Latitude, Longitude: current.Longitude,
					},
				}); updateErr != nil {
					return updateErr
				}
			}
			if len(detail.Jobs) > 0 && scenario.Job.PileLatitude != nil &&
				(detail.Jobs[0].PileLatitude == nil || detail.Jobs[0].TransportMode != scenario.Job.TransportMode) {
				job := detail.Jobs[0]
				if updateErr := service.UpdateJob(ctx, actor, customers.UpdateJobInput{
					ID: job.ID, ExpectedVersion: job.Version, RequestID: "seed-dev", Job: scenario.Job,
				}); updateErr != nil {
					return updateErr
				}
			}
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
	scenarios := []customers.IntakeInput{
		{
			Customer: customers.CustomerInput{FirstName: "Franz", LastName: "Huber", Street: "Unterneukirchen 15", Locality: "Unterneukirchen", Region: "Unterneukirchen", CountryCode: "AT", Email: "franz.huber@example.test", NotificationPreference: customers.NotifyEmail},
			Job:      customers.JobInput{JobType: customers.JobTypeChippingWithTransport, VolumeM3: "80", EstimatedHackMinutes: 180, EstimatedTransportMinutes: 60, TransportTripCount: 1, TransportMode: customers.TransportInternal, PreferenceText: "Anfang September", Urgency: customers.UrgencyNormal, Region: "Unterneukirchen", Source: customers.SourcePhone},
		},
		{
			Customer: customers.CustomerInput{FirstName: "Maria", LastName: "Maier", Street: "Beispielweg 4", Locality: "Musterort", Region: "Musterort", CountryCode: "AT", Email: "maria.maier@example.test", NotificationPreference: customers.NotifyEmail},
			Job:      customers.JobInput{JobType: customers.JobTypeChippingOnly, VolumeM3: "150", EstimatedHackMinutes: 360, TransportMode: customers.TransportNone, Urgency: customers.UrgencyUrgent, Region: "Musterort", Source: customers.SourcePhone},
		},
		{
			Customer: customers.CustomerInput{FirstName: "Johann", LastName: "Berger", Street: "Waldstraße 9", Locality: "Forsttal", Region: "Forsttal", CountryCode: "AT", Email: "johann.berger@example.test", NotificationPreference: customers.NotifyEmail},
			Job:      customers.JobInput{JobType: customers.JobTypeChippingOnly, VolumeM3: "40", EstimatedHackMinutes: 120, TransportMode: customers.TransportNone, PreferenceText: "Oktober", Urgency: customers.UrgencyNormal, Region: "Forsttal", Source: customers.SourcePhone},
		},
		{
			Customer: customers.CustomerInput{FirstName: "Klara", LastName: "Waldner", Street: "Waldrand 7", Locality: "Forsttal", Region: "Forsttal", CountryCode: "AT", Email: "klara.waldner@example.test", NotificationPreference: customers.NotifyEmail},
			Job:      customers.JobInput{JobType: customers.JobTypeChippingOnly, VolumeM3: "65", EstimatedHackMinutes: 150, TransportMode: customers.TransportNone, Urgency: customers.UrgencyHigh, Region: "Forsttal", Source: customers.SourcePhone},
		},
		{
			Customer: customers.CustomerInput{FirstName: "Peter", LastName: "Forster", Street: "Höhenweg 3", Locality: "Musterort", Region: "Musterort", CountryCode: "AT", Email: "peter.forster@example.test", NotificationPreference: customers.NotifyEmail},
			Job:      customers.JobInput{JobType: customers.JobTypeChippingOnly, VolumeM3: "95", EstimatedHackMinutes: 210, TransportMode: customers.TransportNone, Urgency: customers.UrgencyNormal, Region: "Musterort", Source: customers.SourceInPerson},
		},
		{
			Customer: customers.CustomerInput{FirstName: "Eva", LastName: "Gruber", Street: "Fichtenstraße 12", Locality: "Unterneukirchen", Region: "Unterneukirchen", CountryCode: "AT", Email: "eva.gruber@example.test", NotificationPreference: customers.NotifyEmail},
			Job:      customers.JobInput{JobType: customers.JobTypeChippingOnly, VolumeM3: "55", EstimatedHackMinutes: 135, TransportMode: customers.TransportNone, Urgency: customers.UrgencyNormal, Region: "Unterneukirchen", Source: customers.SourceEmail},
		},
		{
			Customer: customers.CustomerInput{FirstName: "Demo", LastName: "OhneKoordinaten", AddressFreeform: "Zufahrt wird vor Termin telefonisch geklärt", CountryCode: "AT", Email: "demo.ohne-koordinaten@example.test", NotificationPreference: customers.NotifyEmail},
			Job:      customers.JobInput{JobType: customers.JobTypeChippingOnly, VolumeM3: "60", EstimatedHackMinutes: 150, TransportMode: customers.TransportNone, Urgency: customers.UrgencyLow, Region: "Unbekannt", Source: customers.SourceOther},
		},
	}
	locations := []planning.Point{
		{Latitude: 48.1711, Longitude: 12.8260},
		{Latitude: 48.2075, Longitude: 12.8754},
		{Latitude: 48.1364, Longitude: 12.7641},
		{Latitude: 48.1518, Longitude: 12.8012},
		{Latitude: 48.2267, Longitude: 12.9078},
		{Latitude: 48.1893, Longitude: 12.8426},
	}
	for index, location := range locations {
		latitude, longitude := location.Latitude, location.Longitude
		scenarios[index].Job.PileLatitude = &latitude
		scenarios[index].Job.PileLongitude = &longitude
		scenarios[index].Job.PileLocationSource = customers.PileSourceCoordinates
	}
	return scenarios
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
  hackwerk healthcheck [worker]  Web- oder Worker-Readiness prüfen
  hackwerk config-check [serve|worker]
                                Web- oder Worker-Konfiguration redigiert diagnostizieren
  hackwerk schema-version        Erwartete Schemaversion ausgeben
  hackwerk version               Buildversion anzeigen`)
}
