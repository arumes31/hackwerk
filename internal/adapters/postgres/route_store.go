package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres/dbgen"
	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/planning"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RouteStore persists route drafts and adopts them into proposal appointments.
type RouteStore struct {
	pool    *pgxpool.Pool
	queries *dbgen.Queries
}

func NewRouteStore(pool *pgxpool.Pool) *RouteStore {
	return &RouteStore{pool: pool, queries: dbgen.New(pool)}
}

func (s *RouteStore) LoadRouteCandidates(ctx context.Context, jobIDs []string) ([]planning.RouteCandidate, error) {
	ids, err := uuidSlice(jobIDs)
	if err != nil {
		return nil, planning.ErrValidation
	}
	rows, err := s.queries.ListRouteCandidates(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := make([]planning.RouteCandidate, 0, len(rows))
	for _, row := range rows {
		latitude, longitude, parseErr := coordinates(row.Latitude, row.Longitude)
		if parseErr != nil {
			return nil, planning.ErrValidation
		}
		location := planning.Point{Latitude: latitude, Longitude: longitude}
		if !location.Valid() || row.JobVersion < 1 || row.WaitlistVersion < 1 || row.EstimatedHackMinutes <= 0 {
			return nil, planning.ErrValidation
		}
		unavailableReason := ""
		if row.HasActiveAppointment {
			unavailableReason = "Bereits eingeplant"
		}
		result = append(result, planning.RouteCandidate{
			JobID: row.JobID, JobNumber: row.JobNumber,
			CustomerName: row.CustomerName, Region: row.Region,
			Locality: row.Locality, VolumeM3: row.JVolumeM3,
			JobType: row.JobType, TransportMode: row.TransportMode,
			UnavailableReason:          unavailableReason,
			ExternalTransportConfirmed: row.ExternalTransportConfirmed,
			Location:                   location,
			WorkDuration:               time.Duration(row.EstimatedHackMinutes+row.EstimatedTransportMinutes) * time.Minute,
			JobVersion:                 row.JobVersion,
			WaitlistVersion:            row.WaitlistVersion,
		})
	}
	return result, nil
}

func (s *RouteStore) LoadRouteMissingLocations(ctx context.Context) ([]planning.RouteMissingLocation, error) {
	rows, err := s.queries.ListRouteMissingLocations(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]planning.RouteMissingLocation, 0, len(rows))
	for _, row := range rows {
		result = append(result, planning.RouteMissingLocation{
			JobID: row.JobID, JobNumber: row.JobNumber, CustomerName: row.CustomerName, Region: row.Region,
		})
	}
	return result, nil
}

func (s *RouteStore) LoadRouteOptions(ctx context.Context) (planning.RouteOptions, error) {
	drivers, err := s.queries.ListRouteDrivers(ctx)
	if err != nil {
		return planning.RouteOptions{}, err
	}
	resources, err := s.queries.ListRouteResources(ctx)
	if err != nil {
		return planning.RouteOptions{}, err
	}
	result := planning.RouteOptions{
		Drivers:   make([]planning.RouteDriverOption, 0, len(drivers)),
		Resources: make([]planning.RouteResourceOption, 0, len(resources)),
	}
	for _, row := range drivers {
		result.Drivers = append(result.Drivers, planning.RouteDriverOption{ID: row.ID, Name: row.DisplayName})
	}
	for _, row := range resources {
		result.Resources = append(result.Resources, planning.RouteResourceOption{
			ID: row.ID, Name: row.Name, Type: row.ResourceType, Exclusive: row.Exclusive,
		})
	}
	return result, nil
}

func (s *RouteStore) ListDraftRouteIDsForDate(ctx context.Context, localDate string) ([]string, error) {
	value, err := dateValue(localDate)
	if err != nil {
		return nil, planning.ErrValidation
	}
	return s.queries.ListDraftRouteIDsForDate(ctx, value)
}

