package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/planning"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PlanningStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
	now     func() time.Time
}

func NewPlanningStore(pool *pgxpool.Pool) *PlanningStore {
	return &PlanningStore{pool: pool, queries: dbgen.New(pool), now: time.Now}
}

func (s *PlanningStore) LoadSnapshot(ctx context.Context, jobID string, from, to time.Time) (planning.Snapshot, error) {
	parsed, err := uuid(jobID)
	if err != nil {
		return planning.Snapshot{}, planning.ErrNotFound
	}
	row, err := s.queries.GetPlanningInput(ctx, parsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return planning.Snapshot{}, planning.ErrNotFound
	}
	if err != nil {
		return planning.Snapshot{}, err
	}
	if row.WorkflowStatus != "waitlist" {
		return planning.Snapshot{}, planning.ErrConflict
	}
	latitude, longitude, _ := coordinates(row.Latitude, row.Longitude)
	drivers, err := s.queries.ListActiveDriversForPlanning(ctx)
	if err != nil {
		return planning.Snapshot{}, err
	}
	resources, err := s.queries.ListActiveResourcesForPlanning(ctx)
	if err != nil {
		return planning.Snapshot{}, err
	}
	reservations, err := s.queries.ListPlanningReservations(ctx, dbgen.ListPlanningReservationsParams{SearchFrom: timestamp(from), SearchTo: timestamp(to)})
	if err != nil {
		return planning.Snapshot{}, err
	}
	result := planning.Snapshot{Job: planning.Job{ID: row.JobID, Number: row.JobNumber, Type: row.JobType, TransportMode: row.TransportMode, Urgency: row.Urgency, Region: row.Region, Version: row.JobVersion, WaitlistVersion: row.WaitlistVersion, CustomerVersion: row.CustomerVersion, HackMinutes: int(row.EstimatedHackMinutes), TransportMinutes: int(row.EstimatedTransportMinutes), ExternalTransportConfirmed: row.ExternalTransportConfirmed, ReceivedAt: row.ReceivedAt.Time.UTC(), EnteredAt: row.EnteredAt.Time.UTC(), PreferredStart: row.PreferredStartDate, PreferredEnd: row.PreferredEndDate, Location: planning.Point{Latitude: latitude, Longitude: longitude}}}
	for _, value := range drivers {
		result.Drivers = append(result.Drivers, planning.Driver{ID: value.ID, Name: value.DisplayName})
	}
	for _, value := range resources {
		result.Resources = append(result.Resources, planning.Resource{ID: value.ID, Name: value.Name, Type: value.ResourceType, Exclusive: value.Exclusive})
	}
	for _, value := range reservations {
		lat, lon, _ := coordinates(value.Latitude, value.Longitude)
		result.Reservations = append(result.Reservations, planning.Reservation{ID: value.AppointmentID, StartsAt: value.StartsAt.Time.UTC(), EndsAt: value.EndsAt.Time.UTC(), DriverIDs: value.DriverIds, ResourceIDs: value.ResourceIds, Location: planning.Point{Latitude: lat, Longitude: lon}})
	}
	return result, nil
}

