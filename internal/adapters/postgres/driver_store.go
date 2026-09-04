package postgres

import (
	"context"
	"errors"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/driver"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DriverStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

func NewDriverStore(pool *pgxpool.Pool) *DriverStore {
	return &DriverStore{pool: pool, queries: dbgen.New(pool)}
}

func (s *DriverStore) ListProfiles(ctx context.Context) ([]driver.Profile, error) {
	rows, err := s.queries.ListDriverProfiles(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]driver.Profile, 0, len(rows))
	for _, row := range rows {
		result = append(result, driver.Profile{
			ID: row.DID, UserID: row.UserID, Username: row.Username, DisplayName: row.DisplayName,
			Phone: row.Phone, Email: row.Email, IsActive: row.Active, CanCompleteJobs: row.CanCompleteJobs,
			InternalNote: row.InternalNote, IsPrimary: row.IsPrimary, AvailabilityPolicy: driver.AvailabilityPolicy(row.AvailabilityPolicy), Version: row.Version,
			CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return result, nil
}

func (s *DriverStore) CreateProfile(ctx context.Context, actor auth.Actor, input driver.ProfileInput, requestID string) (id string, resultErr error) {
	resultErr = withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if err := demotePrimaryDrivers(ctx, queries, actor, input.IsPrimary, "", requestID); err != nil {
			return err
		}
		var err error
		id, err = queries.InsertDriverProfile(ctx, dbgen.InsertDriverProfileParams{
			UserID: input.UserID, DisplayName: input.DisplayName, Phone: input.Phone, Email: input.Email,
			CanCompleteJobs: input.CanCompleteJobs, InternalNote: input.InternalNote,
			IsPrimary: input.IsPrimary, AvailabilityPolicy: string(input.AvailabilityPolicy),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return driver.ErrNotFound
		}
		if err != nil {
			return mapDriverConflict(err)
		}
		return insertAudit(ctx, queries, actor, "driver.created", "driver", id, requestID,
			[]string{"user_id", "display_name", "contact", "can_complete_jobs"})
	})
	return id, resultErr
}

func (s *DriverStore) UpdateProfile(ctx context.Context, actor auth.Actor, id string, version int32, input driver.ProfileInput, requestID string) error {
	driverID, err := uuid(id)
	if err != nil {
		return driver.ErrNotFound
	}
	return withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if err := demotePrimaryDrivers(ctx, queries, actor, input.IsPrimary, id, requestID); err != nil {
			return err
		}
		rows, updateErr := queries.UpdateDriverProfile(ctx, dbgen.UpdateDriverProfileParams{
			UserID: input.UserID, DisplayName: input.DisplayName, Phone: input.Phone, Email: input.Email,
			CanCompleteJobs: input.CanCompleteJobs, InternalNote: input.InternalNote,
			IsPrimary: input.IsPrimary, AvailabilityPolicy: string(input.AvailabilityPolicy),
			ID: driverID, ExpectedVersion: version,
		})
		if updateErr != nil {
			return mapDriverConflict(updateErr)
		}
		if rows == 0 {
			return driver.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "driver.updated", "driver", id, requestID,
			[]string{"user_id", "display_name", "contact", "can_complete_jobs", "internal_note"})
	})
}

func demotePrimaryDrivers(ctx context.Context, queries *dbgen.Queries, actor auth.Actor, newPrimary bool, excludedID string, requestID string) error {
	ids, err := queries.DemoteOtherPrimaryDrivers(ctx, dbgen.DemoteOtherPrimaryDriversParams{NewPrimary: newPrimary, ExcludedID: excludedID})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := insertAudit(ctx, queries, actor, "driver.updated", "driver", id, requestID, []string{"is_primary", "availability_policy"}); err != nil {
			return err
		}
	}
	return nil
}

func (s *DriverStore) DeactivateProfile(ctx context.Context, actor auth.Actor, id string, version int32, requestID string) error {
	driverID, err := uuid(id)
	if err != nil {
		return driver.ErrNotFound
	}
	return withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		current, getErr := queries.LockDriverProfile(ctx, driverID)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return driver.ErrNotFound
		}
		if getErr != nil {
			return getErr
		}
		if current.Version != version || !current.Active {
			return driver.ErrConflict
		}
		reserved, reservedErr := queries.HasActiveDriverReservations(ctx, driverID)
		if reservedErr != nil {
			return reservedErr
		}
		if reserved {
			return driver.ErrConflict
		}
		rows, updateErr := queries.DeactivateDriverProfile(ctx, dbgen.DeactivateDriverProfileParams{ID: driverID, ExpectedVersion: version})
		if updateErr != nil {
			return updateErr
		}
		if rows == 0 {
			return driver.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "driver.deactivated", "driver", id, requestID, []string{"active"})
	})
}

