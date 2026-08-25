package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/resource"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AppointmentStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

func NewAppointmentStore(pool *pgxpool.Pool) *AppointmentStore {
	return &AppointmentStore{pool: pool, queries: dbgen.New(pool)}
}

func (s *AppointmentStore) CreateDraft(ctx context.Context, actor auth.Actor, input appointment.CreateDraftInput) (appointment.Appointment, error) {
	jobID, err := uuid(input.JobID)
	if err != nil {
		return appointment.Appointment{}, appointment.ErrNotFound
	}
	var id string
	err = withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		job, getErr := queries.GetPlanningJob(ctx, jobID)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return appointment.ErrNotFound
		}
		if getErr != nil {
			return getErr
		}
		if job.ArchivedAt.Valid || job.WaitlistID == "" || (job.WorkflowStatus != "waitlist" && job.WorkflowStatus != "planning") {
			return appointment.ErrTransition
		}
		created, insertErr := queries.InsertAppointmentDraft(ctx, dbgen.InsertAppointmentDraftParams{
			JobID: jobID, StartsAt: timestamp(input.Time.StartsAt), EndsAt: timestamp(input.Time.EndsAt),
			BufferBeforeMinutes: input.Time.BufferBeforeMinutes, BufferAfterMinutes: input.Time.BufferAfterMinutes,
		})
		if insertErr != nil {
			return mapAppointmentError(insertErr)
		}
		id = created.ID
		if err := queries.SetJobWorkflow(ctx, dbgen.SetJobWorkflowParams{WorkflowStatus: "planning", JobID: jobID}); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "appointment.draft_created", "appointment", id, input.RequestID,
			[]string{"job_id", "time_range", "buffer"})
	})
	if err != nil {
		return appointment.Appointment{}, err
	}
	return s.Get(ctx, id)
}

func (s *AppointmentStore) Get(ctx context.Context, id string) (appointment.Appointment, error) {
	appointmentID, err := uuid(id)
	if err != nil {
		return appointment.Appointment{}, appointment.ErrNotFound
	}
	row, err := s.queries.GetAppointment(ctx, appointmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return appointment.Appointment{}, appointment.ErrNotFound
	}
	if err != nil {
		return appointment.Appointment{}, err
	}
	value := appointment.Appointment{
		ID: row.AID, JobID: row.AJobID, JobNumber: row.JobNumber, JobWorkflow: row.WorkflowStatus,
		JobType: row.JobType, TransportMode: row.TransportMode,
		Lifecycle: appointment.Lifecycle(row.LifecycleStatus), Confirmation: appointment.Confirmation(row.ConfirmationStatus),
		StartsAt: row.StartsAt.Time.UTC(), EndsAt: row.EndsAt.Time.UTC(), BufferBeforeMinutes: row.BufferBeforeMinutes,
		BufferAfterMinutes: row.BufferAfterMinutes, AvailabilityOverrideReason: row.AvailabilityOverrideReason,
		ExternalTransportConfirmed: row.ExternalTransportConfirmed, EstimatedHackMinutes: row.EstimatedHackMinutes,
		Version: row.Version,
	}
	if err := s.loadAssignments(ctx, &value); err != nil {
		return appointment.Appointment{}, err
	}
	return value, nil
}

