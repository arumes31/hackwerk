package planning

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
)

const DefaultMaxRouteStops = 23

type RouteStatus string

const (
	RouteStatusDraft    RouteStatus = "draft"
	RouteStatusAssigned RouteStatus = "assigned"
)

type RouteCandidate struct {
	JobID, JobNumber            string
	CustomerName, Region        string
	Locality, VolumeM3          string
	JobType, TransportMode      string
	UnavailableReason           string
	ExternalTransportConfirmed  bool
	Location                    Point
	WorkDuration                time.Duration
	JobVersion, WaitlistVersion int32
}

type RouteMissingLocation struct {
	JobID, JobNumber     string
	CustomerName, Region string
}

type RouteDriverOption struct {
	ID, Name string
}

type RouteResourceOption struct {
	ID, Name, Type string
	Exclusive      bool
}

type RouteOptions struct {
	Drivers   []RouteDriverOption
	Resources []RouteResourceOption
}

type RouteStop struct {
	ID, JobID, AppointmentID    string
	JobNumber, CustomerName     string
	CustomerPhone               string
	Region, Locality, VolumeM3  string
	JobType, TransportMode      string
	ExternalTransportConfirmed  bool
	Position                    int
	Location                    Point
	WorkDuration, LegDuration   time.Duration
	LegDistanceMeters           int
	EstimatedArrivalAt          time.Time
	StartsAt, EndsAt            time.Time
	JobVersion, WaitlistVersion int32
}

type RouteComparison struct {
	ManualDistanceMeters, OptimizedDistanceMeters int
	ManualDuration, OptimizedDuration             time.Duration
}

type RouteDraft struct {
	ID, DriverID, ChipperResourceID, TransportResourceID string
	DriverName, ChipperName, TransportName               string
	StartLabel, EndLabel                                 string
	Status                                               RouteStatus
	Version                                              int32
	Departure                                            time.Time
	Start, End                                           Point
	Stops                                                []RouteStop
	Directions                                           RouteDirections
	Comparison                                           RouteComparison
	EstimatedEndAt                                       time.Time
	CreatedAt, UpdatedAt                                 time.Time
}

type PlanRouteInput struct {
	ID                          string
	ExpectedVersion             int32
	Departure                   time.Time
	DriverID, ChipperResourceID string
	TransportResourceID         string
	StartLabel, EndLabel        string
	Start, End                  Point
	JobIDs                      []string
	FixedJobIDs                 []string
	Optimize                    bool
	EndAtLastStop               bool
	RequestID                   string
}

type AssignRouteInput struct {
	ID              string
	ExpectedVersion int32
	RequestID       string
}

type ReorderOwnRouteInput struct {
	ID              string
	ExpectedVersion int32
	StopIDs         []string
	RequestID       string
}

type SaveRouteDraftInput struct {
	Route           RouteDraft
	ExpectedVersion int32
	RequestID       string
}

type SaveRouteOrderInput struct {
	Route           RouteDraft
	ExpectedVersion int32
	StopIDs         []string
	RequestID       string
}

type MoveDraftStopInput struct {
	SourceRouteID, TargetRouteID string
	StopID                       string
	SourceVersion, TargetVersion int32
	RequestID                    string
}

type SaveMovedDraftStopInput struct {
	Source, Target               RouteDraft
	SourceVersion, TargetVersion int32
	StopID, RequestID            string
}

type RouteStore interface {
	LoadRouteCandidates(context.Context, []string) ([]RouteCandidate, error)
	LoadRouteMissingLocations(context.Context) ([]RouteMissingLocation, error)
	LoadRouteOptions(context.Context) (RouteOptions, error)
	SaveRouteDraft(context.Context, auth.Actor, SaveRouteDraftInput) (RouteDraft, error)
	GetRoute(context.Context, string) (RouteDraft, error)
	LatestAssignedRouteForDriver(context.Context, string, string) (RouteDraft, error)
	AssignRoute(context.Context, auth.Actor, AssignRouteInput) (RouteDraft, error)
	SaveRouteOrder(context.Context, auth.Actor, SaveRouteOrderInput) (RouteDraft, error)
	ListDraftRouteIDsForDate(context.Context, string) ([]string, error)
	SaveMovedDraftStop(context.Context, auth.Actor, SaveMovedDraftStopInput) error
}