func (s *DriverStore) Schedule(ctx context.Context, id string) (driver.Availability, error) {
	driverID, err := uuid(id)
	if err != nil {
		return driver.Availability{}, driver.ErrNotFound
	}
	profile, err := s.profile(ctx, driverID)
	if err != nil {
		return driver.Availability{}, err
	}
	ruleRows, err := s.queries.ListAvailabilityRulesForDriver(ctx, driverID)
	if err != nil {
		return driver.Availability{}, err
	}
	exceptionRows, err := s.queries.ListAvailabilityExceptionsForDriver(ctx, driverID)
	if err != nil {
		return driver.Availability{}, err
	}
	rules, err := mapRules(ruleRows)
	if err != nil {
		return driver.Availability{}, err
	}
	return driver.Availability{Profile: profile, Rules: rules, Exceptions: mapExceptions(exceptionRows)}, nil
}

func (s *DriverStore) Availability(ctx context.Context, id string, fromUTC time.Time, toUTC time.Time, localFrom string, localTo string) (driver.Availability, error) {
	driverID, err := uuid(id)
	if err != nil {
		return driver.Availability{}, driver.ErrNotFound
	}
	profile, err := s.profile(ctx, driverID)
	if err != nil {
		return driver.Availability{}, err
	}
	fromDate, err := dateValue(localFrom)
	if err != nil {
		return driver.Availability{}, err
	}
	toDate, err := dateValue(localTo)
	if err != nil {
		return driver.Availability{}, err
	}
	ruleRows, err := s.queries.ListAvailabilityRulesInRange(ctx, dbgen.ListAvailabilityRulesInRangeParams{
		DriverID: driverID, LocalFrom: fromDate, LocalTo: toDate,
	})
	if err != nil {
		return driver.Availability{}, err
	}
	exceptionRows, err := s.queries.ListAvailabilityExceptionsInRange(ctx, dbgen.ListAvailabilityExceptionsInRangeParams{
		DriverID: driverID, LocalFrom: fromDate, LocalTo: toDate,
		FromUtc: timestamp(fromUTC), ToUtc: timestamp(toUTC),
	})
	if err != nil {
		return driver.Availability{}, err
	}
	rules := make([]driver.Rule, 0, len(ruleRows))
	for _, row := range ruleRows {
		rule, mapErr := ruleFromRangeRow(row)
		if mapErr != nil {
			return driver.Availability{}, mapErr
		}
		rules = append(rules, rule)
	}
	exceptions := make([]driver.Exception, 0, len(exceptionRows))
	for _, row := range exceptionRows {
		exceptions = append(exceptions, driver.Exception{
			ID: row.ID, DriverID: row.DriverID, Type: driver.ExceptionType(row.ExceptionType), IsAllDay: row.AllDay,
			LocalDate: row.LocalDate, StartsAt: optionalTimestamp(row.StartsAt), EndsAt: optionalTimestamp(row.EndsAt),
			InternalNote: row.InternalNote, Version: row.Version,
		})
	}
	return driver.Availability{Profile: profile, Rules: rules, Exceptions: exceptions}, nil
}

func (s *DriverStore) CreateRule(ctx context.Context, actor auth.Actor, driverID string, input driver.RuleInput, requestID string) (id string, resultErr error) {
	parsedDriverID, params, err := ruleInsertParams(driverID, input)
	if err != nil {
		return "", driver.ErrNotFound
	}
	params.DriverID = parsedDriverID
	resultErr = withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if err := lockDriverAvailability(ctx, queries, parsedDriverID); err != nil {
			return err
		}
		var insertErr error
		id, insertErr = queries.InsertAvailabilityRule(ctx, params)
		if insertErr != nil {
			return mapDriverConflict(insertErr)
		}
		return insertAudit(ctx, queries, actor, "availability_rule.created", "availability_rule", id, requestID,
			[]string{"driver_id", "weekday", "local_time", "valid_range", "status"})
	})
	return id, resultErr
}