func (s *RouteStore) SaveMovedDraftStop(ctx context.Context, actor auth.Actor, input planning.SaveMovedDraftStopInput) error {
	sourceValues, err := prepareRouteDraftValues(actor, input.Source)
	if err != nil {
		return err
	}
	targetValues, err := prepareRouteDraftValues(actor, input.Target)
	if err != nil {
		return err
	}
	ids := []string{input.Source.ID, input.Target.ID}
	slices.Sort(ids)
	return withQueries(ctx, s.pool, func(q *dbgen.Queries) error {
		locked := make(map[string]dbgen.LockRouteDraftRow, 2)
		for _, id := range ids {
			parsed, parseErr := uuid(id)
			if parseErr != nil {
				return planning.ErrNotFound
			}
			row, lockErr := q.LockRouteDraft(ctx, parsed)
			if errors.Is(lockErr, pgx.ErrNoRows) {
				return planning.ErrNotFound
			}
			if lockErr != nil {
				return lockErr
			}
			locked[id] = row
		}
		if locked[input.Source.ID].Status != string(planning.RouteStatusDraft) || locked[input.Target.ID].Status != string(planning.RouteStatusDraft) || locked[input.Source.ID].Version != input.SourceVersion || locked[input.Target.ID].Version != input.TargetVersion {
			return planning.ErrConflict
		}
		for _, route := range []struct {
			value    planning.RouteDraft
			prepared routeDraftValues
			version  int32
		}{{input.Source, sourceValues, input.SourceVersion}, {input.Target, targetValues, input.TargetVersion}} {
			if err := lockRouteSelections(ctx, q, route.value); err != nil {
				return err
			}
			id, _ := uuid(route.value.ID)
			rows, updateErr := q.UpdateRouteDraft(ctx, route.prepared.updateParams(id, route.version))
			if updateErr != nil {
				return mapRouteError(updateErr)
			}
			if rows != 1 {
				return planning.ErrConflict
			}
			if err := q.DeleteRouteStops(ctx, id); err != nil {
				return err
			}
			if err := insertRouteStops(ctx, q, id, route.value.Stops); err != nil {
				return err
			}
		}
		return insertAudit(ctx, q, actor, "route.stop_moved", "route_draft", input.Source.ID, input.RequestID, []string{"source_route_id", "target_route_id", "stop_id", "routing_metrics"})
	})
}

func (s *RouteStore) SaveRouteDraft(ctx context.Context, actor auth.Actor, input planning.SaveRouteDraftInput) (planning.RouteDraft, error) {
	route := input.Route
	values, err := prepareRouteDraftValues(actor, route)
	if err != nil {
		return planning.RouteDraft{}, err
	}
	resultErr := withQueries(ctx, s.pool, func(q *dbgen.Queries) error {
		action := "route.created"
		if route.ID == "" {
			if err := lockRouteSelections(ctx, q, route); err != nil {
				return err
			}
			created, insertErr := q.InsertRouteDraft(ctx, values.insertParams())
			if insertErr != nil {
				return mapRouteError(insertErr)
			}
			route.ID = created.ID
			route.Version = created.Version
		} else {
			routeID, parseErr := uuid(route.ID)
			if parseErr != nil {
				return planning.ErrNotFound
			}
			current, lockErr := q.LockRouteDraft(ctx, routeID)
			if errors.Is(lockErr, pgx.ErrNoRows) {
				return planning.ErrNotFound
			}
			if lockErr != nil {
				return lockErr
			}
			if current.Status != string(planning.RouteStatusDraft) || current.Version != input.ExpectedVersion {
				return planning.ErrConflict
			}
			if err := lockRouteSelections(ctx, q, route); err != nil {
				return err
			}
			rows, updateErr := q.UpdateRouteDraft(ctx, values.updateParams(routeID, input.ExpectedVersion))
			if updateErr != nil {
				return mapRouteError(updateErr)
			}
			if rows != 1 {
				return planning.ErrConflict
			}
			if err := q.DeleteRouteStops(ctx, routeID); err != nil {
				return err
			}
			route.Version = input.ExpectedVersion + 1
			action = "route.updated"
		}
		routeID, _ := uuid(route.ID)
		if err := insertRouteStops(ctx, q, routeID, route.Stops); err != nil {
			return err
		}
		return insertAudit(ctx, q, actor, action, "route_draft", route.ID, input.RequestID,
			[]string{"driver_id", "resource_ids", "departure_at", "route_endpoints", "routing_metrics", "stops"})
	})
	if resultErr != nil {
		return planning.RouteDraft{}, resultErr
	}
	return s.GetRoute(ctx, route.ID)
}

func (s *RouteStore) GetRoute(ctx context.Context, id string) (planning.RouteDraft, error) {
	parsed, err := uuid(id)
	if err != nil {
		return planning.RouteDraft{}, planning.ErrNotFound
	}
	route, err := getRoute(ctx, s.queries, parsed)
	if err != nil {
		return planning.RouteDraft{}, err
	}
	if err := s.loadRouteStopPhones(ctx, &route); err != nil {
		return planning.RouteDraft{}, err
	}
	return route, nil
}

func (s *RouteStore) loadRouteStopPhones(ctx context.Context, route *planning.RouteDraft) error {
	if len(route.Stops) == 0 {
		return nil
	}
	jobIDs := make([]string, 0, len(route.Stops))
	for _, stop := range route.Stops {
		jobIDs = append(jobIDs, stop.JobID)
	}
	ids, err := uuidSlice(jobIDs)
	if err != nil {
		return planning.ErrValidation
	}
	rows, err := s.queries.ListRouteStopPhones(ctx, ids)
	if err != nil {
		return err
	}
	phones := make(map[string]string, len(route.Stops))
	for _, row := range rows {
		phones[row.JobID] = row.CustomerPhone
	}
	for index := range route.Stops {
		route.Stops[index].CustomerPhone = phones[route.Stops[index].JobID]
	}
	return nil
}