func (s *PlanningStore) SaveRun(ctx context.Context, actor auth.Actor, snapshot planning.Snapshot, from, to time.Time, suggestions []planning.Suggestion, cfg planning.Config) (planning.Run, error) {
	jobID, err := uuid(snapshot.Job.ID)
	if err != nil {
		return planning.Run{}, planning.ErrValidation
	}
	actorID, err := uuid(actor.UserID)
	if err != nil {
		return planning.Run{}, planning.ErrValidation
	}
	configJSON, err := json.Marshal(planning.RunSnapshot{Config: cfg, Exclusions: planning.ExplainExclusions(snapshot, suggestions, from, to)})
	if err != nil {
		return planning.Run{}, err
	}
	var runID string
	err = withQueries(ctx, s.pool, func(q *dbgen.Queries) error {
		fingerprint, fingerprintErr := q.CurrentPlanningInputFingerprint(ctx, dbgen.CurrentPlanningInputFingerprintParams{
			JobID: jobID, SearchFrom: timestamp(from), SearchTo: timestamp(to),
		})
		if fingerprintErr != nil {
			return fingerprintErr
		}
		runID, err = q.InsertPlanningRun(ctx, dbgen.InsertPlanningRunParams{JobID: jobID, ActorUserID: actorID, JobVersion: snapshot.Job.Version, WaitlistVersion: snapshot.Job.WaitlistVersion, SearchFrom: timestamp(from), SearchTo: timestamp(to), InputFingerprint: fingerprint, ConfigSnapshot: configJSON, ExpiresAt: timestamp(s.now().UTC().Add(cfg.SuggestionTTL))})
		if err != nil {
			return err
		}
		parsedRun, _ := uuid(runID)
		for _, value := range suggestions {
			invalidMetric := value.DistanceMeters < 0 || value.DistanceMeters > math.MaxInt32 ||
				value.DurationSeconds < 0 || value.DurationSeconds > math.MaxInt32
			if value.Rank < 1 || value.Rank > math.MaxInt16 || invalidMetric {
				return planning.ErrValidation
			}
			driverID, parseErr := uuid(value.DriverID)
			if parseErr != nil {
				return planning.ErrValidation
			}
			resourceIDs, parseErr := uuidSlice(value.ResourceIDs)
			if parseErr != nil {
				return planning.ErrValidation
			}
			components, marshalErr := json.Marshal(value.Components)
			if marshalErr != nil {
				return marshalErr
			}
			score := pgtype.Numeric{}
			if scanErr := score.Scan(strconv.FormatFloat(value.Score, 'f', 2, 64)); scanErr != nil {
				return scanErr
			}
			// #nosec G115 -- distance and duration are checked against the int32 range above.
			distance := int32(value.DistanceMeters)
			// #nosec G115 -- duration is checked against the int32 range above.
			duration := int32(value.DurationSeconds)
			rank := int16(value.Rank)
			id, insertErr := q.InsertPlanningSuggestion(ctx, dbgen.InsertPlanningSuggestionParams{RunID: parsedRun, Rank: rank, StartsAt: timestamp(value.StartsAt), EndsAt: timestamp(value.EndsAt), DriverID: driverID, ResourceIds: resourceIDs, ResourcePurposes: value.ResourcePurposes, Score: score, Components: components, Reasons: value.Reasons, Warnings: value.Warnings, RoutingSource: value.RoutingSource, DistanceMeters: &distance, DurationSeconds: &duration})
			if insertErr != nil {
				return insertErr
			}
			_ = id
		}
		return insertAudit(ctx, q, actor, "planning.suggestions_created", "planning_run", runID, "", []string{"job_id", "input_versions", "suggestions", "score_components", "expiry"})
	})
	if err != nil {
		return planning.Run{}, err
	}
	return s.ListRun(ctx, runID)
}

