package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/appointment"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/driver"
	"example.invalid/hackplan/internal/notification"
	"example.invalid/hackplan/internal/resource"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AppointmentStore struct {
	pool            *pgxpool.Pool
	queries         *dbgen.Queries
	tokens          *notification.KeyRing
	confirmationTTL time.Duration
	mailMaxAttempts int32
	smsMaxAttempts  int32
	mailEnabled     bool
	smsEnabled      bool
	now             func() time.Time
}

type AppointmentStoreOption func(*AppointmentStore)

func WithConfirmationPlanning(tokens *notification.KeyRing, ttl time.Duration, mailMaxAttempts, smsMaxAttempts int, mailEnabled, smsEnabled bool) AppointmentStoreOption {
	return func(store *AppointmentStore) {
		if tokens != nil {
			store.tokens = tokens
		}
		if ttl > 0 {
			store.confirmationTTL = ttl
		}
		if mailMaxAttempts > 0 && mailMaxAttempts <= math.MaxInt32 {
			store.mailMaxAttempts = int32(mailMaxAttempts)
		}
		if smsMaxAttempts > 0 && smsMaxAttempts <= math.MaxInt32 {
			store.smsMaxAttempts = int32(smsMaxAttempts)
		}
		store.mailEnabled = mailEnabled
		store.smsEnabled = smsEnabled
	}
}

func NewAppointmentStore(pool *pgxpool.Pool, options ...AppointmentStoreOption) *AppointmentStore {
	store := &AppointmentStore{
		pool: pool, queries: dbgen.New(pool), tokens: notification.DevelopmentKeyRing(),
		confirmationTTL: 14 * 24 * time.Hour, mailMaxAttempts: 6, smsMaxAttempts: 6,
		mailEnabled: true, smsEnabled: true, now: time.Now,
	}
	for _, option := range options {
		option(store)
	}
	return store
}

func (s *AppointmentStore) planConfirmation(
	ctx context.Context,
	queries *dbgen.Queries,
	appointmentID pgtype.UUID,
	withoutNotificationReason string,
	revokeReason string,
) error {
	return s.planConfirmationAt(ctx, queries, appointmentID, withoutNotificationReason, revokeReason, s.now().UTC())
}