func (s *RouteStore) AssignRoute(ctx context.Context, actor auth.Actor, input planning.AssignRouteInput) (planning.RouteDraft, error) {
	routeID, err := uuid(input.ID)
	if err != nil {
		return planning.RouteDraft{}, planning.ErrNotFound
	}
	resultErr := withQueries(ctx, s.pool, func(q *dbgen.Queries) error {
		if err := q.LockSchedulingMutation(ctx); err != nil {
			return err
		}
		draft, lockErr := q.LockRouteDraft(ctx, routeID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return planning.ErrNotFound
		}
		if lockErr != nil {
			return lockErr
		}
		if draft.Status != string(planning.RouteStatusDraft) || draft.Version != input.ExpectedVersion {
			return planning.ErrConflict
		}
		route := planning.RouteDraft{
			ID: draft.RdID, DriverID: draft.RdDriverID,
			ChipperResourceID: draft.RdChipperResourceID, TransportResourceID: draft.TransportResourceID,
		}
		if err := lockRouteSelections(ctx, q, route); err != nil {
			return err
		}
		storedStops, listErr := q.ListRouteStops(ctx, routeID)
		if listErr != nil {
			return listErr
		}
		stops, stopsErr := q.LockRouteStopsForAssignment(ctx, routeID)
		if stopsErr != nil {
			return stopsErr
		}
		if len(stops) == 0 || len(stops) != len(storedStops) {
			return planning.ErrConflict
		}
		var inboundTravelSeconds int64
		for _, stop := range stops {
			inboundTravelSeconds += int64(stop.TravelDurationSeconds)
		}
		if inboundTravelSeconds > int64(draft.DurationSeconds) {
			return planning.ErrConflict
		}
		returnBufferMinutes, bufferErr := routeTravelBufferMinutes(int64(draft.DurationSeconds) - inboundTravelSeconds)
		if bufferErr != nil {
			return bufferErr
		}
		driverID, _ := uuid(draft.RdDriverID)
		var chipperID pgtype.UUID
		if draft.RdChipperResourceID != "" {
			chipperID, _ = uuid(draft.RdChipperResourceID)
		}
		var transportID pgtype.UUID
		if draft.TransportResourceID != "" {
			transportID, _ = uuid(draft.TransportResourceID)
		}
		reservationCursor := draft.DepartureAt.Time
		for index, stop := range stops {
			if err := validateRouteStopForAssignment(stop, transportID.Valid); err != nil {
				return err
			}
			beforeMinutes, bufferErr := routeTravelBufferMinutes(int64(stop.TravelDurationSeconds))
			if bufferErr != nil {
				return bufferErr
			}
			reservedStartsAt := stop.PlannedStartsAt.Time.Add(-time.Duration(beforeMinutes) * time.Minute)
			if !reservedStartsAt.Equal(reservationCursor) {
				return planning.ErrConflict
			}
			afterMinutes := int32(0)
			if index == len(stops)-1 {
				afterMinutes = returnBufferMinutes
			}
			reservedEndsAt := stop.PlannedEndsAt.Time.Add(time.Duration(afterMinutes) * time.Minute)
			available, availabilityErr := q.PlanningDriverAvailable(ctx, dbgen.PlanningDriverAvailableParams{
				DriverID: driverID, StartsAt: timestamp(reservedStartsAt), EndsAt: timestamp(reservedEndsAt),
			})
			if availabilityErr != nil {
				return availabilityErr
			}
			if !available {
				return planning.ErrConflict
			}
			jobID, _ := uuid(stop.RsJobID)
			appointmentID, insertErr := q.InsertAdoptedProposal(ctx, dbgen.InsertAdoptedProposalParams{
				JobID: jobID, StartsAt: stop.PlannedStartsAt, EndsAt: stop.PlannedEndsAt,
				BufferBeforeMinutes: beforeMinutes, BufferAfterMinutes: afterMinutes,
			})
			if insertErr != nil {
				return mapRouteError(insertErr)
			}
			appointmentUUID, _ := uuid(appointmentID)
			rows, insertErr := q.InsertAppointmentDriver(ctx, dbgen.InsertAppointmentDriverParams{
				AppointmentID: appointmentUUID, DriverID: driverID, IsPrimary: true,
			})
			if insertErr != nil {
				return mapRouteError(insertErr)
			}
			if rows != 1 {
				return planning.ErrConflict
			}
			if chipperID.Valid {
				rows, insertErr = q.InsertAppointmentResource(ctx, dbgen.InsertAppointmentResourceParams{
					AppointmentID: appointmentUUID, ResourceID: chipperID, Purpose: "chipping",
				})
				if insertErr != nil {
					return mapRouteError(insertErr)
				}
				if rows != 1 {
					return planning.ErrConflict
				}
			}
			if transportID.Valid {
				rows, insertErr = q.InsertAppointmentResource(ctx, dbgen.InsertAppointmentResourceParams{
					AppointmentID: appointmentUUID, ResourceID: transportID, Purpose: "transport",
				})
				if insertErr != nil {
					return mapRouteError(insertErr)
				}
				if rows != 1 {
					return planning.ErrConflict
				}
			}
			if chipperID.Valid {
				ready, readyErr := q.AppointmentAssignmentsReady(ctx, dbgen.AppointmentAssignmentsReadyParams{
					AppointmentID: appointmentUUID, JobType: stop.JobType, TransportMode: stop.TransportMode,
					ExternalTransportConfirmed: stop.ExternalTransportConfirmed, AllowMissingChipper: false,
				})
				if readyErr != nil {
					return readyErr
				}
				if !ready {
					return planning.ErrConflict
				}
			}
			if err := q.SetJobWorkflow(ctx, dbgen.SetJobWorkflowParams{WorkflowStatus: "planning", JobID: jobID}); err != nil {
				return err
			}
			stopID, _ := uuid(stop.RsID)
			rows, linkErr := q.LinkRouteStopAppointment(ctx, dbgen.LinkRouteStopAppointmentParams{
				AppointmentID: appointmentUUID, ID: stopID, RouteDraftID: routeID,
			})
			if linkErr != nil {
				return mapRouteError(linkErr)
			}
			if rows != 1 {
				return planning.ErrConflict
			}
			reservationCursor = stop.PlannedEndsAt.Time
		}
		rows, setErr := q.SetRouteDraftAssigned(ctx, dbgen.SetRouteDraftAssignedParams{
			ID: routeID, ExpectedVersion: input.ExpectedVersion,
		})
		if setErr != nil {
			return mapRouteError(setErr)
		}
		if rows != 1 {
			return planning.ErrConflict
		}
		return insertAudit(ctx, q, actor, "route.assigned", "route_draft", input.ID, input.RequestID,
			[]string{"status", "appointment_proposals", "assignments"})
	})
	if resultErr != nil {
		return planning.RouteDraft{}, resultErr
	}
	return s.GetRoute(ctx, input.ID)
}