func (s *PlanningStore) ListRun(ctx context.Context, runID string) (planning.Run, error) {
	parsed, err := uuid(runID)
	if err != nil {
		return planning.Run{}, planning.ErrNotFound
	}
	rows, err := s.queries.ListPlanningSuggestions(ctx, parsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return planning.Run{}, planning.ErrNotFound
	}
	if err != nil {
		return planning.Run{}, err
	}
	if len(rows) == 0 {
		return planning.Run{}, planning.ErrNotFound
	}
	result := planning.Run{ID: runID, JobID: rows[0].RJobID, CreatedAt: rows[0].CreatedAt.Time.UTC(), ExpiresAt: rows[0].ExpiresAt.Time.UTC()}
	var runSnapshot planning.RunSnapshot
	if err := json.Unmarshal(rows[0].ConfigSnapshot, &runSnapshot); err != nil {
		return planning.Run{}, errors.New("planning: invalid stored run snapshot")
	}
	// Runs created before the explanatory envelope stored Config directly.
	if runSnapshot.Config.HorizonDays == 0 {
		if err := json.Unmarshal(rows[0].ConfigSnapshot, &runSnapshot.Config); err != nil {
			return planning.Run{}, errors.New("planning: invalid stored config")
		}
	}
	result.Exclusions = runSnapshot.Exclusions
	result.HorizonDays = runSnapshot.Config.HorizonDays
	result.CandidateLimit = runSnapshot.Config.CandidateLimit
	for _, row := range rows {
		var component planning.Component
		if json.Unmarshal(row.Components, &component) != nil {
			return planning.Run{}, errors.New("planning: invalid stored components")
		}
		score, parseErr := strconv.ParseFloat(row.SScore, 64)
		if parseErr != nil {
			return planning.Run{}, parseErr
		}
		distance, duration := 0, 0
		if row.DistanceMeters != nil {
			distance = int(*row.DistanceMeters)
		}
		if row.DurationSeconds != nil {
			duration = int(*row.DurationSeconds)
		}
		result.Suggestions = append(result.Suggestions, planning.Suggestion{ID: row.SID, RunID: row.SRunID, Rank: int(row.Rank), StartsAt: row.StartsAt.Time.UTC(), EndsAt: row.EndsAt.Time.UTC(), DriverID: row.SDriverID, DriverName: row.DriverName, ResourceIDs: row.ResourceIds, ResourcePurposes: row.ResourcePurposes, ResourceNames: row.ResourceNames, Score: score, Components: component, Reasons: row.Reasons, Warnings: row.Warnings, RoutingSource: row.RoutingSource, DistanceMeters: distance, DurationSeconds: duration, Status: row.Status, JobID: row.RJobID, JobVersion: row.JobVersion, WaitlistVersion: row.WaitlistVersion, CreatedAt: row.CreatedAt.Time.UTC(), ExpiresAt: row.ExpiresAt.Time.UTC()})
	}
	return result, nil
}