func (s *AppointmentStore) Assign(ctx context.Context, actor auth.Actor, input appointment.AssignInput) (appointment.Appointment, error) {
	appointmentID, err := uuid(input.ID)
	if err != nil {
		return appointment.Appointment{}, appointment.ErrNotFound
	}
	err = withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		current, getErr := queries.GetAppointmentForUpdate(ctx, appointmentID)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return appointment.ErrNotFound
		}
		if getErr != nil {
			return getErr
		}
		if current.Version != input.ExpectedVersion || !appointment.Lifecycle(current.LifecycleStatus).Editable() {
			return appointment.ErrConflict
		}
		if err := queries.DeleteAppointmentAssignments(ctx, appointmentID); err != nil {
			return err
		}
		if err := queries.DeleteAppointmentResourceAssignments(ctx, appointmentID); err != nil {
			return err
		}
		for _, driverID := range input.Assignments.DriverIDs {
			parsed, parseErr := uuid(driverID)
			if parseErr != nil {
				return appointment.ErrValidation
			}
			rows, insertErr := queries.InsertAppointmentDriver(ctx, dbgen.InsertAppointmentDriverParams{
				DriverID: parsed, IsPrimary: driverID == input.Assignments.PrimaryDriverID, AppointmentID: appointmentID,
			})
			if insertErr != nil {
				return mapAppointmentError(insertErr)
			}
			if rows != 1 {
				return appointment.ErrValidation
			}
		}
		for _, assigned := range input.Assignments.Resources {
			resourceID, parseErr := uuid(assigned.ID)
			if parseErr != nil {
				return appointment.ErrValidation
			}
			rows, insertErr := queries.InsertAppointmentResource(ctx, dbgen.InsertAppointmentResourceParams{
				Purpose: string(assigned.Purpose), ResourceID: resourceID, AppointmentID: appointmentID,
			})
			if insertErr != nil {
				return mapAppointmentError(insertErr)
			}
			if rows != 1 {
				return appointment.ErrValidation
			}
		}
		rows, bumpErr := queries.BumpAppointmentVersion(ctx, dbgen.BumpAppointmentVersionParams{ID: appointmentID, ExpectedVersion: input.ExpectedVersion})
		if bumpErr != nil {
			return mapAppointmentError(bumpErr)
		}
		if rows != 1 {
			return appointment.ErrConflict
		}
		if err := queries.SetAppointmentOverrideReason(ctx, dbgen.SetAppointmentOverrideReasonParams{Reason: input.Assignments.OverrideReason, ID: appointmentID}); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "appointment.assignments_changed", "appointment", input.ID, input.RequestID,
			[]string{"drivers", "resources", "availability_override"})
	})
	if err != nil {
		return appointment.Appointment{}, err
	}
	return s.Get(ctx, input.ID)
}

func (s *AppointmentStore) Propose(ctx context.Context, actor auth.Actor, input appointment.MutateInput, overrideReason string) (appointment.Appointment, error) {
	err := s.mutate(ctx, input.ID, input.ExpectedVersion, func(queries *dbgen.Queries, id pgtype.UUID, current dbgen.GetAppointmentForUpdateRow) error {
		if current.LifecycleStatus != string(appointment.LifecycleDraft) {
			return appointment.ErrTransition
		}
		if err := ensureAssignmentsReady(ctx, queries, id, current); err != nil {
			return err
		}
		if err := queries.SetAppointmentOverrideReason(ctx, dbgen.SetAppointmentOverrideReasonParams{Reason: overrideReason, ID: id}); err != nil {
			return err
		}
		rows, updateErr := queries.SetAppointmentProposal(ctx, dbgen.SetAppointmentProposalParams{ID: id, ExpectedVersion: input.ExpectedVersion})
		if updateErr != nil {
			return mapAppointmentError(updateErr)
		}
		if rows != 1 {
			return appointment.ErrConflict
		}
		if err := refreshReservations(ctx, queries, id); err != nil {
			return err
		}
		jobID, _ := uuid(current.AJobID)
		if err := queries.SetJobWorkflow(ctx, dbgen.SetJobWorkflowParams{WorkflowStatus: "planning", JobID: jobID}); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "appointment.proposed", "appointment", input.ID, input.RequestID,
			[]string{"lifecycle_status", "reservations", "availability_override"})
	})
	if err != nil {
		return appointment.Appointment{}, err
	}
	return s.Get(ctx, input.ID)
}