func (s *DriverStore) UpdateRule(ctx context.Context, actor auth.Actor, driverID string, id string, version int32, input driver.RuleInput, requestID string) error {
	parsedDriverID, insertParams, err := ruleInsertParams(driverID, input)
	if err != nil {
		return driver.ErrNotFound
	}
	ruleID, err := uuid(id)
	if err != nil {
		return driver.ErrNotFound
	}
	return withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if err := lockDriverAvailability(ctx, queries, parsedDriverID); err != nil {
			return err
		}
		rows, updateErr := queries.UpdateAvailabilityRule(ctx, dbgen.UpdateAvailabilityRuleParams{
			IsoWeekday: insertParams.IsoWeekday, LocalStart: insertParams.LocalStart, LocalEnd: insertParams.LocalEnd,
			ValidFrom: insertParams.ValidFrom, ValidUntil: insertParams.ValidUntil,
			Status: insertParams.Status, InternalNote: insertParams.InternalNote,
			ID: ruleID, DriverID: parsedDriverID, ExpectedVersion: version,
		})
		if updateErr != nil {
			return mapDriverConflict(updateErr)
		}
		if rows == 0 {
			return driver.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "availability_rule.updated", "availability_rule", id, requestID,
			[]string{"weekday", "local_time", "valid_range", "status", "internal_note"})
	})
}

func (s *DriverStore) DeleteRule(ctx context.Context, actor auth.Actor, driverID string, id string, version int32, requestID string) error {
	return s.deleteAvailability(ctx, actor, driverID, id, requestID, "availability_rule.deleted", func(queries *dbgen.Queries, targetID pgtype.UUID, targetDriverID pgtype.UUID) (int64, error) {
		return queries.DeleteAvailabilityRule(ctx, dbgen.DeleteAvailabilityRuleParams{ID: targetID, DriverID: targetDriverID, ExpectedVersion: version})
	})
}

func (s *DriverStore) ClearRulesForDay(ctx context.Context, actor auth.Actor, driverID string, weekday int, refs []driver.RuleRef, requestID string) error {
	if weekday < 1 || weekday > 7 {
		return driver.ErrValidation
	}
	parsedDriverID, err := uuid(driverID)
	if err != nil {
		return driver.ErrNotFound
	}
	expected := make(map[string]int32, len(refs))
	for _, ref := range refs {
		expected[ref.ID] = ref.Version
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := dbgen.New(tx)
		if err := lockDriverAvailability(ctx, queries, parsedDriverID); err != nil {
			return err
		}
		// #nosec G115 -- weekday is explicitly bounded to 1..7 above.
		isoWeekday := int16(weekday)
		rows, err := queries.LockAvailabilityRulesForDay(ctx, dbgen.LockAvailabilityRulesForDayParams{
			DriverID: parsedDriverID, IsoWeekday: isoWeekday,
		})
		if err != nil {
			return err
		}
		actual := make(map[string]int32, len(expected))
		for _, row := range rows {
			actual[row.ID] = row.Version
		}
		if len(actual) != len(expected) {
			return driver.ErrConflict
		}
		for id, version := range expected {
			if actual[id] != version {
				return driver.ErrConflict
			}
		}
		count, err := queries.ClearAvailabilityRulesForDay(ctx, dbgen.ClearAvailabilityRulesForDayParams{
			DriverID: parsedDriverID, IsoWeekday: isoWeekday,
		})
		if err != nil {
			return mapDriverConflict(err)
		}
		if count != int64(len(expected)) {
			return driver.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "availability_rule.day_cleared", "driver", driverID, requestID,
			[]string{"weekday", "rules"})
	})
}

func (s *DriverStore) CreateException(ctx context.Context, actor auth.Actor, driverID string, input driver.ExceptionInput, requestID string) (id string, resultErr error) {
	parsedDriverID, err := uuid(driverID)
	if err != nil {
		return "", driver.ErrNotFound
	}
	params := exceptionInsertParams(parsedDriverID, input)
	resultErr = withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if err := lockDriverAvailability(ctx, queries, parsedDriverID); err != nil {
			return err
		}
		var insertErr error
		id, insertErr = queries.InsertAvailabilityException(ctx, params)
		if insertErr != nil {
			return mapDriverConflict(insertErr)
		}
		return insertAudit(ctx, queries, actor, "availability_exception.created", "availability_exception", id, requestID,
			[]string{"driver_id", "exception_type", "time_range"})
	})
	return id, resultErr
}

func (s *DriverStore) CreateExceptions(ctx context.Context, actor auth.Actor, driverID string, inputs []driver.ExceptionInput, requestID string) error {
	parsedDriverID, err := uuid(driverID)
	if err != nil {
		return driver.ErrNotFound
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		queries := dbgen.New(tx)
		if err := lockDriverAvailability(ctx, queries, parsedDriverID); err != nil {
			return err
		}
		for _, input := range inputs {
			if _, err := queries.InsertAvailabilityException(ctx, exceptionInsertParams(parsedDriverID, input)); err != nil {
				return mapDriverConflict(err)
			}
		}
		return insertAudit(ctx, queries, actor, "availability_exception.preset_created", "driver", driverID, requestID,
			[]string{"exception_type", "local_dates"})
	})
}