func (s *PlanningStore) Adopt(ctx context.Context, actor auth.Actor, suggestionID, requestID string) (appointmentID string, resultErr error) {
	parsedSuggestion, err := uuid(suggestionID)
	if err != nil {
		return "", planning.ErrNotFound
	}
	resultErr = withQueries(ctx, s.pool, func(q *dbgen.Queries) error {
		if lockErr := q.LockSchedulingMutation(ctx); lockErr != nil {
			return lockErr
		}
		row, getErr := q.GetPlanningSuggestionForUpdate(ctx, parsedSuggestion)
		if errors.Is(getErr, pgx.ErrNoRows) {
			return planning.ErrNotFound
		}
		if getErr != nil {
			return getErr
		}
		if row.Status != "pending" || !row.ExpiresAt.Time.After(s.now().UTC()) || row.WorkflowStatus != "waitlist" || row.JobVersion != row.CurrentJobVersion || row.WaitlistVersion != row.CurrentWaitlistVersion {
			return planning.ErrConflict
		}
		driverID, parseErr := uuid(row.SDriverID)
		if parseErr != nil {
			return planning.ErrConflict
		}
		if _, lockErr := q.LockPlanningDriver(ctx, driverID); lockErr != nil {
			return planning.ErrConflict
		}
		resourceIDs, parseErr := uuidSlice(row.ResourceIds)
		if parseErr != nil {
			return planning.ErrConflict
		}
		lockedResources, lockErr := q.LockPlanningResources(ctx, resourceIDs)
		if lockErr != nil || len(lockedResources) != len(resourceIDs) {
			return planning.ErrConflict
		}
		fingerprint, fingerprintErr := q.CurrentPlanningInputFingerprint(ctx, dbgen.CurrentPlanningInputFingerprintParams{
			JobID: mustUUID(row.RJobID), SearchFrom: row.SearchFrom, SearchTo: row.SearchTo,
		})
		if fingerprintErr != nil || !bytes.Equal(fingerprint, row.InputFingerprint) {
			return planning.ErrConflict
		}
		driverAvailable, availabilityErr := q.PlanningDriverAvailable(ctx, dbgen.PlanningDriverAvailableParams{
			DriverID: driverID, StartsAt: row.StartsAt, EndsAt: row.EndsAt,
		})
		if availabilityErr != nil || !driverAvailable {
			return planning.ErrConflict
		}
		jobID, _ := uuid(row.RJobID)
		created, insertErr := q.InsertAdoptedProposal(ctx, dbgen.InsertAdoptedProposalParams{JobID: jobID, StartsAt: row.StartsAt, EndsAt: row.EndsAt})
		if insertErr != nil {
			return mapPlanningError(insertErr)
		}
		appointmentID = created
		appointmentUUID, _ := uuid(created)
		rows, insertErr := q.InsertAppointmentDriver(ctx, dbgen.InsertAppointmentDriverParams{AppointmentID: appointmentUUID, DriverID: driverID, IsPrimary: true})
		if insertErr != nil || rows != 1 {
			if insertErr != nil {
				return mapPlanningError(insertErr)
			}
			return planning.ErrConflict
		}
		if len(row.ResourceIds) != len(row.ResourcePurposes) {
			return planning.ErrConflict
		}
		for index, id := range row.ResourceIds {
			resourceID, parseErr := uuid(id)
			if parseErr != nil {
				return planning.ErrConflict
			}
			rows, insertErr = q.InsertAppointmentResource(ctx, dbgen.InsertAppointmentResourceParams{AppointmentID: appointmentUUID, ResourceID: resourceID, Purpose: row.ResourcePurposes[index]})
			if insertErr != nil || rows != 1 {
				if insertErr != nil {
					return mapPlanningError(insertErr)
				}
				return planning.ErrConflict
			}
		}
		ready, readyErr := q.AppointmentAssignmentsReady(ctx, dbgen.AppointmentAssignmentsReadyParams{AppointmentID: appointmentUUID, JobType: row.JobType, TransportMode: row.TransportMode, ExternalTransportConfirmed: row.ExternalTransportConfirmed})
		if readyErr != nil {
			return readyErr
		}
		if !ready {
			return planning.ErrConflict
		}
		if err := q.SetJobWorkflow(ctx, dbgen.SetJobWorkflowParams{WorkflowStatus: "planning", JobID: jobID}); err != nil {
			return err
		}
		rows, err = q.MarkPlanningSuggestionAdopted(ctx, dbgen.MarkPlanningSuggestionAdoptedParams{AppointmentID: appointmentUUID, ID: parsedSuggestion})
		if err != nil || rows != 1 {
			return planning.ErrConflict
		}
		runID, _ := uuid(row.SRunID)
		if err := q.DiscardOtherPlanningSuggestions(ctx, dbgen.DiscardOtherPlanningSuggestionsParams{RunID: runID, AdoptedID: parsedSuggestion}); err != nil {
			return err
		}
		return insertAudit(ctx, q, actor, "planning.suggestion_adopted", "appointment", appointmentID, requestID, []string{"planning_suggestion_id", "lifecycle_status", "time_range", "assignments"})
	})
	return appointmentID, resultErr
}

func coordinates(latitude, longitude string) (float64, float64, error) {
	if latitude == "" || longitude == "" {
		return 0, 0, nil
	}
	lat, err := strconv.ParseFloat(latitude, 64)
	if err != nil {
		return 0, 0, err
	}
	lon, err := strconv.ParseFloat(longitude, 64)
	return lat, lon, err
}
func mapPlanningError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23P01", "23505", "40001", "40P01":
			return fmt.Errorf("%w: concurrent reservation", planning.ErrConflict)
		case "23503", "23514", "22P02":
			return planning.ErrValidation
		}
	}
	return err
}

func (s *PlanningStore) ClusterEntries(ctx context.Context) ([]planning.ClusterEntry, error) {
	rows, err := s.queries.ListPlanningClusterEntries(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]planning.ClusterEntry, 0, len(rows))
	for _, row := range rows {
		latitude, longitude, parseErr := coordinates(row.Latitude, row.Longitude)
		if parseErr != nil {
			return nil, parseErr
		}
		result = append(result, planning.ClusterEntry{JobID: row.JobID, Region: row.Region, Location: planning.Point{Latitude: latitude, Longitude: longitude}})
	}
	return result, nil
}