func (s *AppointmentStore) Reschedule(ctx context.Context, actor auth.Actor, input appointment.MoveInput, overrideReason string) (appointment.Appointment, error) {
	err := s.mutate(ctx, input.ID, input.ExpectedVersion, func(queries *dbgen.Queries, id pgtype.UUID, current dbgen.GetAppointmentForUpdateRow) error {
		if current.LifecycleStatus != string(appointment.LifecycleDraft) {
			if err := ensureAssignmentsReady(ctx, queries, id, current); err != nil {
				return err
			}
		}
		rows, updateErr := queries.UpdateAppointmentTime(ctx, dbgen.UpdateAppointmentTimeParams{
			StartsAt: timestamp(input.StartsAt), EndsAt: timestamp(input.EndsAt), ID: id, ExpectedVersion: input.ExpectedVersion,
		})
		if updateErr != nil {
			return mapAppointmentError(updateErr)
		}
		if rows != 1 {
			return appointment.ErrConflict
		}
		if err := queries.SetAppointmentOverrideReason(ctx, dbgen.SetAppointmentOverrideReasonParams{Reason: overrideReason, ID: id}); err != nil {
			return err
		}
		if err := refreshReservations(ctx, queries, id); err != nil {
			return err
		}
		if current.LifecycleStatus == string(appointment.LifecycleFixed) {
			if err := insertAppointmentEvent(ctx, queries, "appointment.moved", id, input.ExpectedVersion+1); err != nil {
				return err
			}
		}
		return insertAudit(ctx, queries, actor, "appointment.moved", "appointment", input.ID, input.RequestID,
			[]string{"time_range", "confirmation_status", "availability_override"})
	})
	if err != nil {
		return appointment.Appointment{}, err
	}
	return s.Get(ctx, input.ID)
}

func (s *AppointmentStore) Fix(ctx context.Context, actor auth.Actor, input appointment.MutateInput) (appointment.Appointment, error) {
	actorID, err := uuid(actor.UserID)
	if err != nil {
		return appointment.Appointment{}, appointment.ErrValidation
	}
	err = s.mutate(ctx, input.ID, input.ExpectedVersion, func(queries *dbgen.Queries, id pgtype.UUID, current dbgen.GetAppointmentForUpdateRow) error {
		if err := ensureAssignmentsReady(ctx, queries, id, current); err != nil {
			return err
		}
		rows, updateErr := queries.SetAppointmentFixed(ctx, dbgen.SetAppointmentFixedParams{
			ActorUserID: actorID, ID: id, ExpectedVersion: input.ExpectedVersion,
		})
		if updateErr != nil {
			return mapAppointmentError(updateErr)
		}
		if rows != 1 {
			return appointment.ErrConflict
		}
		if err := refreshReservations(ctx, queries, id); err != nil {
			return err
		}
		jobID, _ := uuid(current.AJobID)
		if err := queries.SetJobWorkflow(ctx, dbgen.SetJobWorkflowParams{WorkflowStatus: "scheduled", JobID: jobID}); err != nil {
			return err
		}
		if err := queries.RemoveWaitlistScheduled(ctx, jobID); err != nil {
			return err
		}
		if err := insertAppointmentEvent(ctx, queries, "appointment.fixed", id, input.ExpectedVersion+1); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "appointment.fixed", "appointment", input.ID, input.RequestID,
			[]string{"lifecycle_status", "confirmation_status", "reservations", "outbox"})
	})
	if err != nil {
		return appointment.Appointment{}, err
	}
	return s.Get(ctx, input.ID)
}

func (s *AppointmentStore) Cancel(ctx context.Context, actor auth.Actor, input appointment.CancelInput) (appointment.Appointment, error) {
	actorID, err := uuid(actor.UserID)
	if err != nil {
		return appointment.Appointment{}, appointment.ErrValidation
	}
	err = s.mutate(ctx, input.ID, input.ExpectedVersion, func(queries *dbgen.Queries, id pgtype.UUID, current dbgen.GetAppointmentForUpdateRow) error {
		rows, updateErr := queries.SetAppointmentCancelled(ctx, dbgen.SetAppointmentCancelledParams{
			ActorUserID: actorID, Reason: input.Reason, ID: id, ExpectedVersion: input.ExpectedVersion,
		})
		if updateErr != nil {
			return mapAppointmentError(updateErr)
		}
		if rows != 1 {
			return appointment.ErrConflict
		}
		if err := refreshReservations(ctx, queries, id); err != nil {
			return err
		}
		jobID, _ := uuid(current.AJobID)
		workflow := "waitlist"
		if current.LifecycleStatus == string(appointment.LifecycleFixed) {
			workflow = "cancelled"
		}
		if err := queries.SetJobWorkflow(ctx, dbgen.SetJobWorkflowParams{WorkflowStatus: workflow, JobID: jobID}); err != nil {
			return err
		}
		if workflow == "waitlist" {
			if err := queries.RestoreWaitlistAfterCancellation(ctx, jobID); err != nil {
				return err
			}
		}
		if err := insertAppointmentEvent(ctx, queries, "appointment.cancelled", id, input.ExpectedVersion+1); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "appointment.cancelled", "appointment", input.ID, input.RequestID,
			[]string{"lifecycle_status", "reason", "reservations"})
	})
	if err != nil {
		return appointment.Appointment{}, err
	}
	return s.Get(ctx, input.ID)
}