func (s *DriverStore) UpdateException(ctx context.Context, actor auth.Actor, driverID string, id string, version int32, input driver.ExceptionInput, requestID string) error {
	parsedDriverID, err := uuid(driverID)
	if err != nil {
		return driver.ErrNotFound
	}
	exceptionID, err := uuid(id)
	if err != nil {
		return driver.ErrNotFound
	}
	params := exceptionInsertParams(parsedDriverID, input)
	return withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if err := lockDriverAvailability(ctx, queries, parsedDriverID); err != nil {
			return err
		}
		rows, updateErr := queries.UpdateAvailabilityException(ctx, dbgen.UpdateAvailabilityExceptionParams{
			ExceptionType: params.ExceptionType, AllDay: params.AllDay, LocalDate: params.LocalDate,
			StartsAt: params.StartsAt, EndsAt: params.EndsAt, InternalNote: params.InternalNote,
			ID: exceptionID, DriverID: parsedDriverID, ExpectedVersion: version,
		})
		if updateErr != nil {
			return mapDriverConflict(updateErr)
		}
		if rows == 0 {
			return driver.ErrConflict
		}
		return insertAudit(ctx, queries, actor, "availability_exception.updated", "availability_exception", id, requestID,
			[]string{"exception_type", "time_range", "internal_note"})
	})
}

func (s *DriverStore) DeleteException(ctx context.Context, actor auth.Actor, driverID string, id string, version int32, requestID string) error {
	return s.deleteAvailability(ctx, actor, driverID, id, requestID, "availability_exception.deleted", func(queries *dbgen.Queries, targetID pgtype.UUID, targetDriverID pgtype.UUID) (int64, error) {
		return queries.DeleteAvailabilityException(ctx, dbgen.DeleteAvailabilityExceptionParams{ID: targetID, DriverID: targetDriverID, ExpectedVersion: version})
	})
}

func (s *DriverStore) deleteAvailability(ctx context.Context, actor auth.Actor, driverID string, id string, requestID string, action string, operation func(*dbgen.Queries, pgtype.UUID, pgtype.UUID) (int64, error)) error {
	parsedDriverID, err := uuid(driverID)
	if err != nil {
		return driver.ErrNotFound
	}
	targetID, err := uuid(id)
	if err != nil {
		return driver.ErrNotFound
	}
	return withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if err := lockDriverAvailability(ctx, queries, parsedDriverID); err != nil {
			return err
		}
		rows, deleteErr := operation(queries, targetID, parsedDriverID)
		if deleteErr != nil {
			return deleteErr
		}
		if rows == 0 {
			return driver.ErrConflict
		}
		return insertAudit(ctx, queries, actor, action, "availability", id, requestID, []string{"deleted"})
	})
}

func lockDriverAvailability(ctx context.Context, queries *dbgen.Queries, driverID pgtype.UUID) error {
	_, err := queries.LockDriverForAvailability(ctx, driverID)
	if errors.Is(err, pgx.ErrNoRows) {
		return driver.ErrNotFound
	}
	return err
}