func (s *RouteService) DraftsForDate(ctx context.Context, actor auth.Actor, localDate string) ([]RouteDraft, error) {
	if err := actor.Require(auth.PermissionRoutePlan); err != nil {
		return nil, err
	}
	if _, err := time.Parse(time.DateOnly, strings.TrimSpace(localDate)); err != nil {
		return nil, ErrValidation
	}
	ids, err := s.store.ListDraftRouteIDsForDate(ctx, localDate)
	if err != nil {
		return nil, err
	}
	result := make([]RouteDraft, 0, len(ids))
	for _, id := range ids {
		route, loadErr := s.store.GetRoute(ctx, id)
		if loadErr != nil {
			return nil, loadErr
		}
		result = append(result, route)
	}
	return result, nil
}

func (s *RouteService) MoveDraftStop(ctx context.Context, actor auth.Actor, input MoveDraftStopInput) ([]RouteDraft, error) {
	if err := actor.Require(auth.PermissionRoutePlan); err != nil {
		return nil, err
	}
	input.SourceRouteID = strings.TrimSpace(input.SourceRouteID)
	input.TargetRouteID = strings.TrimSpace(input.TargetRouteID)
	input.StopID = strings.TrimSpace(input.StopID)
	if input.SourceRouteID == "" || input.TargetRouteID == "" || input.SourceRouteID == input.TargetRouteID || input.StopID == "" || input.SourceVersion < 1 || input.TargetVersion < 1 {
		return nil, ErrValidation
	}
	source, err := s.store.GetRoute(ctx, input.SourceRouteID)
	if err != nil {
		return nil, err
	}
	target, err := s.store.GetRoute(ctx, input.TargetRouteID)
	if err != nil {
		return nil, err
	}
	if source.Status != RouteStatusDraft || target.Status != RouteStatusDraft || source.Version != input.SourceVersion || target.Version != input.TargetVersion || len(source.Stops) < 2 || len(target.Stops) >= s.config.MaxStops {
		return nil, ErrConflict
	}
	index := slices.IndexFunc(source.Stops, func(stop RouteStop) bool { return stop.ID == input.StopID })
	if index < 0 {
		return nil, ErrConflict
	}
	moved := source.Stops[index]
	if slices.ContainsFunc(target.Stops, func(stop RouteStop) bool { return stop.JobID == moved.JobID }) {
		return nil, ErrConflict
	}
	source.Stops = append(source.Stops[:index], source.Stops[index+1:]...)
	target.Stops = append(target.Stops, moved)
	for index := range source.Stops {
		source.Stops[index].Position = index + 1
	}
	for index := range target.Stops {
		target.Stops[index].Position = index + 1
	}
	if err := s.calculateDirections(ctx, &source, false); err != nil {
		return nil, err
	}
	if err := s.calculateDirections(ctx, &target, false); err != nil {
		return nil, err
	}
	if err := s.store.SaveMovedDraftStop(ctx, actor, SaveMovedDraftStopInput{Source: source, Target: target, SourceVersion: input.SourceVersion, TargetVersion: input.TargetVersion, StopID: input.StopID, RequestID: input.RequestID}); err != nil {
		return nil, err
	}
	updatedSource, err := s.store.GetRoute(ctx, source.ID)
	if err != nil {
		return nil, err
	}
	updatedTarget, err := s.store.GetRoute(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	return []RouteDraft{updatedSource, updatedTarget}, nil
}

type RouteConfig struct {
	MaxStops int
}

func DefaultRouteConfig() RouteConfig {
	return RouteConfig{MaxStops: DefaultMaxRouteStops}
}

type RouteService struct {
	store      RouteStore
	matrix     Router
	directions DirectionsRouter
	config     RouteConfig
}

func NewRouteService(store RouteStore, matrix Router, directions DirectionsRouter, config RouteConfig) (*RouteService, error) {
	if store == nil || matrix == nil || directions == nil {
		return nil, ErrValidation
	}
	if config.MaxStops == 0 {
		config.MaxStops = DefaultMaxRouteStops
	}
	if config.MaxStops < 1 || config.MaxStops > 23 {
		return nil, ErrValidation
	}
	return &RouteService{store: store, matrix: matrix, directions: directions, config: config}, nil
}

func (s *RouteService) Options(ctx context.Context, actor auth.Actor) (RouteOptions, error) {
	if err := actor.Require(auth.PermissionRoutePlan); err != nil {
		return RouteOptions{}, err
	}
	return s.store.LoadRouteOptions(ctx)
}

func (s *RouteService) Candidates(ctx context.Context, actor auth.Actor) ([]RouteCandidate, error) {
	if err := actor.Require(auth.PermissionRoutePlan); err != nil {
		return nil, err
	}
	return s.store.LoadRouteCandidates(ctx, nil)
}

func (s *RouteService) MissingLocations(ctx context.Context, actor auth.Actor) ([]RouteMissingLocation, error) {
	if err := actor.Require(auth.PermissionRoutePlan); err != nil {
		return nil, err
	}
	return s.store.LoadRouteMissingLocations(ctx)
}

func (s *RouteService) Route(ctx context.Context, actor auth.Actor, routeID string) (RouteDraft, error) {
	if err := actor.Require(auth.PermissionRoutePlan); err != nil {
		return RouteDraft{}, err
	}
	routeID = strings.TrimSpace(routeID)
	if routeID == "" {
		return RouteDraft{}, ErrValidation
	}
	return s.store.GetRoute(ctx, routeID)
}

func (s *RouteService) Plan(ctx context.Context, actor auth.Actor, input PlanRouteInput) (RouteDraft, error) {
	if err := actor.Require(auth.PermissionRoutePlan); err != nil {
		return RouteDraft{}, err
	}
	normalizePlanRouteInput(&input)
	if err := s.validatePlanInput(input); err != nil {
		return RouteDraft{}, err
	}
	candidates, err := s.store.LoadRouteCandidates(ctx, input.JobIDs)
	if err != nil {
		return RouteDraft{}, err
	}
	ordered, err := orderRouteCandidates(input.JobIDs, candidates)
	if err != nil {
		return RouteDraft{}, err
	}
	for _, candidate := range ordered {
		switch {
		case candidate.UnavailableReason != "":
			return RouteDraft{}, ErrConflict
		case candidate.JobType == "chipping_only":
		case candidate.JobType == "chipping_with_transport" && candidate.TransportMode == "internal" && input.TransportResourceID != "":
		case candidate.JobType == "chipping_with_transport" && candidate.TransportMode == "external" && candidate.ExternalTransportConfirmed:
		default:
			return RouteDraft{}, ErrValidation
		}
	}
	manual := append([]RouteCandidate(nil), ordered...)
	if input.Optimize {
		ordered, err = s.optimize(ctx, input.Start, input.End, ordered, input.FixedJobIDs, input.EndAtLastStop)
		if err != nil {
			return RouteDraft{}, err
		}
	}
	if input.EndAtLastStop {
		input.End = ordered[len(ordered)-1].Location
		input.EndLabel = routeCandidateLabel(ordered[len(ordered)-1])
	}
	route := RouteDraft{
		ID: input.ID, DriverID: input.DriverID, ChipperResourceID: input.ChipperResourceID,
		TransportResourceID: input.TransportResourceID, Status: RouteStatusDraft,
		Version: input.ExpectedVersion, Departure: input.Departure.UTC(), StartLabel: strings.TrimSpace(input.StartLabel), EndLabel: strings.TrimSpace(input.EndLabel), Start: input.Start, End: input.End,
	}
	route.Stops = make([]RouteStop, 0, len(ordered))
	for index, candidate := range ordered {
		route.Stops = append(route.Stops, RouteStop{
			JobID: candidate.JobID, JobNumber: candidate.JobNumber, CustomerName: candidate.CustomerName,
			Region: candidate.Region, Locality: candidate.Locality, VolumeM3: candidate.VolumeM3,
			Position: index + 1, Location: candidate.Location, WorkDuration: candidate.WorkDuration,
			JobVersion: candidate.JobVersion, WaitlistVersion: candidate.WaitlistVersion,
			JobType: candidate.JobType, TransportMode: candidate.TransportMode,
			ExternalTransportConfirmed: candidate.ExternalTransportConfirmed,
		})
	}
	if err := s.calculateDirections(ctx, &route, false); err != nil {
		return RouteDraft{}, err
	}
	if input.Optimize {
		manualRoute := route
		manualRoute.Stops = routeStopsFromCandidates(manual)
		if input.EndAtLastStop {
			manualRoute.End = manual[len(manual)-1].Location
			manualRoute.EndLabel = routeCandidateLabel(manual[len(manual)-1])
		}
		if err := s.calculateDirections(ctx, &manualRoute, false); err == nil {
			route.Comparison = RouteComparison{
				ManualDistanceMeters: manualRoute.Directions.DistanceMeters, OptimizedDistanceMeters: route.Directions.DistanceMeters,
				ManualDuration: manualRoute.Directions.Duration, OptimizedDuration: route.Directions.Duration,
			}
		}
	}
	saved, err := s.store.SaveRouteDraft(ctx, actor, SaveRouteDraftInput{
		Route: route, ExpectedVersion: input.ExpectedVersion, RequestID: input.RequestID,
	})
	if err != nil {
		return RouteDraft{}, err
	}
	saved.Comparison = route.Comparison
	return saved, nil
}

func (s *RouteService) Assign(ctx context.Context, actor auth.Actor, input AssignRouteInput) (RouteDraft, error) {
	if err := actor.Require(auth.PermissionRouteAssign); err != nil {
		return RouteDraft{}, err
	}
	input.ID = strings.TrimSpace(input.ID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	if input.ID == "" || input.ExpectedVersion < 1 {
		return RouteDraft{}, ErrValidation
	}
	current, err := s.store.GetRoute(ctx, input.ID)
	if err != nil {
		return RouteDraft{}, err
	}
	if current.Status != RouteStatusDraft || current.Version != input.ExpectedVersion || len(current.Stops) == 0 {
		return RouteDraft{}, ErrConflict
	}
	return s.store.AssignRoute(ctx, actor, input)
}

func (s *RouteService) OwnRoute(ctx context.Context, actor auth.Actor, routeID string) (RouteDraft, error) {
	if err := requireOwnRouteActor(actor, auth.PermissionRouteViewOwn); err != nil {
		return RouteDraft{}, err
	}
	routeID = strings.TrimSpace(routeID)
	if routeID == "" {
		return RouteDraft{}, ErrValidation
	}
	route, err := s.store.GetRoute(ctx, routeID)
	if err != nil {
		return RouteDraft{}, err
	}
	if route.Status != RouteStatusAssigned || route.DriverID != actor.DriverID {
		return RouteDraft{}, auth.ErrForbidden
	}
	return route, nil
}

func (s *RouteService) OwnRouteForDate(ctx context.Context, actor auth.Actor, localDate string) (RouteDraft, error) {
	if err := requireOwnRouteActor(actor, auth.PermissionRouteViewOwn); err != nil {
		return RouteDraft{}, err
	}
	localDate = strings.TrimSpace(localDate)
	if _, err := time.Parse(time.DateOnly, localDate); err != nil {
		return RouteDraft{}, ErrValidation
	}
	route, err := s.store.LatestAssignedRouteForDriver(ctx, actor.DriverID, localDate)
	if err != nil {
		return RouteDraft{}, err
	}
	if route.Status != RouteStatusAssigned || route.DriverID != actor.DriverID {
		return RouteDraft{}, auth.ErrForbidden
	}
	return route, nil
}

func (s *RouteService) ReorderOwn(ctx context.Context, actor auth.Actor, input ReorderOwnRouteInput) (RouteDraft, error) {
	if err := requireOwnRouteActor(actor, auth.PermissionRouteReorderOwn); err != nil {
		return RouteDraft{}, err
	}
	input.ID = strings.TrimSpace(input.ID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	for index := range input.StopIDs {
		input.StopIDs[index] = strings.TrimSpace(input.StopIDs[index])
	}
	if input.ID == "" || input.ExpectedVersion < 1 || len(input.StopIDs) == 0 {
		return RouteDraft{}, ErrValidation
	}
	current, err := s.store.GetRoute(ctx, input.ID)
	if err != nil {
		return RouteDraft{}, err
	}
	if current.DriverID != actor.DriverID || current.Status != RouteStatusAssigned {
		return RouteDraft{}, auth.ErrForbidden
	}
	if current.Version != input.ExpectedVersion {
		return RouteDraft{}, ErrConflict
	}
	ordered, err := orderRouteStops(input.StopIDs, current.Stops)
	if err != nil {
		return RouteDraft{}, err
	}
	current.Stops = ordered
	if err := s.calculateDirections(ctx, &current, true); err != nil {
		return RouteDraft{}, err
	}
	return s.store.SaveRouteOrder(ctx, actor, SaveRouteOrderInput{
		Route: current, ExpectedVersion: input.ExpectedVersion,
		StopIDs: append([]string(nil), input.StopIDs...), RequestID: input.RequestID,
	})
}

func (s *RouteService) validatePlanInput(input PlanRouteInput) error {
	if input.ID != "" && input.ExpectedVersion < 1 {
		return ErrValidation
	}
	if input.ID == "" && input.ExpectedVersion != 0 {
		return ErrValidation
	}
	startLabel := strings.TrimSpace(input.StartLabel)
	endLabel := strings.TrimSpace(input.EndLabel)
	if input.Departure.IsZero() || input.DriverID == "" || !input.Start.Valid() ||
		startLabel == "" || (!input.EndAtLastStop && (!input.End.Valid() || endLabel == "")) ||
		len([]rune(startLabel)) > 200 || len([]rune(endLabel)) > 200 || len(input.JobIDs) < 1 || len(input.JobIDs) > s.config.MaxStops {
		return ErrValidation
	}
	seen := make(map[string]struct{}, len(input.JobIDs))
	for _, id := range input.JobIDs {
		if id == "" {
			return ErrValidation
		}
		if _, duplicate := seen[id]; duplicate {
			return ErrValidation
		}
		seen[id] = struct{}{}
	}
	return nil
}

func routeCandidateLabel(candidate RouteCandidate) string {
	parts := make([]string, 0, 2)
	if value := strings.TrimSpace(candidate.JobNumber); value != "" {
		parts = append(parts, value)
	}
	if value := strings.TrimSpace(candidate.Locality); value != "" {
		parts = append(parts, value)
	}
	if len(parts) == 0 {
		return "Letzter Stopp"
	}
	return strings.Join(parts, " · ")
}

func (s *RouteService) optimize(ctx context.Context, start, end Point, candidates []RouteCandidate, fixedJobIDs []string, endAtLastStop bool) ([]RouteCandidate, error) {
	fixed := make(map[string]struct{}, len(fixedJobIDs))
	for _, id := range fixedJobIDs {
		if id == "" {
			return nil, ErrValidation
		}
		if _, duplicate := fixed[id]; duplicate {
			return nil, ErrValidation
		}
		fixed[id] = struct{}{}
	}
	remaining := make([]int, 0, len(candidates))
	for index, candidate := range candidates {
		if _, ok := fixed[candidate.JobID]; !ok {
			remaining = append(remaining, index+1)
		}
		delete(fixed, candidate.JobID)
	}
	if len(fixed) != 0 {
		return nil, ErrValidation
	}
	points := make([]Point, 0, len(candidates)+2)
	points = append(points, start)
	for _, candidate := range candidates {
		points = append(points, candidate.Location)
	}
	if !endAtLastStop {
		points = append(points, end)
	}
	matrix, err := s.matrix.Matrix(ctx, points)
	if err != nil {
		return nil, fmt.Errorf("planning: calculating route matrix: %w", err)
	}
	if err := validateRouteMatrix(matrix, len(points)); err != nil {
		return nil, err
	}
	buildOrder := func(seed int) ([]RouteCandidate, []int) {
		available := append([]int(nil), remaining...)
		ordered := make([]RouteCandidate, 0, len(candidates))
		indices := make([]int, 0, len(candidates))
		current := 0
		seedPending := seed != 0
		for position, candidate := range candidates {
			if slices.Contains(fixedJobIDs, candidate.JobID) {
				ordered = append(ordered, candidate)
				indices = append(indices, position+1)
				current = position + 1
				continue
			}
			nextPosition := 0
			if seedPending {
				nextPosition = slices.Index(available, seed)
				seedPending = false
			} else {
				sort.SliceStable(available, func(left, right int) bool {
					a, b := matrix.Cells[current][available[left]], matrix.Cells[current][available[right]]
					if a.Duration != b.Duration {
						return a.Duration < b.Duration
					}
					if a.DistanceMeters != b.DistanceMeters {
						return a.DistanceMeters < b.DistanceMeters
					}
					return candidates[available[left]-1].JobID < candidates[available[right]-1].JobID
				})
			}
			next := available[nextPosition]
			available = append(available[:nextPosition], available[nextPosition+1:]...)
			ordered = append(ordered, candidates[next-1])
			indices = append(indices, next)
			current = next
		}
		return ordered, indices
	}

	seeds := remaining
	if len(seeds) == 0 {
		seeds = []int{0}
	}
	var best []RouteCandidate
	var bestDuration time.Duration
	var bestDistance int64
	var bestKey string
	for _, seed := range seeds {
		ordered, indices := buildOrder(seed)
		path := indices
		if !endAtLastStop {
			path = append(path, len(points)-1)
		}
		current := 0
		var duration time.Duration
		var distance int64
		valid := true
		for _, next := range path {
			cell := matrix.Cells[current][next]
			if cell.Duration > time.Duration(math.MaxInt64)-duration || int64(cell.DistanceMeters) > math.MaxInt64-distance {
				valid = false
				break
			}
			duration += cell.Duration
			distance += int64(cell.DistanceMeters)
			current = next
		}
		if !valid {
			return nil, ErrValidation
		}
		ids := make([]string, len(ordered))
		for index := range ordered {
			ids[index] = ordered[index].JobID
		}
		key := strings.Join(ids, "\x00")
		if best == nil || duration < bestDuration ||
			(duration == bestDuration && (distance < bestDistance || (distance == bestDistance && key < bestKey))) {
			best = ordered
			bestDuration = duration
			bestDistance = distance
			bestKey = key
		}
	}
	return best, nil
}

func routeStopsFromCandidates(candidates []RouteCandidate) []RouteStop {
	result := make([]RouteStop, 0, len(candidates))
	for index, candidate := range candidates {
		result = append(result, RouteStop{
			JobID: candidate.JobID, JobNumber: candidate.JobNumber, CustomerName: candidate.CustomerName,
			Region: candidate.Region, Locality: candidate.Locality, VolumeM3: candidate.VolumeM3,
			JobType: candidate.JobType, TransportMode: candidate.TransportMode,
			ExternalTransportConfirmed: candidate.ExternalTransportConfirmed,
			Position:                   index + 1, Location: candidate.Location, WorkDuration: candidate.WorkDuration,
			JobVersion: candidate.JobVersion, WaitlistVersion: candidate.WaitlistVersion,
		})
	}
	return result
}

func (r RouteDraft) NextStop(now time.Time) *RouteStop {
	for index := range r.Stops {
		if r.Stops[index].EndsAt.IsZero() || r.Stops[index].EndsAt.After(now) {
			return &r.Stops[index]
		}
	}
	return nil
}

func (s *RouteService) calculateDirections(ctx context.Context, route *RouteDraft, preserveAppointments bool) error {
	points := make([]Point, 0, len(route.Stops)+2)
	points = append(points, route.Start)
	for _, stop := range route.Stops {
		points = append(points, stop.Location)
	}
	returnsToEnd := route.End != route.Stops[len(route.Stops)-1].Location
	if returnsToEnd {
		points = append(points, route.End)
	}
	directions, err := s.directions.Directions(ctx, points)
	if err != nil {
		return fmt.Errorf("planning: calculating route directions: %w", err)
	}
	if err := validateRouteDirections(directions, len(points)); err != nil {
		return err
	}
	route.Directions = directions
	cursor := route.Departure.UTC()
	for index := range route.Stops {
		stop := &route.Stops[index]
		leg := directions.Legs[index]
		travelDuration, err := routeReservationDuration(leg.Duration)
		if err != nil {
			return err
		}
		cursor = cursor.Add(travelDuration)
		stop.Position = index + 1
		stop.LegDistanceMeters = leg.DistanceMeters
		stop.LegDuration = leg.Duration
		stop.EstimatedArrivalAt = cursor
		if !preserveAppointments {
			stop.StartsAt = cursor
			stop.EndsAt = cursor.Add(stop.WorkDuration)
		}
		cursor = cursor.Add(stop.WorkDuration)
	}
	route.EstimatedEndAt = cursor
	if returnsToEnd {
		travelDuration, err := routeReservationDuration(directions.Legs[len(directions.Legs)-1].Duration)
		if err != nil {
			return err
		}
		route.EstimatedEndAt = cursor.Add(travelDuration)
	}
	return nil
}

func routeReservationDuration(duration time.Duration) (time.Duration, error) {
	if duration < 0 {
		return 0, ErrValidation
	}
	minutes := duration / time.Minute
	if duration%time.Minute != 0 {
		minutes++
	}
	if minutes > time.Duration(math.MaxInt64)/time.Minute {
		return 0, ErrValidation
	}
	return minutes * time.Minute, nil
}

func orderRouteCandidates(ids []string, candidates []RouteCandidate) ([]RouteCandidate, error) {
	byID := make(map[string]RouteCandidate, len(candidates))
	for _, candidate := range candidates {
		candidate.JobID = strings.TrimSpace(candidate.JobID)
		if candidate.JobID == "" || !candidate.Location.Valid() || candidate.WorkDuration <= 0 || candidate.JobVersion < 1 || candidate.WaitlistVersion < 1 {
			return nil, ErrValidation
		}
		if _, duplicate := byID[candidate.JobID]; duplicate {
			return nil, ErrConflict
		}
		byID[candidate.JobID] = candidate
	}
	if len(byID) != len(ids) {
		return nil, ErrConflict
	}
	ordered := make([]RouteCandidate, 0, len(ids))
	for _, id := range ids {
		candidate, ok := byID[id]
		if !ok {
			return nil, ErrConflict
		}
		ordered = append(ordered, candidate)
	}
	return ordered, nil
}

func orderRouteStops(ids []string, stops []RouteStop) ([]RouteStop, error) {
	if len(ids) != len(stops) {
		return nil, ErrValidation
	}
	byID := make(map[string]RouteStop, len(stops))
	for _, stop := range stops {
		if stop.ID == "" || !stop.Location.Valid() || stop.WorkDuration <= 0 {
			return nil, ErrConflict
		}
		if _, duplicate := byID[stop.ID]; duplicate {
			return nil, ErrConflict
		}
		byID[stop.ID] = stop
	}
	ordered := make([]RouteStop, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		stop, ok := byID[id]
		if !ok {
			return nil, ErrValidation
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, ErrValidation
		}
		seen[id] = struct{}{}
		ordered = append(ordered, stop)
	}
	return ordered, nil
}

func validateRouteMatrix(matrix Matrix, size int) error {
	if matrix.Source == "" || len(matrix.Cells) != size {
		return ErrValidation
	}
	for _, row := range matrix.Cells {
		if len(row) != size || slices.ContainsFunc(row, func(cell MatrixCell) bool {
			return cell.DistanceMeters < 0 || cell.Duration < 0
		}) {
			return ErrValidation
		}
	}
	return nil
}

func validateRouteDirections(value RouteDirections, points int) error {
	if value.Source == "" || len(value.Geometry) < 2 || len(value.Legs) != points-1 || value.DistanceMeters < 0 || value.Duration < 0 {
		return ErrValidation
	}
	for _, point := range value.Geometry {
		if !point.Valid() {
			return ErrValidation
		}
	}
	for _, leg := range value.Legs {
		if leg.DistanceMeters < 0 || leg.Duration < 0 {
			return ErrValidation
		}
	}
	return nil
}

func normalizePlanRouteInput(input *PlanRouteInput) {
	input.ID = strings.TrimSpace(input.ID)
	input.DriverID = strings.TrimSpace(input.DriverID)
	input.ChipperResourceID = strings.TrimSpace(input.ChipperResourceID)
	input.TransportResourceID = strings.TrimSpace(input.TransportResourceID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	for index := range input.JobIDs {
		input.JobIDs[index] = strings.TrimSpace(input.JobIDs[index])
	}
	selected := make(map[string]struct{}, len(input.JobIDs))
	for _, id := range input.JobIDs {
		selected[id] = struct{}{}
	}
	fixed := input.FixedJobIDs[:0]
	for index := range input.FixedJobIDs {
		id := strings.TrimSpace(input.FixedJobIDs[index])
		if _, ok := selected[id]; ok {
			fixed = append(fixed, id)
		}
	}
	input.FixedJobIDs = fixed
}

func requireOwnRouteActor(actor auth.Actor, permission auth.Permission) error {
	if err := actor.Require(permission); err != nil {
		return err
	}
	if actor.Role != auth.RoleDriver || strings.TrimSpace(actor.DriverID) == "" {
		return auth.ErrForbidden
	}
	return nil
}