func (s *AppointmentStore) Complete(ctx context.Context, actor auth.Actor, input appointment.CompleteInput) (appointment.Appointment, error) {
	actorID, err := uuid(actor.UserID)
	if err != nil {
		return appointment.Appointment{}, appointment.ErrValidation
	}
	err = s.mutate(ctx, input.ID, input.ExpectedVersion, func(queries *dbgen.Queries, id pgtype.UUID, current dbgen.GetAppointmentForUpdateRow) error {
		rows, updateErr := queries.SetAppointmentCompleted(ctx, dbgen.SetAppointmentCompletedParams{
			ActorUserID: actorID, OverrideReason: input.OverrideReason, ID: id, ExpectedVersion: input.ExpectedVersion,
		})
		if updateErr != nil {
			return mapAppointmentError(updateErr)
		}
		if rows != 1 {
			return appointment.ErrConflict
		}
		if err := refreshReservations(ctx, queries, id); err != nil {
			return err
		}
		jobID, _ := uuid(current.AJobID)
		if err := queries.SetJobWorkflow(ctx, dbgen.SetJobWorkflowParams{WorkflowStatus: "completed", JobID: jobID}); err != nil {
			return err
		}
		if err := insertAppointmentEvent(ctx, queries, "appointment.completed", id, input.ExpectedVersion+1); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "appointment.completed", "appointment", input.ID, input.RequestID,
			[]string{"lifecycle_status", "reservations", "completion_override"})
	})
	if err != nil {
		return appointment.Appointment{}, err
	}
	return s.Get(ctx, input.ID)
}