func (s *AppointmentStore) planConfirmationAt(
	ctx context.Context,
	queries *dbgen.Queries,
	appointmentID pgtype.UUID,
	withoutNotificationReason string,
	revokeReason string,
	now time.Time,
) error {
	data, err := queries.GetNotificationPlanningData(ctx, appointmentID)
	if err != nil {
		return err
	}
	type target struct {
		channel     notification.Channel
		recipient   string
		maxAttempts int32
	}
	targets := make([]target, 0, 2)
	if s.mailEnabled && (data.NotificationPreference == "email" || data.NotificationPreference == "both") && data.Email != "" {
		targets = append(targets, target{channel: notification.ChannelEmail, recipient: data.Email, maxAttempts: s.mailMaxAttempts})
	}
	if s.smsEnabled && (data.NotificationPreference == "sms" || data.NotificationPreference == "both") && data.Phone != "" {
		targets = append(targets, target{channel: notification.ChannelSMS, recipient: data.Phone, maxAttempts: s.smsMaxAttempts})
	}
	withoutNotificationReason = strings.TrimSpace(withoutNotificationReason)
	if len(targets) == 0 && withoutNotificationReason == "" {
		return appointment.ErrNotification
	}
	if err := queries.RevokeActiveConfirmationRequests(ctx, dbgen.RevokeActiveConfirmationRequestsParams{
		Reason: &revokeReason, AppointmentID: appointmentID,
	}); err != nil {
		return err
	}
	if len(targets) == 0 {
		return queries.SetAppointmentNotificationOverride(ctx, dbgen.SetAppointmentNotificationOverrideParams{
			Reason: withoutNotificationReason, ConfirmationStatus: string(appointment.ConfirmationNotRequested), AppointmentID: appointmentID,
		})
	}
	if err := queries.SetAppointmentNotificationOverride(ctx, dbgen.SetAppointmentNotificationOverrideParams{
		Reason: "", ConfirmationStatus: string(appointment.ConfirmationPending), AppointmentID: appointmentID,
	}); err != nil {
		return err
	}
	requestID, err := queries.NewConfirmationRequestID(ctx)
	if err != nil {
		return err
	}
	requestUUID, err := uuid(requestID)
	if err != nil {
		return err
	}
	tokenVersion, err := queries.NextConfirmationTokenVersion(ctx, appointmentID)
	if err != nil {
		return err
	}
	material, err := s.tokens.Issue(requestID, data.AppointmentID, tokenVersion)
	if err != nil {
		return err
	}
	if err := queries.InsertConfirmationRequest(ctx, dbgen.InsertConfirmationRequestParams{
		ID: requestUUID, AppointmentID: appointmentID, TokenHash: material.Hash, FormNonceHash: material.NonceHash,
		TokenKeyID: material.KeyID, TokenVersion: tokenVersion, ExpiresAt: timestamp(now.UTC().Add(s.confirmationTTL)),
	}); err != nil {
		return err
	}
	snapshot, err := json.Marshal(map[string]string{
		"customer_name": data.CustomerName, "job_type": data.JobType, "volume_m3": data.JVolumeM3,
		"starts_at": data.StartsAt.Time.UTC().Format(time.RFC3339Nano), "ends_at": data.EndsAt.Time.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	for _, item := range targets {
		notificationID, insertErr := queries.InsertNotification(ctx, dbgen.InsertNotificationParams{
			AppointmentID: appointmentID, ConfirmationRequestID: requestUUID, Channel: string(item.channel),
			RecipientSnapshot: item.recipient, TemplateVersion: notification.TemplateVersion, Parameters: snapshot, MaxAttempts: item.maxAttempts,
		})
		if insertErr != nil {
			return insertErr
		}
		notificationUUID, parseErr := uuid(notificationID)
		if parseErr != nil {
			return parseErr
		}
		if err := queries.InsertNotificationOutboxEvent(ctx, dbgen.InsertNotificationOutboxEventParams{
			NotificationID: notificationUUID, MaxAttempts: item.maxAttempts,
		}); err != nil {
			return err
		}
	}
	return nil
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

func (s *AppointmentStore) Plan(ctx context.Context, actor auth.Actor, input appointment.PlanInput, overrideReason string) (appointment.Appointment, error) {
	jobID, err := uuid(input.JobID)
	if err != nil {
		return appointment.Appointment{}, appointment.ErrNotFound
	}
	var id string
	err = withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if lockErr := queries.LockSchedulingMutation(ctx); lockErr != nil {
			return lockErr
		}
		job, getErr := queries.GetPlanningJob(ctx, jobID)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return appointment.ErrNotFound
		}
		if getErr != nil {
			return getErr
		}
		if job.ArchivedAt.Valid || job.WaitlistID == "" || job.WorkflowStatus != "waitlist" {
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
		appointmentID, parseErr := uuid(id)
		if parseErr != nil {
			return parseErr
		}
		if err := queries.SetJobWorkflow(ctx, dbgen.SetJobWorkflowParams{WorkflowStatus: "planning", JobID: jobID}); err != nil {
			return err
		}
		if err := insertAudit(ctx, queries, actor, "appointment.draft_created", "appointment", id, input.RequestID,
			[]string{"job_id", "time_range", "buffer"}); err != nil {
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
		current, getErr := queries.GetAppointmentForUpdate(ctx, appointmentID)
		if getErr != nil {
			return getErr
		}
		if err := ensureAssignmentsReady(ctx, queries, appointmentID, current, input.Time.StartsAt, input.Time.EndsAt, overrideReason); err != nil {
			return err
		}
		rows, bumpErr := queries.BumpAppointmentVersion(ctx, dbgen.BumpAppointmentVersionParams{ID: appointmentID, ExpectedVersion: created.Version})
		if bumpErr != nil {
			return mapAppointmentError(bumpErr)
		}
		if rows != 1 {
			return appointment.ErrVersionConflict
		}
		if err := queries.SetAppointmentOverrideReason(ctx, dbgen.SetAppointmentOverrideReasonParams{Reason: overrideReason, ID: appointmentID}); err != nil {
			return err
		}
		if err := insertAudit(ctx, queries, actor, "appointment.assignments_changed", "appointment", id, input.RequestID,
			[]string{"drivers", "resources", "availability_override"}); err != nil {
			return err
		}
		rows, proposalErr := queries.SetAppointmentProposal(ctx, dbgen.SetAppointmentProposalParams{ID: appointmentID, ExpectedVersion: created.Version + 1})
		if proposalErr != nil {
			return mapAppointmentError(proposalErr)
		}
		if rows != 1 {
			return appointment.ErrVersionConflict
		}
		if err := refreshReservations(ctx, queries, appointmentID); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "appointment.proposed", "appointment", id, input.RequestID,
			[]string{"lifecycle_status", "reservations", "availability_override"})
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
		PreferredStartDate: row.PreferredStartDate, PreferredEndDate: row.PreferredEndDate, PreferenceMode: row.PreferenceMode,
		Lifecycle: appointment.Lifecycle(row.LifecycleStatus), Confirmation: appointment.Confirmation(row.ConfirmationStatus),
		StartsAt: row.StartsAt.Time.UTC(), EndsAt: row.EndsAt.Time.UTC(), BufferBeforeMinutes: row.BufferBeforeMinutes,
		BufferAfterMinutes: row.BufferAfterMinutes, AvailabilityOverrideReason: row.AvailabilityOverrideReason,
		ExternalTransportConfirmed: row.ExternalTransportConfirmed, EstimatedHackMinutes: row.EstimatedHackMinutes,
		EstimatedTransportMinutes: row.EstimatedTransportMinutes,
		Version:                   row.Version,
	}
	if err := s.loadAssignments(ctx, &value); err != nil {
		return appointment.Appointment{}, err
	}
	return value, nil
}

func (s *AppointmentStore) Detail(ctx context.Context, id string) (appointment.Detail, error) {
	appointmentID, err := uuid(id)
	if err != nil {
		return appointment.Detail{}, appointment.ErrNotFound
	}
	row, err := s.queries.GetAppointmentDetail(ctx, appointmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return appointment.Detail{}, appointment.ErrNotFound
	}
	if err != nil {
		return appointment.Detail{}, err
	}
	value := appointment.Detail{
		CalendarEvent: appointment.CalendarEvent{
			Appointment: appointment.Appointment{
				ID: row.AID, JobID: row.AJobID, JobNumber: row.JobNumber, JobWorkflow: row.WorkflowStatus,
				JobType: row.JobType, TransportMode: row.TransportMode,
				Lifecycle: appointment.Lifecycle(row.LifecycleStatus), Confirmation: appointment.Confirmation(row.ConfirmationStatus),
				StartsAt: row.StartsAt.Time.UTC(), EndsAt: row.EndsAt.Time.UTC(),
				BufferBeforeMinutes: row.BufferBeforeMinutes, BufferAfterMinutes: row.BufferAfterMinutes,
				ExternalTransportConfirmed: row.ExternalTransportConfirmed, EstimatedHackMinutes: row.EstimatedHackMinutes,
				EstimatedTransportMinutes: row.EstimatedTransportMinutes,
				Version:                   row.Version,
			},
			CustomerID: row.CustomerID, CustomerName: row.CustomerName, Locality: row.Locality,
			Street: row.Street, PostalCode: row.PostalCode, VolumeM3: row.JVolumeM3,
			Latitude: row.Latitude, Longitude: row.Longitude,
			MapsURL: appointmentMapsURL(row.Latitude, row.Longitude, row.Street, row.PostalCode, row.Locality),
		},
		Phone: row.Phone, Email: row.Email, NotificationPreference: row.NotificationPreference,
	}
	if err := s.loadAssignments(ctx, &value.Appointment); err != nil {
		return appointment.Detail{}, err
	}
	notes, err := s.queries.ListJobNotes(ctx, mustUUID(value.JobID))
	if err != nil {
		return appointment.Detail{}, err
	}
	value.Notes = make([]appointment.Note, 0, len(notes))
	for _, note := range notes {
		value.Notes = append(value.Notes, appointment.Note{
			AuthorName: note.AuthorName,
			Body:       note.Body,
			CreatedAt:  note.CreatedAt.Time.UTC(),
		})
	}
	return value, nil
}

func (s *AppointmentStore) Assign(ctx context.Context, actor auth.Actor, input appointment.AssignInput) (appointment.Appointment, error) {
	appointmentID, err := uuid(input.ID)
	if err != nil {
		return appointment.Appointment{}, appointment.ErrNotFound
	}
	err = withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if lockErr := queries.LockSchedulingMutation(ctx); lockErr != nil {
			return lockErr
		}
		current, getErr := queries.GetAppointmentForUpdate(ctx, appointmentID)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return appointment.ErrNotFound
		}
		if getErr != nil {
			return getErr
		}
		if current.Version != input.ExpectedVersion {
			return appointment.ErrVersionConflict
		}
		if !appointment.Lifecycle(current.LifecycleStatus).Editable() {
			return appointment.ErrTransition
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
		if current.LifecycleStatus != string(appointment.LifecycleDraft) {
			if err := ensureAssignmentsReady(ctx, queries, appointmentID, current, current.StartsAt.Time.UTC(), current.EndsAt.Time.UTC(), input.Assignments.OverrideReason); err != nil {
				return err
			}
		}
		rows, bumpErr := queries.BumpAppointmentVersion(ctx, dbgen.BumpAppointmentVersionParams{ID: appointmentID, ExpectedVersion: input.ExpectedVersion})
		if bumpErr != nil {
			return mapAppointmentError(bumpErr)
		}
		if rows != 1 {
			return appointment.ErrVersionConflict
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
		if err := ensureAssignmentsReady(ctx, queries, id, current, current.StartsAt.Time.UTC(), current.EndsAt.Time.UTC(), overrideReason); err != nil {
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
			return appointment.ErrVersionConflict
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
		rows, updateErr := queries.UpdateAppointmentTime(ctx, dbgen.UpdateAppointmentTimeParams{
			StartsAt: timestamp(input.StartsAt), EndsAt: timestamp(input.EndsAt), ID: id, ExpectedVersion: input.ExpectedVersion,
		})
		if updateErr != nil {
			return mapAppointmentError(updateErr)
		}
		if rows != 1 {
			return appointment.ErrVersionConflict
		}
		if err := queries.SetAppointmentOverrideReason(ctx, dbgen.SetAppointmentOverrideReasonParams{Reason: overrideReason, ID: id}); err != nil {
			return err
		}
		if current.LifecycleStatus != string(appointment.LifecycleDraft) {
			if err := ensureAssignmentsReady(ctx, queries, id, current, input.StartsAt.UTC(), input.EndsAt.UTC(), overrideReason); err != nil {
				return err
			}
		}
		if err := refreshReservations(ctx, queries, id); err != nil {
			return err
		}
		if current.LifecycleStatus == string(appointment.LifecycleFixed) {
			if err := insertAppointmentEvent(ctx, queries, "appointment.moved", id, input.ExpectedVersion+1); err != nil {
				return err
			}
			if err := s.planConfirmation(ctx, queries, id, input.WithoutNotificationReason, "appointment moved"); err != nil {
				return err
			}
		}
		return insertAudit(ctx, queries, actor, "appointment.moved", "appointment", input.ID, input.RequestID,
			[]string{"time_range", "confirmation_status", "availability_override", "confirmation_request", "notifications", "notification_override"})
	})
	if err != nil {
		return appointment.Appointment{}, err
	}
	return s.Get(ctx, input.ID)
}

func (s *AppointmentStore) Fix(ctx context.Context, actor auth.Actor, input appointment.FixInput) (appointment.Appointment, error) {
	actorID, err := uuid(actor.UserID)
	if err != nil {
		return appointment.Appointment{}, appointment.ErrValidation
	}
	err = s.mutate(ctx, input.ID, input.ExpectedVersion, func(queries *dbgen.Queries, id pgtype.UUID, current dbgen.GetAppointmentForUpdateRow) error {
		if err := ensureAssignmentsReady(ctx, queries, id, current, current.StartsAt.Time.UTC(), current.EndsAt.Time.UTC(), current.AvailabilityOverrideReason); err != nil {
			return err
		}
		rows, updateErr := queries.SetAppointmentFixed(ctx, dbgen.SetAppointmentFixedParams{
			ActorUserID: actorID, ID: id, ExpectedVersion: input.ExpectedVersion,
		})
		if updateErr != nil {
			return mapAppointmentError(updateErr)
		}
		if rows != 1 {
			return appointment.ErrVersionConflict
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
		if err := s.planConfirmation(ctx, queries, id, input.WithoutNotificationReason, "appointment fixed"); err != nil {
			return err
		}
		return insertAudit(ctx, queries, actor, "appointment.fixed", "appointment", input.ID, input.RequestID,
			[]string{"lifecycle_status", "confirmation_status", "reservations", "confirmation_request", "notifications", "notification_override", "outbox"})
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
			return appointment.ErrVersionConflict
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

func (s *AppointmentStore) Reopen(ctx context.Context, actor auth.Actor, input appointment.ReopenInput) (appointment.Appointment, error) {
	err := s.mutate(ctx, input.ID, input.ExpectedVersion, func(queries *dbgen.Queries, id pgtype.UUID, current dbgen.GetAppointmentForUpdateRow) error {
		if current.LifecycleStatus != string(appointment.LifecycleCancelled) {
			return appointment.ErrTransition
		}
		if err := ensureAssignmentsReady(
			ctx,
			queries,
			id,
			current,
			current.StartsAt.Time.UTC(),
			current.EndsAt.Time.UTC(),
			input.OverrideReason,
		); err != nil {
			return err
		}
		rows, updateErr := queries.ReopenCancelledAppointment(ctx, dbgen.ReopenCancelledAppointmentParams{
			AvailabilityOverrideReason: input.OverrideReason,
			ID:                         id,
			ExpectedVersion:            input.ExpectedVersion,
		})
		if updateErr != nil {
			return mapAppointmentError(updateErr)
		}
		if rows != 1 {
			return appointment.ErrVersionConflict
		}
		if err := refreshReservations(ctx, queries, id); err != nil {
			return err
		}
		jobID, err := uuid(current.AJobID)
		if err != nil {
			return appointment.ErrValidation
		}
		if err := queries.SetJobWorkflow(ctx, dbgen.SetJobWorkflowParams{
			WorkflowStatus: "planning",
			JobID:          jobID,
		}); err != nil {
			return err
		}
		revokeReason := "appointment reopened"
		if err := queries.RevokeActiveConfirmationRequests(ctx, dbgen.RevokeActiveConfirmationRequestsParams{
			Reason:        &revokeReason,
			AppointmentID: id,
		}); err != nil {
			return err
		}
		return insertAppointmentReopenedAudit(
			ctx,
			queries,
			actor,
			input,
			current.CancellationReason,
			[]string{
				"lifecycle_status",
				"confirmation_status",
				"reservations",
				"job_workflow",
				"confirmation_request",
				"cancellation",
				"availability_override",
			},
		)
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
			return appointment.ErrVersionConflict
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
			MapsURL: customers.PointMapsURL(parseFloat(row.Latitude), parseFloat(row.Longitude)),
		})
		if result[len(result)-1].MapsURL == "" {
			result[len(result)-1].MapsURL = customers.MapsURL(customers.CustomerInput{Street: row.Street, PostalCode: row.PostalCode, Locality: row.Locality, CountryCode: "AT"})
		}
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

func appointmentMapsURL(latitude, longitude, street, postalCode, locality string) string {
	if link := customers.PointMapsURL(parseFloat(latitude), parseFloat(longitude)); link != "" {
		return link
	}
	return customers.MapsURL(customers.CustomerInput{Street: street, PostalCode: postalCode, Locality: locality, CountryCode: "AT"})
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
			TransportMode: row.TransportMode, ExternalTransportConfirmed: row.ExternalTransportConfirmed,
			VolumeM3: row.JVolumeM3, EstimatedHackMinutes: row.EstimatedHackMinutes, EstimatedTransportMinutes: row.EstimatedTransportMinutes,
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
			JobNumber: row.JobNumber, CustomerName: row.CustomerName,
			StartsAt: row.StartsAt.Time.UTC(), EndsAt: row.EndsAt.Time.UTC(), Reason: "überschneidende aktive Reservierung",
		})
	}
	return result, nil
}

func (s *AppointmentStore) Swap(ctx context.Context, actor auth.Actor, input appointment.SwapInput) ([]appointment.Appointment, error) {
	ids := []string{input.FirstID, input.SecondID}
	slices.Sort(ids)
	err := withQueries(ctx, s.pool, func(queries *dbgen.Queries) error {
		if err := queries.LockSchedulingMutation(ctx); err != nil {
			return err
		}
		locked := make(map[string]dbgen.GetAppointmentForUpdateRow, 2)
		parsed := make(map[string]pgtype.UUID, 2)
		for _, id := range ids {
			value, parseErr := uuid(id)
			if parseErr != nil {
				return appointment.ErrNotFound
			}
			row, getErr := queries.GetAppointmentForUpdate(ctx, value)
			if errors.Is(getErr, pgx.ErrNoRows) {
				return appointment.ErrNotFound
			}
			if getErr != nil {
				return getErr
			}
			locked[id], parsed[id] = row, value
		}
		first, second := locked[input.FirstID], locked[input.SecondID]
		if first.Version != input.FirstVersion || second.Version != input.SecondVersion {
			return appointment.ErrVersionConflict
		}
		if !swapLifecycle(first.LifecycleStatus) || !swapLifecycle(second.LifecycleStatus) {
			return appointment.ErrTransition
		}
		for _, value := range []struct {
			id      string
			version int32
		}{{input.FirstID, input.FirstVersion}, {input.SecondID, input.SecondVersion}} {
			rows, prepareErr := queries.PrepareAppointmentSwap(ctx, dbgen.PrepareAppointmentSwapParams{ID: parsed[value.id], ExpectedVersion: value.version})
			if prepareErr != nil {
				return mapAppointmentError(prepareErr)
			}
			if rows != 1 {
				return appointment.ErrVersionConflict
			}
			if err := refreshReservations(ctx, queries, parsed[value.id]); err != nil {
				return err
			}
		}
		firstDuration, secondDuration := first.EndsAt.Time.Sub(first.StartsAt.Time), second.EndsAt.Time.Sub(second.StartsAt.Time)
		updates := []struct {
			id         string
			version    int32
			start, end time.Time
		}{{input.FirstID, input.FirstVersion, second.StartsAt.Time, second.StartsAt.Time.Add(firstDuration)}, {input.SecondID, input.SecondVersion, first.StartsAt.Time, first.StartsAt.Time.Add(secondDuration)}}
		for _, value := range updates {
			rows, updateErr := queries.UpdateAppointmentTime(ctx, dbgen.UpdateAppointmentTimeParams{StartsAt: timestamp(value.start.UTC()), EndsAt: timestamp(value.end.UTC()), ID: parsed[value.id], ExpectedVersion: value.version})
			if updateErr != nil {
				return mapAppointmentError(updateErr)
			}
			if rows != 1 {
				return appointment.ErrVersionConflict
			}
		}
		for _, value := range []struct {
			id, status string
			version    int32
		}{{input.FirstID, first.LifecycleStatus, input.FirstVersion + 1}, {input.SecondID, second.LifecycleStatus, input.SecondVersion + 1}} {
			rows, restoreErr := queries.RestoreAppointmentSwapStatus(ctx, dbgen.RestoreAppointmentSwapStatusParams{LifecycleStatus: value.status, ID: parsed[value.id], ExpectedVersion: value.version})
			if restoreErr != nil {
				return mapAppointmentError(restoreErr)
			}
			if rows != 1 {
				return appointment.ErrVersionConflict
			}
			if err := refreshReservations(ctx, queries, parsed[value.id]); err != nil {
				return err
			}
		}
		return insertAudit(ctx, queries, actor, "appointment.swapped", "appointment", input.FirstID, input.RequestID, []string{"first_appointment_id", "second_appointment_id", "time_ranges"})
	})
	if err != nil {
		return nil, err
	}
	first, err := s.Get(ctx, input.FirstID)
	if err != nil {
		return nil, err
	}
	second, err := s.Get(ctx, input.SecondID)
	if err != nil {
		return nil, err
	}
	return []appointment.Appointment{first, second}, nil
}

func swapLifecycle(value string) bool {
	return value == string(appointment.LifecycleDraft) || value == string(appointment.LifecycleProposal)
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
		if lockErr := queries.LockSchedulingMutation(ctx); lockErr != nil {
			return lockErr
		}
		current, getErr := queries.GetAppointmentForUpdate(ctx, appointmentID)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return appointment.ErrNotFound
		}
		if getErr != nil {
			return getErr
		}
		if current.Version != expectedVersion {
			return appointment.ErrVersionConflict
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

func ensureAssignmentsReady(
	ctx context.Context,
	queries *dbgen.Queries,
	id pgtype.UUID,
	current dbgen.GetAppointmentForUpdateRow,
	startsAt, endsAt time.Time,
	overrideReason string,
) error {
	driverIDs, err := queries.LockAppointmentDrivers(ctx, id)
	if err != nil {
		return err
	}
	if _, err := queries.LockAppointmentResources(ctx, id); err != nil {
		return err
	}
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
	requiredMinutes := current.EstimatedHackMinutes + current.EstimatedTransportMinutes
	if endsAt.Sub(startsAt) < time.Duration(requiredMinutes)*time.Minute {
		return appointment.ErrValidation
	}
	if strings.TrimSpace(overrideReason) != "" {
		return nil
	}
	fromUTC := startsAt.Add(-time.Duration(current.BufferBeforeMinutes) * time.Minute).UTC()
	toUTC := endsAt.Add(time.Duration(current.BufferAfterMinutes) * time.Minute).UTC()
	location, err := time.LoadLocation("Europe/Vienna")
	if err != nil {
		return err
	}
	for _, driverID := range driverIDs {
		availability, loadErr := loadDriverAvailabilitySnapshot(ctx, queries, driverID, fromUTC, toUTC, location)
		if loadErr != nil {
			return loadErr
		}
		status, _, evaluateErr := driver.EvaluateAvailability(availability, fromUTC, toUTC, location)
		if evaluateErr != nil {
			return evaluateErr
		}
		if status != driver.StatusAvailable {
			return appointment.ErrAvailability
		}
	}
	return nil
}

func loadDriverAvailabilitySnapshot(
	ctx context.Context,
	queries *dbgen.Queries,
	driverID string,
	fromUTC, toUTC time.Time,
	location *time.Location,
) (driver.Availability, error) {
	parsedID, err := uuid(driverID)
	if err != nil {
		return driver.Availability{}, appointment.ErrValidation
	}
	profileRow, err := queries.GetDriverProfile(ctx, parsedID)
	if err != nil {
		return driver.Availability{}, err
	}
	localFrom := fromUTC.In(location).Format(time.DateOnly)
	localTo := toUTC.Add(-time.Nanosecond).In(location).Format(time.DateOnly)
	fromDate, err := dateValue(localFrom)
	if err != nil {
		return driver.Availability{}, err
	}
	toDate, err := dateValue(localTo)
	if err != nil {
		return driver.Availability{}, err
	}
	ruleRows, err := queries.ListAvailabilityRulesInRange(ctx, dbgen.ListAvailabilityRulesInRangeParams{
		DriverID: parsedID, LocalFrom: fromDate, LocalTo: toDate,
	})
	if err != nil {
		return driver.Availability{}, err
	}
	exceptionRows, err := queries.ListAvailabilityExceptionsInRange(ctx, dbgen.ListAvailabilityExceptionsInRangeParams{
		DriverID: parsedID, LocalFrom: fromDate, LocalTo: toDate, FromUtc: timestamp(fromUTC), ToUtc: timestamp(toUTC),
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
	return driver.Availability{
		Profile: driver.Profile{
			ID: profileRow.DID, IsActive: profileRow.Active, IsPrimary: profileRow.IsPrimary,
			AvailabilityPolicy: driver.AvailabilityPolicy(profileRow.AvailabilityPolicy),
		},
		Rules: rules, Exceptions: exceptions,
	}, nil
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

func insertAppointmentReopenedAudit(
	ctx context.Context,
	queries *dbgen.Queries,
	actor auth.Actor,
	input appointment.ReopenInput,
	previousCancellationReason string,
	changedFields []string,
) error {
	metadata, err := json.Marshal(map[string]any{
		"changed_fields":               changedFields,
		"reason":                       input.Reason,
		"previous_cancellation_reason": previousCancellationReason,
	})
	if err != nil {
		return fmt.Errorf("postgres: encoding appointment reopen audit metadata: %w", err)
	}
	return queries.InsertAuditEvent(ctx, dbgen.InsertAuditEventParams{
		ActorType:   "user",
		ActorUserID: actor.UserID,
		Action:      "appointment.reopened",
		ObjectType:  "appointment",
		ObjectID:    input.ID,
		RequestID:   input.RequestID,
		Metadata:    metadata,
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
	if errors.Is(err, appointment.ErrConflict) || errors.Is(err, appointment.ErrVersionConflict) || errors.Is(err, appointment.ErrNotFound) ||
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