func routeTravelBufferMinutes(seconds int64) (int32, error) {
	if seconds < 0 {
		return 0, planning.ErrConflict
	}
	minutes := seconds / 60
	if seconds%60 != 0 {
		minutes++
	}
	if minutes > math.MaxInt32 {
		return 0, planning.ErrConflict
	}
	return int32(minutes), nil
}

func (s *RouteStore) SaveRouteOrder(ctx context.Context, actor auth.Actor, input planning.SaveRouteOrderInput) (planning.RouteDraft, error) {
	routeID, err := uuid(input.Route.ID)
	if err != nil {
		return planning.RouteDraft{}, planning.ErrNotFound
	}
	geometry, err := encodeRouteGeometry(input.Route.Directions.Geometry)
	if err != nil {
		return planning.RouteDraft{}, err
	}
	distance, duration, err := routeMetrics(input.Route.Directions)
	if err != nil {
		return planning.RouteDraft{}, err
	}
	resultErr := withQueries(ctx, s.pool, func(q *dbgen.Queries) error {
		current, lockErr := q.LockRouteDraft(ctx, routeID)
		if errors.Is(lockErr, pgx.ErrNoRows) {
			return planning.ErrNotFound
		}
		if lockErr != nil {
			return lockErr
		}
		if actor.Role != auth.RoleDriver || actor.DriverID == "" || current.RdDriverID != actor.DriverID {
			return auth.ErrForbidden
		}
		if current.Status != string(planning.RouteStatusAssigned) || current.Version != input.ExpectedVersion {
			return planning.ErrConflict
		}
		stored, listErr := q.ListRouteStops(ctx, routeID)
		if listErr != nil {
			return listErr
		}
		if err := validateRouteOrder(stored, input.Route.Stops, input.StopIDs); err != nil {
			return err
		}
		byID := make(map[string]planning.RouteStop, len(input.Route.Stops))
		for _, stop := range input.Route.Stops {
			byID[stop.ID] = stop
		}
		for index, stopIDText := range input.StopIDs {
			stopID, _ := uuid(stopIDText)
			stop := byID[stopIDText]
			legDistance, metricErr := nonnegativeInt32(stop.LegDistanceMeters)
			if metricErr != nil {
				return metricErr
			}
			legDuration, metricErr := durationSeconds(stop.LegDuration)
			if metricErr != nil {
				return metricErr
			}
			rows, updateErr := q.UpdateRouteStopPosition(ctx, dbgen.UpdateRouteStopPositionParams{
				Position: int32(index + 1), ID: stopID, RouteDraftID: routeID,
			})
			if updateErr != nil {
				return mapRouteError(updateErr)
			}
			if rows != 1 {
				return planning.ErrConflict
			}
			rows, updateErr = q.UpdateRouteStopTravel(ctx, dbgen.UpdateRouteStopTravelParams{
				TravelDistanceMeters: legDistance, TravelDurationSeconds: legDuration,
				ID: stopID, RouteDraftID: routeID,
			})
			if updateErr != nil {
				return mapRouteError(updateErr)
			}
			if rows != 1 {
				return planning.ErrConflict
			}
		}
		rows, updateErr := q.UpdateRouteDraftMetrics(ctx, dbgen.UpdateRouteDraftMetricsParams{
			RoutingSource: input.Route.Directions.Source, DistanceMeters: distance,
			DurationSeconds: duration, RouteGeometry: geometry, ID: routeID,
			ExpectedVersion: input.ExpectedVersion,
		})
		if updateErr != nil {
			return mapRouteError(updateErr)
		}
		if rows != 1 {
			return planning.ErrConflict
		}
		return insertAudit(ctx, q, actor, "route.reordered", "route_draft", input.Route.ID, input.RequestID,
			[]string{"stop_order", "routing_metrics"})
	})
	if resultErr != nil {
		return planning.RouteDraft{}, resultErr
	}
	return s.GetRoute(ctx, input.Route.ID)
}