func (s *AppointmentStore) ListCalendar(ctx context.Context, fromUTC, toUTC time.Time) ([]appointment.CalendarEvent, error) {
	rows, err := s.queries.ListCalendarAppointments(ctx, dbgen.ListCalendarAppointmentsParams{FromUtc: timestamp(fromUTC), ToUtc: timestamp(toUTC)})
	if err != nil {
		return nil, err
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	result := make([]appointment.CalendarEvent, 0, len(rows))
	index := make(map[string]int, len(rows))
	for _, row := range rows {
		id, parseErr := uuid(row.AID)
		if parseErr != nil {
			return nil, parseErr
		}
		ids = append(ids, id)
		result = append(result, appointment.CalendarEvent{
			Appointment: appointment.Appointment{
				ID: row.AID, JobID: row.AJobID, JobNumber: row.JobNumber,
				Lifecycle: appointment.Lifecycle(row.LifecycleStatus), Confirmation: appointment.Confirmation(row.ConfirmationStatus),
				StartsAt: row.StartsAt.Time.UTC(), EndsAt: row.EndsAt.Time.UTC(), BufferBeforeMinutes: row.BufferBeforeMinutes,
				BufferAfterMinutes: row.BufferAfterMinutes, JobType: row.JobType, Version: row.Version,
			},
			CustomerID: row.CustomerID, CustomerName: row.CustomerName, Locality: row.Locality,
			Street: row.Street, PostalCode: row.PostalCode, VolumeM3: row.JVolumeM3,
			Latitude: row.Latitude, Longitude: row.Longitude,
			MapsURL: customers.MapsURL(customers.CustomerInput{Street: row.Street, PostalCode: row.PostalCode, Locality: row.Locality, CountryCode: "AT"}),
		})
		index[row.AID] = len(result) - 1
	}
	if len(ids) == 0 {
		return result, nil
	}
	drivers, err := s.queries.ListAppointmentDrivers(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range drivers {
		at := index[row.AppointmentID]
		result[at].Drivers = append(result[at].Drivers, appointment.DriverAssignment{ID: row.DriverID, Name: row.DisplayName, Primary: row.IsPrimary})
	}
	resources, err := s.queries.ListAppointmentResources(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range resources {
		at := index[row.AppointmentID]
		result[at].Resources = append(result[at].Resources, appointment.AssignedResource{
			ID: row.ResourceID, Name: row.Name, Type: resource.Type(row.ResourceType), Purpose: appointment.Purpose(row.Purpose),
		})
	}
	return result, nil
}

func (s *AppointmentStore) PlanningOptions(ctx context.Context) (appointment.PlanningOptions, error) {
	driverRows, err := s.queries.ListActiveDriversForPlanning(ctx)
	if err != nil {
		return appointment.PlanningOptions{}, err
	}
	resourceRows, err := s.queries.ListActiveResourcesForPlanning(ctx)
	if err != nil {
		return appointment.PlanningOptions{}, err
	}
	waitlistRows, err := s.queries.ListWaitlistForPlanning(ctx)
	if err != nil {
		return appointment.PlanningOptions{}, err
	}
	result := appointment.PlanningOptions{
		Drivers: make([]appointment.PlanningDriver, 0, len(driverRows)), Resources: make([]appointment.PlanningResource, 0, len(resourceRows)),
		Waitlist: make([]appointment.WaitlistItem, 0, len(waitlistRows)),
	}
	for _, row := range driverRows {
		result.Drivers = append(result.Drivers, appointment.PlanningDriver{ID: row.ID, Name: row.DisplayName, CanCompleteJobs: row.CanCompleteJobs})
	}
	for _, row := range resourceRows {
		result.Resources = append(result.Resources, appointment.PlanningResource{ID: row.ID, Name: row.Name, Type: resource.Type(row.ResourceType), IsExclusive: row.Exclusive})
	}
	for _, row := range waitlistRows {
		result.Waitlist = append(result.Waitlist, appointment.WaitlistItem{
			WaitlistID: row.WaitlistID, JobID: row.JobID, JobNumber: row.JobNumber, JobType: row.JobType,
			VolumeM3: row.JVolumeM3, EstimatedHackMinutes: row.EstimatedHackMinutes,
			CustomerName: row.CustomerName, Locality: row.Locality,
		})
	}
	return result, nil
}

func (s *AppointmentStore) ListConflicts(ctx context.Context, fromUTC, toUTC time.Time, driverIDs, resourceIDs []string, excludeID string) ([]appointment.Conflict, error) {
	parsedDrivers, err := uuidSlice(driverIDs)
	if err != nil {
		return nil, appointment.ErrValidation
	}
	parsedResources, err := uuidSlice(resourceIDs)
	if err != nil {
		return nil, appointment.ErrValidation
	}
	if excludeID != "" {
		if _, err := uuid(excludeID); err != nil {
			return nil, appointment.ErrValidation
		}
	}
	rows, err := s.queries.FindAppointmentConflicts(ctx, dbgen.FindAppointmentConflictsParams{
		DriverIds: parsedDrivers, ResourceIds: parsedResources, FromUtc: timestamp(fromUTC), ToUtc: timestamp(toUTC), ExcludeAppointmentID: excludeID,
	})
	if err != nil {
		return nil, err
	}
	result := make([]appointment.Conflict, 0, len(rows))
	for _, row := range rows {
		result = append(result, appointment.Conflict{
			Type: row.ConflictType, SubjectID: row.SubjectID, SubjectName: row.SubjectName, AppointmentID: row.AppointmentID,
			StartsAt: row.StartsAt.Time.UTC(), EndsAt: row.EndsAt.Time.UTC(), Reason: "überschneidende aktive Reservierung",
		})
	}
	return result, nil
}

func (s *AppointmentStore) DriverCanComplete(ctx context.Context, appointmentID, driverID string) (bool, error) {
	parsedAppointmentID, err := uuid(appointmentID)
	if err != nil {
		return false, appointment.ErrNotFound
	}
	parsedDriverID, err := uuid(driverID)
	if err != nil {
		return false, appointment.ErrNotFound
	}
	return s.queries.DriverCanCompleteAppointment(ctx, dbgen.DriverCanCompleteAppointmentParams{
		AppointmentID: parsedAppointmentID, DriverID: parsedDriverID,
	})
}

func (s *AppointmentStore) mutate(ctx context.Context, id string, expectedVersion int32, operation func(*dbgen.Queries, pgtype.UUID, dbgen.GetAppointmentForUpdateRow) error) error {
	appointmentID, err := uuid(id)
	if err != nil {
		return appointment.ErrNotFound
	}
	return withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		current, getErr := queries.GetAppointmentForUpdate(ctx, appointmentID)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return appointment.ErrNotFound
		}
		if getErr != nil {
			return getErr
		}
		if current.Version != expectedVersion {
			return appointment.ErrConflict
		}
		if err := operation(queries, appointmentID, current); err != nil {
			return mapAppointmentError(err)
		}
		return nil
	})
}