func (s *DriverStore) profile(ctx context.Context, id pgtype.UUID) (driver.Profile, error) {
	row, err := s.queries.GetDriverProfile(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return driver.Profile{}, driver.ErrNotFound
	}
	if err != nil {
		return driver.Profile{}, err
	}
	return driver.Profile{
		ID: row.DID, UserID: row.UserID, Username: row.Username, DisplayName: row.DisplayName,
		Phone: row.Phone, Email: row.Email, IsActive: row.Active, CanCompleteJobs: row.CanCompleteJobs,
		InternalNote: row.InternalNote, IsPrimary: row.IsPrimary, AvailabilityPolicy: driver.AvailabilityPolicy(row.AvailabilityPolicy), Version: row.Version,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

func ruleInsertParams(driverID string, input driver.RuleInput) (pgtype.UUID, dbgen.InsertAvailabilityRuleParams, error) {
	if input.Weekday < 1 || input.Weekday > 7 {
		return pgtype.UUID{}, dbgen.InsertAvailabilityRuleParams{}, driver.ErrValidation
	}
	parsedDriverID, err := uuid(driverID)
	if err != nil {
		return pgtype.UUID{}, dbgen.InsertAvailabilityRuleParams{}, err
	}
	start, err := timeValue(input.LocalStart)
	if err != nil {
		return pgtype.UUID{}, dbgen.InsertAvailabilityRuleParams{}, err
	}
	end, err := timeValue(input.LocalEnd)
	if err != nil {
		return pgtype.UUID{}, dbgen.InsertAvailabilityRuleParams{}, err
	}
	from, err := dateValue(input.ValidFrom)
	if err != nil {
		return pgtype.UUID{}, dbgen.InsertAvailabilityRuleParams{}, err
	}
	// #nosec G115 -- input.Weekday is explicitly bounded to 1..7 above.
	isoWeekday := int16(input.Weekday)
	return parsedDriverID, dbgen.InsertAvailabilityRuleParams{
		DriverID: parsedDriverID, IsoWeekday: isoWeekday, LocalStart: start, LocalEnd: end,
		ValidFrom: from, ValidUntil: input.ValidUntil, Status: string(input.Status), InternalNote: input.InternalNote,
	}, nil
}

func exceptionInsertParams(driverID pgtype.UUID, input driver.ExceptionInput) dbgen.InsertAvailabilityExceptionParams {
	startsAt := ""
	endsAt := ""
	if !input.IsAllDay {
		startsAt = input.StartsAt.Format(time.RFC3339Nano)
		endsAt = input.EndsAt.Format(time.RFC3339Nano)
	}
	return dbgen.InsertAvailabilityExceptionParams{
		DriverID: driverID, ExceptionType: string(input.Type), AllDay: input.IsAllDay,
		LocalDate: input.LocalDate, StartsAt: startsAt, EndsAt: endsAt, InternalNote: input.InternalNote,
	}
}

func mapRules(rows []dbgen.ListAvailabilityRulesForDriverRow) ([]driver.Rule, error) {
	result := make([]driver.Rule, 0, len(rows))
	for _, row := range rows {
		start, err := driver.ParseLocalTime(row.LocalStart)
		if err != nil {
			return nil, err
		}
		end, err := driver.ParseLocalTime(row.LocalEnd)
		if err != nil {
			return nil, err
		}
		result = append(result, driver.Rule{
			ID: row.ID, DriverID: row.DriverID, Weekday: int(row.IsoWeekday), StartMinute: start, EndMinute: end,
			ValidFrom: row.ValidFrom, ValidUntil: row.ValidUntil, Status: driver.RuleStatus(row.Status),
			InternalNote: row.InternalNote, Version: row.Version,
		})
	}
	return result, nil
}

func ruleFromRangeRow(row dbgen.ListAvailabilityRulesInRangeRow) (driver.Rule, error) {
	start, err := driver.ParseLocalTime(row.LocalStart)
	if err != nil {
		return driver.Rule{}, err
	}
	end, err := driver.ParseLocalTime(row.LocalEnd)
	if err != nil {
		return driver.Rule{}, err
	}
	return driver.Rule{
		ID: row.ID, DriverID: row.DriverID, Weekday: int(row.IsoWeekday), StartMinute: start, EndMinute: end,
		ValidFrom: row.ValidFrom, ValidUntil: row.ValidUntil, Status: driver.RuleStatus(row.Status),
		InternalNote: row.InternalNote, Version: row.Version,
	}, nil
}

func mapExceptions(rows []dbgen.ListAvailabilityExceptionsForDriverRow) []driver.Exception {
	result := make([]driver.Exception, 0, len(rows))
	for _, row := range rows {
		result = append(result, driver.Exception{
			ID: row.ID, DriverID: row.DriverID, Type: driver.ExceptionType(row.ExceptionType), IsAllDay: row.AllDay,
			LocalDate: row.LocalDate, StartsAt: optionalTimestamp(row.StartsAt), EndsAt: optionalTimestamp(row.EndsAt),
			InternalNote: row.InternalNote, Version: row.Version,
		})
	}
	return result
}

func timeValue(value string) (pgtype.Time, error) {
	minutes, err := driver.ParseLocalTime(value)
	if err != nil {
		return pgtype.Time{}, err
	}
	return pgtype.Time{Microseconds: int64(minutes) * int64(time.Minute/time.Microsecond), Valid: true}, nil
}

func dateValue(value string) (pgtype.Date, error) {
	parsed, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return pgtype.Date{}, err
	}
	return pgtype.Date{Time: parsed, Valid: true}, nil
}

func optionalTimestamp(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func mapDriverConflict(err error) error {
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) {
		switch postgresErr.Code {
		case "23505", "23P01":
			return driver.ErrConflict
		case "23503", "23514", "22P02":
			return driver.ErrValidation
		}
	}
	return err
}