func (s *RouteStore) LatestAssignedRouteForDriver(ctx context.Context, driverID, localDate string) (planning.RouteDraft, error) {
	parsedDriverID, err := uuid(driverID)
	if err != nil {
		return planning.RouteDraft{}, planning.ErrNotFound
	}
	var date pgtype.Date
	if err := date.Scan(localDate); err != nil || !date.Valid {
		return planning.RouteDraft{}, planning.ErrValidation
	}
	id, err := s.queries.LatestAssignedRouteForDriver(ctx, dbgen.LatestAssignedRouteForDriverParams{
		DriverID: parsedDriverID, LocalDate: date,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return planning.RouteDraft{}, planning.ErrNotFound
	}
	if err != nil {
		return planning.RouteDraft{}, err
	}
	return s.GetRoute(ctx, id)
}

type routeDraftValues struct {
	actorID, driverID, chipperID pgtype.UUID
	transportID                  string
	departure                    pgtype.Timestamptz
	startLabel                   string
	startLatitude                pgtype.Numeric
	startLongitude               pgtype.Numeric
	endLabel                     string
	endLatitude                  pgtype.Numeric
	endLongitude                 pgtype.Numeric
	routingSource                string
	distanceMeters               int32
	durationSeconds              int32
	geometry                     []byte
}

func prepareRouteDraftValues(actor auth.Actor, route planning.RouteDraft) (routeDraftValues, error) {
	actorID, err := uuid(actor.UserID)
	if err != nil {
		return routeDraftValues{}, planning.ErrValidation
	}
	driverID, err := uuid(route.DriverID)
	if err != nil {
		return routeDraftValues{}, planning.ErrValidation
	}
	var chipperID pgtype.UUID
	if route.ChipperResourceID != "" {
		chipperID, err = uuid(route.ChipperResourceID)
		if err != nil {
			return routeDraftValues{}, planning.ErrValidation
		}
	}
	if route.TransportResourceID != "" {
		if _, err := uuid(route.TransportResourceID); err != nil {
			return routeDraftValues{}, planning.ErrValidation
		}
	}
	startLatitude, err := coordinateNumeric(route.Start.Latitude)
	if err != nil {
		return routeDraftValues{}, err
	}
	startLongitude, err := coordinateNumeric(route.Start.Longitude)
	if err != nil {
		return routeDraftValues{}, err
	}
	endLatitude, err := coordinateNumeric(route.End.Latitude)
	if err != nil {
		return routeDraftValues{}, err
	}
	endLongitude, err := coordinateNumeric(route.End.Longitude)
	if err != nil {
		return routeDraftValues{}, err
	}
	geometry, err := encodeRouteGeometry(route.Directions.Geometry)
	if err != nil {
		return routeDraftValues{}, err
	}
	distance, duration, err := routeMetrics(route.Directions)
	if err != nil {
		return routeDraftValues{}, err
	}
	if route.Status != planning.RouteStatusDraft || route.Departure.IsZero() || len(route.Stops) == 0 {
		return routeDraftValues{}, planning.ErrValidation
	}
	return routeDraftValues{
		actorID: actorID, driverID: driverID, chipperID: chipperID,
		transportID: route.TransportResourceID, departure: timestamp(route.Departure.UTC()),
		startLabel: strings.TrimSpace(route.StartLabel), endLabel: strings.TrimSpace(route.EndLabel),
		startLatitude: startLatitude, startLongitude: startLongitude,
		endLatitude: endLatitude, endLongitude: endLongitude,
		routingSource: route.Directions.Source, distanceMeters: distance,
		durationSeconds: duration, geometry: geometry,
	}, nil
}

func (v routeDraftValues) insertParams() dbgen.InsertRouteDraftParams {
	return dbgen.InsertRouteDraftParams{
		ActorUserID: v.actorID, DriverID: v.driverID, ChipperResourceID: v.chipperID,
		TransportResourceID: v.transportID, DepartureAt: v.departure,
		StartLabel:    v.startLabel,
		StartLatitude: v.startLatitude, StartLongitude: v.startLongitude,
		EndLabel:    v.endLabel,
		EndLatitude: v.endLatitude, EndLongitude: v.endLongitude,
		RoutingSource: v.routingSource, DistanceMeters: v.distanceMeters,
		DurationSeconds: v.durationSeconds, RouteGeometry: v.geometry,
	}
}

func (v routeDraftValues) updateParams(id pgtype.UUID, version int32) dbgen.UpdateRouteDraftParams {
	return dbgen.UpdateRouteDraftParams{
		ActorUserID: v.actorID, DriverID: v.driverID, ChipperResourceID: v.chipperID,
		TransportResourceID: v.transportID, DepartureAt: v.departure,
		StartLabel:    v.startLabel,
		StartLatitude: v.startLatitude, StartLongitude: v.startLongitude,
		EndLabel:    v.endLabel,
		EndLatitude: v.endLatitude, EndLongitude: v.endLongitude,
		RoutingSource: v.routingSource, DistanceMeters: v.distanceMeters,
		DurationSeconds: v.durationSeconds, RouteGeometry: v.geometry,
		ID: id, ExpectedVersion: version,
	}
}

func insertRouteStops(ctx context.Context, q *dbgen.Queries, routeID pgtype.UUID, stops []planning.RouteStop) error {
	for index, stop := range stops {
		jobID, err := uuid(stop.JobID)
		if err != nil || stop.JobVersion < 1 || stop.WaitlistVersion < 1 || stop.StartsAt.IsZero() || !stop.EndsAt.After(stop.StartsAt) {
			return planning.ErrValidation
		}
		distance, err := nonnegativeInt32(stop.LegDistanceMeters)
		if err != nil {
			return err
		}
		duration, err := durationSeconds(stop.LegDuration)
		if err != nil {
			return err
		}
		_, err = q.InsertRouteStop(ctx, dbgen.InsertRouteStopParams{
			RouteDraftID: routeID, JobID: jobID, JobVersion: stop.JobVersion,
			WaitlistVersion: stop.WaitlistVersion, Position: int32(index + 1),
			TravelDistanceMeters: distance, TravelDurationSeconds: duration,
			PlannedStartsAt: timestamp(stop.StartsAt.UTC()), PlannedEndsAt: timestamp(stop.EndsAt.UTC()),
		})
		if err != nil {
			return mapRouteError(err)
		}
	}
	return nil
}

func lockRouteSelections(ctx context.Context, q *dbgen.Queries, route planning.RouteDraft) error {
	driverID, err := uuid(route.DriverID)
	if err != nil {
		return planning.ErrValidation
	}
	if _, err := q.LockPlanningDriver(ctx, driverID); errors.Is(err, pgx.ErrNoRows) {
		return planning.ErrConflict
	} else if err != nil {
		return err
	}
	drivers, err := q.ListRouteDrivers(ctx)
	if err != nil {
		return err
	}
	driverReady := false
	for _, candidate := range drivers {
		if candidate.ID == route.DriverID {
			driverReady = true
			break
		}
	}
	if !driverReady {
		return planning.ErrConflict
	}
	resourceTexts := make([]string, 0, 2)
	if route.ChipperResourceID != "" {
		resourceTexts = append(resourceTexts, route.ChipperResourceID)
	}
	if route.TransportResourceID != "" {
		resourceTexts = append(resourceTexts, route.TransportResourceID)
	}
	resourceIDs, err := uuidSlice(resourceTexts)
	if err != nil || (route.ChipperResourceID != "" && route.ChipperResourceID == route.TransportResourceID) {
		return planning.ErrValidation
	}
	if len(resourceIDs) > 0 {
		locked, lockErr := q.LockPlanningResources(ctx, resourceIDs)
		if lockErr != nil {
			return lockErr
		}
		if len(locked) != len(resourceIDs) {
			return planning.ErrConflict
		}
	}
	resources, err := q.ListRouteResources(ctx)
	if err != nil {
		return err
	}
	types := make(map[string]string, len(resources))
	for _, resource := range resources {
		types[resource.ID] = resource.ResourceType
	}
	if route.ChipperResourceID != "" && types[route.ChipperResourceID] != "chipper" {
		return planning.ErrConflict
	}
	if route.TransportResourceID != "" && types[route.TransportResourceID] != "transport_vehicle" {
		return planning.ErrConflict
	}
	return nil
}

func validateRouteStopForAssignment(stop dbgen.LockRouteStopsForAssignmentRow, hasTransportResource bool) error {
	if stop.ArchivedAt.Valid || stop.WaitlistID == "" || stop.JobVersion != stop.CurrentJobVersion ||
		stop.WaitlistVersion != stop.CurrentWaitlistVersion ||
		(stop.WorkflowStatus != "waitlist" && stop.WorkflowStatus != "planning") ||
		stop.Latitude == "" || stop.Longitude == "" || !stop.PlannedEndsAt.Time.After(stop.PlannedStartsAt.Time) {
		return planning.ErrConflict
	}
	switch stop.JobType {
	case "chipping_only":
		return nil
	case "chipping_with_transport":
		if stop.TransportMode == "internal" && hasTransportResource {
			return nil
		}
		if stop.TransportMode == "external" && stop.ExternalTransportConfirmed {
			return nil
		}
	}
	return planning.ErrConflict
}

func validateRouteOrder(stored []dbgen.ListRouteStopsRow, stops []planning.RouteStop, order []string) error {
	if len(stored) == 0 || len(stored) != len(stops) || len(stored) != len(order) {
		return planning.ErrValidation
	}
	known := make(map[string]struct{}, len(stored))
	for _, stop := range stored {
		known[stop.RsID] = struct{}{}
	}
	calculated := make(map[string]struct{}, len(stops))
	for _, stop := range stops {
		if _, ok := known[stop.ID]; !ok || stop.LegDistanceMeters < 0 || stop.LegDuration < 0 {
			return planning.ErrValidation
		}
		if _, duplicate := calculated[stop.ID]; duplicate {
			return planning.ErrValidation
		}
		calculated[stop.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(order))
	for _, id := range order {
		if _, ok := known[id]; !ok {
			return planning.ErrValidation
		}
		if _, duplicate := seen[id]; duplicate {
			return planning.ErrValidation
		}
		seen[id] = struct{}{}
	}
	return nil
}

func getRoute(ctx context.Context, q *dbgen.Queries, id pgtype.UUID) (planning.RouteDraft, error) {
	row, err := q.GetRouteDraft(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return planning.RouteDraft{}, planning.ErrNotFound
	}
	if err != nil {
		return planning.RouteDraft{}, err
	}
	stopRows, err := q.ListRouteStops(ctx, id)
	if err != nil {
		return planning.RouteDraft{}, err
	}
	startLatitude, startLongitude, err := coordinates(row.RdStartLatitude, row.RdStartLongitude)
	if err != nil {
		return planning.RouteDraft{}, err
	}
	endLatitude, endLongitude, err := coordinates(row.RdEndLatitude, row.RdEndLongitude)
	if err != nil {
		return planning.RouteDraft{}, err
	}
	geometry, err := decodeRouteGeometry(row.RouteGeometry)
	if err != nil {
		return planning.RouteDraft{}, err
	}
	route := planning.RouteDraft{
		ID: row.RdID, DriverID: row.RdDriverID, ChipperResourceID: row.RdChipperResourceID,
		TransportResourceID: row.TransportResourceID,
		DriverName:          row.DriverName, ChipperName: row.ChipperName, TransportName: row.TransportName,
		Status: planning.RouteStatus(row.Status), StartLabel: row.StartLabel, EndLabel: row.EndLabel,
		Version: row.Version, Departure: row.DepartureAt.Time.UTC(),
		Start: planning.Point{Latitude: startLatitude, Longitude: startLongitude},
		End:   planning.Point{Latitude: endLatitude, Longitude: endLongitude},
		Directions: planning.RouteDirections{
			Geometry: geometry, DistanceMeters: int(row.DistanceMeters),
			Duration: time.Duration(row.DurationSeconds) * time.Second, Source: row.RoutingSource,
			Estimated: row.RoutingSource != "osrm", FreshAt: row.UpdatedAt.Time.UTC(),
		},
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
	route.Stops = make([]planning.RouteStop, 0, len(stopRows))
	for _, stopRow := range stopRows {
		latitude, longitude, parseErr := coordinates(stopRow.Latitude, stopRow.Longitude)
		if parseErr != nil {
			return planning.RouteDraft{}, parseErr
		}
		stop := planning.RouteStop{
			ID: stopRow.RsID, JobID: stopRow.RsJobID, AppointmentID: stopRow.AppointmentID,
			JobNumber: stopRow.JobNumber, CustomerName: stopRow.CustomerName,
			Region: stopRow.Region, Locality: stopRow.Locality, VolumeM3: stopRow.JVolumeM3,
			JobType: stopRow.JobType, TransportMode: stopRow.TransportMode,
			ExternalTransportConfirmed: stopRow.ExternalTransportConfirmed,
			Position:                   int(stopRow.Position), Location: planning.Point{Latitude: latitude, Longitude: longitude},
			WorkDuration:      stopRow.PlannedEndsAt.Time.Sub(stopRow.PlannedStartsAt.Time),
			LegDuration:       time.Duration(stopRow.TravelDurationSeconds) * time.Second,
			LegDistanceMeters: int(stopRow.TravelDistanceMeters),
			StartsAt:          stopRow.PlannedStartsAt.Time.UTC(), EndsAt: stopRow.PlannedEndsAt.Time.UTC(),
			JobVersion: stopRow.JobVersion, WaitlistVersion: stopRow.WaitlistVersion,
		}
		route.Stops = append(route.Stops, stop)
		route.Directions.Legs = append(route.Directions.Legs, planning.RouteLeg{
			DistanceMeters: stop.LegDistanceMeters, Duration: stop.LegDuration,
		})
	}
	returnDistance, returnDuration := int(row.DistanceMeters), time.Duration(row.DurationSeconds)*time.Second
	cursor := route.Departure
	for index := range route.Stops {
		stop := &route.Stops[index]
		returnDistance -= stop.LegDistanceMeters
		returnDuration -= stop.LegDuration
		cursor = cursor.Add(stop.LegDuration)
		stop.EstimatedArrivalAt = cursor
		cursor = cursor.Add(stop.WorkDuration)
	}
	if returnDistance < 0 || returnDuration < 0 {
		return planning.RouteDraft{}, planning.ErrValidation
	}
	if len(route.Stops) > 0 {
		route.Directions.Legs = append(route.Directions.Legs, planning.RouteLeg{
			DistanceMeters: returnDistance, Duration: returnDuration,
		})
	}
	route.EstimatedEndAt = cursor.Add(returnDuration)
	return route, nil
}

type geoJSONLineString struct {
	Type        string      `json:"type"`
	Coordinates [][]float64 `json:"coordinates"`
}

func encodeRouteGeometry(points []planning.Point) ([]byte, error) {
	if len(points) < 2 {
		return nil, planning.ErrValidation
	}
	coordinates := make([][]float64, 0, len(points))
	for _, point := range points {
		if !point.Valid() {
			return nil, planning.ErrValidation
		}
		coordinates = append(coordinates, []float64{point.Longitude, point.Latitude})
	}
	value, err := json.Marshal(geoJSONLineString{Type: "LineString", Coordinates: coordinates})
	if err != nil {
		return nil, err
	}
	return value, nil
}

func decodeRouteGeometry(value []byte) ([]planning.Point, error) {
	var line geoJSONLineString
	if json.Unmarshal(value, &line) != nil || line.Type != "LineString" || len(line.Coordinates) < 2 {
		return nil, planning.ErrValidation
	}
	result := make([]planning.Point, 0, len(line.Coordinates))
	for _, coordinate := range line.Coordinates {
		if len(coordinate) != 2 {
			return nil, planning.ErrValidation
		}
		point := planning.Point{Latitude: coordinate[1], Longitude: coordinate[0]}
		if !point.Valid() {
			return nil, planning.ErrValidation
		}
		result = append(result, point)
	}
	return result, nil
}

func coordinateNumeric(value float64) (pgtype.Numeric, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return pgtype.Numeric{}, planning.ErrValidation
	}
	var result pgtype.Numeric
	if err := result.Scan(strconv.FormatFloat(value, 'f', 6, 64)); err != nil {
		return pgtype.Numeric{}, planning.ErrValidation
	}
	return result, nil
}

func routeMetrics(value planning.RouteDirections) (int32, int32, error) {
	distance, err := nonnegativeInt32(value.DistanceMeters)
	if err != nil {
		return 0, 0, err
	}
	var duration int64
	for _, leg := range value.Legs {
		seconds, secondsErr := durationSeconds(leg.Duration)
		if secondsErr != nil {
			return 0, 0, secondsErr
		}
		duration += int64(seconds)
		if duration > math.MaxInt32 {
			return 0, 0, planning.ErrValidation
		}
	}
	return distance, int32(duration), nil
}

func nonnegativeInt32(value int) (int32, error) {
	if value < 0 || value > math.MaxInt32 {
		return 0, planning.ErrValidation
	}
	return int32(value), nil
}

func durationSeconds(value time.Duration) (int32, error) {
	if value < 0 {
		return 0, planning.ErrValidation
	}
	seconds := math.Ceil(value.Seconds())
	if seconds > math.MaxInt32 {
		return 0, planning.ErrValidation
	}
	return int32(seconds), nil
}

func mapRouteError(err error) error {
	if errors.Is(err, planning.ErrConflict) || errors.Is(err, planning.ErrNotFound) ||
		errors.Is(err, planning.ErrValidation) || errors.Is(err, auth.ErrForbidden) {
		return err
	}
	return mapPlanningError(err)
}