func (s *AppointmentStore) loadAssignments(ctx context.Context, value *appointment.Appointment) error {
	id, err := uuid(value.ID)
	if err != nil {
		return err
	}
	drivers, err := s.queries.ListAppointmentDrivers(ctx, []pgtype.UUID{id})
	if err != nil {
		return err
	}
	for _, row := range drivers {
		value.Drivers = append(value.Drivers, appointment.DriverAssignment{ID: row.DriverID, Name: row.DisplayName, Primary: row.IsPrimary})
	}
	resources, err := s.queries.ListAppointmentResources(ctx, []pgtype.UUID{id})
	if err != nil {
		return err
	}
	for _, row := range resources {
		value.Resources = append(value.Resources, appointment.AssignedResource{
			ID: row.ResourceID, Name: row.Name, Type: resource.Type(row.ResourceType), Purpose: appointment.Purpose(row.Purpose),
		})
	}
	return nil
}

func refreshReservations(ctx context.Context, queries *dbgen.Queries, id pgtype.UUID) error {
	if err := queries.RefreshAppointmentReservations(ctx, id); err != nil {
		return mapAppointmentError(err)
	}
	if err := queries.RefreshAppointmentResourceReservations(ctx, id); err != nil {
		return mapAppointmentError(err)
	}
	return nil
}

func ensureAssignmentsReady(ctx context.Context, queries *dbgen.Queries, id pgtype.UUID, current dbgen.GetAppointmentForUpdateRow) error {
	ready, err := queries.AppointmentAssignmentsReady(ctx, dbgen.AppointmentAssignmentsReadyParams{
		AppointmentID: id, JobType: current.JobType, TransportMode: current.TransportMode,
		ExternalTransportConfirmed: current.ExternalTransportConfirmed,
	})
	if err != nil {
		return err
	}
	if !ready {
		return appointment.ErrValidation
	}
	return nil
}

func insertAppointmentEvent(ctx context.Context, queries *dbgen.Queries, eventType string, id pgtype.UUID, version int32) error {
	idText := uuidText(id)
	payload, err := json.Marshal(map[string]any{"appointment_id": idText, "version": version})
	if err != nil {
		return err
	}
	return queries.InsertOutboxEvent(ctx, dbgen.InsertOutboxEventParams{
		EventType: eventType, AggregateID: id, Payload: payload,
		IdempotencyKey: eventType + ":" + idText + ":v" + strconv.FormatInt(int64(version), 10),
	})
}

func uuidText(id pgtype.UUID) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", id.Bytes[0:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:16])
}

func uuidSlice(values []string) ([]pgtype.UUID, error) {
	result := make([]pgtype.UUID, 0, len(values))
	for _, value := range values {
		parsed, err := uuid(value)
		if err != nil {
			return nil, err
		}
		result = append(result, parsed)
	}
	return result, nil
}

func mapAppointmentError(err error) error {
	if errors.Is(err, appointment.ErrConflict) || errors.Is(err, appointment.ErrNotFound) ||
		errors.Is(err, appointment.ErrTransition) || errors.Is(err, appointment.ErrValidation) {
		return err
	}
	var postgresErr *pgconn.PgError
	if errors.As(err, &postgresErr) {
		switch postgresErr.Code {
		case "23P01", "23505", "40001", "40P01":
			return fmt.Errorf("%w: concurrent reservation", appointment.ErrConflict)
		case "23503", "23514", "22P02":
			return appointment.ErrValidation
		}
	}
	return err
}
