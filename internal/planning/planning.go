// Package planning produces explainable, non-binding appointment suggestions.
package planning

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
)

var (
	ErrConfiguration = errors.New("planning: default start location is not configured")
	ErrConflict      = errors.New("planning: stale or conflicting suggestion")
	ErrNoCapacity    = errors.New("planning: no valid capacity")
	ErrNotFound      = errors.New("planning: not found")
	ErrValidation    = errors.New("planning: validation failed")
)

type Point struct{ Latitude, Longitude float64 }

func (p Point) Valid() bool {
	return p.Latitude >= -90 && p.Latitude <= 90 && p.Longitude >= -180 && p.Longitude <= 180 &&
		(p.Latitude != 0 || p.Longitude != 0)
}

type MatrixCell struct {
	DistanceMeters int
	Duration       time.Duration
}

type Matrix struct {
	Cells     [][]MatrixCell
	Source    string
	Estimated bool
	FreshAt   time.Time
}

type Router interface {
	Matrix(context.Context, []Point) (Matrix, error)
}

type Interval struct {
	StartsAt, EndsAt time.Time
	Status           string
}

type Driver struct {
	ID, Name     string
	Availability []Interval
}

type Resource struct {
	ID, Name, Type string
	Exclusive      bool
}

type Reservation struct {
	ID                     string
	StartsAt, EndsAt       time.Time
	DriverIDs, ResourceIDs []string
	Location               Point
}

type Job struct {
	ID, Number, Type, TransportMode, Urgency, Region string
	Version, WaitlistVersion, CustomerVersion        int32
	HackMinutes, TransportMinutes                    int
	ExternalTransportConfirmed                       bool
	ReceivedAt, EnteredAt                            time.Time
	PreferredStart, PreferredEnd                     string
	Location                                         Point
}

type Snapshot struct {
	Job          Job
	Drivers      []Driver
	Resources    []Resource
	Reservations []Reservation
}

type Weights struct {
	Preference, Travel, Driver, Resource, Utilization, Urgency, Region float64
}

type Config struct {
	Location                      *time.Location
	RouterName                    string
	BusinessOpen, BusinessClose   int
	SlotMinutes, HorizonDays      int
	BufferMinutes, CandidateLimit int
	SuggestionTTL                 time.Duration
	Weights                       Weights
}

func DefaultConfig(location *time.Location) Config {
	return Config{
		Location: location, RouterName: "haversine", BusinessOpen: 7 * 60, BusinessClose: 17 * 60,
		SlotMinutes: 15, HorizonDays: 56, BufferMinutes: 15, CandidateLimit: 2500,
		SuggestionTTL: 30 * time.Minute,
		Weights:       Weights{Preference: 25, Travel: 25, Driver: 15, Resource: 10, Utilization: 10, Urgency: 10, Region: 5},
	}
}

func (c Config) Validate() error {
	weight := c.Weights.Preference + c.Weights.Travel + c.Weights.Driver + c.Weights.Resource + c.Weights.Utilization + c.Weights.Urgency + c.Weights.Region
	if c.Location == nil || c.Location.String() != "Europe/Vienna" || c.BusinessOpen < 0 || c.BusinessClose > 24*60 || c.BusinessClose <= c.BusinessOpen ||
		c.SlotMinutes < 5 || c.SlotMinutes > 60 || 60%c.SlotMinutes != 0 || c.HorizonDays < 1 || c.HorizonDays > 90 ||
		c.BufferMinutes < 0 || c.BufferMinutes > 240 || c.CandidateLimit < 10 || c.CandidateLimit > 10000 ||
		c.SuggestionTTL < time.Minute || c.SuggestionTTL > 24*time.Hour || weight <= 0 {
		return ErrValidation
	}
	for _, value := range []float64{c.Weights.Preference, c.Weights.Travel, c.Weights.Driver, c.Weights.Resource, c.Weights.Utilization, c.Weights.Urgency, c.Weights.Region} {
		if value < 0 || value > 100 {
			return ErrValidation
		}
	}
	return nil
}

type Component struct {
	Preference, Travel, Driver, Resource, Utilization, Urgency, Region float64
}

type Suggestion struct {
	ID, RunID, DriverID, DriverName, RoutingSource string
	Rank, DistanceMeters, DurationSeconds          int
	StartsAt, EndsAt, CreatedAt, ExpiresAt         time.Time
	ResourceIDs, ResourcePurposes                  []string
	Score                                          float64
	Components                                     Component
	Reasons, Warnings, ResourceNames               []string
	Status                                         string
	JobID                                          string
	JobVersion, WaitlistVersion                    int32
}

type Run struct {
	ID, JobID            string
	Suggestions          []Suggestion
	CreatedAt, ExpiresAt time.Time
	Stale                bool
	Exclusions           []Exclusion
	HorizonDays          int
	CandidateLimit       int
}

type Exclusion struct {
	Kind, Name, Reason string
}

// RunSnapshot is the persisted, non-customer run explanation. Config remains
// available for reproducibility while exclusions explain why active internal
// capacity did not appear in the top-three result.
type RunSnapshot struct {
	Config     Config      `json:"config"`
	Exclusions []Exclusion `json:"exclusions,omitempty"`
}

type ClusterEntry struct {
	JobID, Region string
	Location      Point
}
type ClusterHint struct {
	JobIDs   []string
	Region   string
	Count    int
	RadiusKM float64
}

type Store interface {
	LoadSnapshot(context.Context, string, time.Time, time.Time) (Snapshot, error)
	SaveRun(context.Context, auth.Actor, Snapshot, time.Time, time.Time, []Suggestion, Config) (Run, error)
	ListRun(context.Context, string) (Run, error)
	Adopt(context.Context, auth.Actor, string, string) (string, error)
	ClusterEntries(context.Context) ([]ClusterEntry, error)
}

type Availability interface {
	Resolve(context.Context, auth.Actor, string, time.Time, time.Time) ([]Interval, error)
}

// DefaultStartProvider resolves the current operating start point without relying
// on process configuration. It is a narrow port so the setting can be persisted.
type DefaultStartProvider interface {
	DefaultStart(context.Context) (Point, error)
}

type Observer interface {
	ObservePlanning(time.Duration, int, bool)
}
type Option func(*Service)

func WithObserver(observer Observer) Option {
	return func(service *Service) { service.observer = observer }
}

// WithDefaultStartProvider supplies the runtime-managed start point for suggestions.
func WithDefaultStartProvider(provider DefaultStartProvider) Option {
	return func(service *Service) { service.defaultStart = provider }
}

type Service struct {
	store        Store
	availability Availability
	router       Router
	config       Config
	now          func() time.Time
	observer     Observer
	defaultStart DefaultStartProvider
}

func New(store Store, availability Availability, router Router, cfg Config, now func() time.Time, options ...Option) (*Service, error) {
	if store == nil || availability == nil || router == nil || cfg.Validate() != nil {
		return nil, ErrValidation
	}
	if now == nil {
		now = time.Now
	}
	service := &Service{store: store, availability: availability, router: router, config: cfg, now: now}
	for _, option := range options {
		option(service)
	}
	return service, nil
}

func (s *Service) Suggest(ctx context.Context, actor auth.Actor, jobID string) (Run, error) {
	startedAt := time.Now()
	candidateCount := 0
	fallback := false
	defer func() {
		if s.observer != nil {
			s.observer.ObservePlanning(time.Since(startedAt), candidateCount, fallback)
		}
	}()
	if err := actor.Require(auth.PermissionPlanningView); err != nil {
		return Run{}, err
	}
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return Run{}, ErrValidation
	}
	defaultStart, err := s.resolveDefaultStart(ctx)
	if err != nil {
		return Run{}, err
	}
	now := s.now().UTC()
	from := ceilTime(now, time.Duration(s.config.SlotMinutes)*time.Minute)
	to := from.AddDate(0, 0, s.config.HorizonDays)
	snapshot, err := s.store.LoadSnapshot(ctx, jobID, from, to)
	if err != nil {
		return Run{}, err
	}
	for i := range snapshot.Drivers {
		intervals, resolveErr := s.availability.Resolve(ctx, actor, snapshot.Drivers[i].ID, from, to)
		if resolveErr != nil {
			return Run{}, resolveErr
		}
		snapshot.Drivers[i].Availability = intervals
	}
	suggestions, err := GenerateWithDefaultStart(ctx, snapshot, s.router, s.config, defaultStart, from)
	if err != nil {
		return Run{}, err
	}
	candidateCount = len(suggestions)
	if s.config.RouterName == "osrm" {
		for _, suggestion := range suggestions {
			if suggestion.RoutingSource == "haversine" {
				fallback = true
				break
			}
		}
	}
	return s.store.SaveRun(ctx, actor, snapshot, from, to, suggestions, s.config)
}

func (s *Service) resolveDefaultStart(ctx context.Context) (Point, error) {
	if s.defaultStart == nil {
		return Point{}, ErrConfiguration
	}
	start, err := s.defaultStart.DefaultStart(ctx)
	if err != nil {
		return Point{}, fmt.Errorf("%w: resolving default start: %w", ErrConfiguration, err)
	}
	if !start.Valid() {
		return Point{}, ErrConfiguration
	}
	return start, nil
}

func (s *Service) ListRun(ctx context.Context, actor auth.Actor, runID string) (Run, error) {
	if err := actor.Require(auth.PermissionPlanningView); err != nil {
		return Run{}, err
	}
	run, err := s.store.ListRun(ctx, strings.TrimSpace(runID))
	if err == nil {
		run.Stale = !run.ExpiresAt.IsZero() && !run.ExpiresAt.After(s.now().UTC())
	}
	return run, err
}

func (s *Service) Adopt(ctx context.Context, actor auth.Actor, suggestionID, requestID string) (string, error) {
	if err := actor.Require(auth.PermissionPlanningAdopt); err != nil {
		return "", err
	}
	if strings.TrimSpace(suggestionID) == "" {
		return "", ErrValidation
	}
	return s.store.Adopt(ctx, actor, suggestionID, requestID)
}

func (s *Service) ClusterHints(ctx context.Context, actor auth.Actor) ([]ClusterHint, error) {
	if err := actor.Require(auth.PermissionPlanningView); err != nil {
		return nil, err
	}
	values, err := s.store.ClusterEntries(ctx)
	if err != nil {
		return nil, err
	}
	return Clusters(values, 15, 3), nil
}

func Clusters(values []ClusterEntry, radiusKM float64, minimum int) []ClusterHint {
	if radiusKM <= 0 || minimum < 2 {
		return nil
	}
	slices.SortFunc(values, func(a, b ClusterEntry) int { return strings.Compare(a.JobID, b.JobID) })
	used := make(map[string]bool)
	result := make([]ClusterHint, 0)
	for _, seed := range values {
		if used[seed.JobID] || !seed.Location.Valid() {
			continue
		}
		members := []ClusterEntry{seed}
		for _, candidate := range values {
			if candidate.JobID == seed.JobID || used[candidate.JobID] || !candidate.Location.Valid() {
				continue
			}
			if haversine(seed.Location, candidate.Location) <= radiusKM*1000 {
				members = append(members, candidate)
			}
		}
		if len(members) < minimum {
			continue
		}
		ids := make([]string, 0, len(members))
		region := seed.Region
		for _, member := range members {
			used[member.JobID] = true
			ids = append(ids, member.JobID)
			if region == "" {
				region = member.Region
			}
		}
		result = append(result, ClusterHint{JobIDs: ids, Region: region, Count: len(ids), RadiusKM: radiusKM})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return strings.Join(result[i].JobIDs, ",") < strings.Join(result[j].JobIDs, ",")
	})
	return result
}

func Generate(ctx context.Context, snapshot Snapshot, router Router, cfg Config, now time.Time) ([]Suggestion, error) {
	return generate(ctx, snapshot, router, cfg, Point{}, now)
}

// GenerateWithDefaultStart evaluates suggestions using a verified, runtime-supplied start point.
func GenerateWithDefaultStart(ctx context.Context, snapshot Snapshot, router Router, cfg Config, defaultStart Point, now time.Time) ([]Suggestion, error) {
	if !defaultStart.Valid() {
		return nil, ErrConfiguration
	}
	return generate(ctx, snapshot, router, cfg, defaultStart, now)
}

func ExplainExclusions(snapshot Snapshot, suggestions []Suggestion, from, to time.Time) []Exclusion {
	usedDrivers, usedResources := make(map[string]bool), make(map[string]bool)
	for _, suggestion := range suggestions {
		usedDrivers[suggestion.DriverID] = true
		for _, id := range suggestion.ResourceIDs {
			usedResources[id] = true
		}
	}
	result := make([]Exclusion, 0)
	for _, driver := range snapshot.Drivers {
		if usedDrivers[driver.ID] {
			continue
		}
		reason := "nicht in den drei bestbewerteten Vorschlägen enthalten"
		if !available(driver.Availability, from, to) {
			reason = "im Planungszeitraum nicht durchgehend verfügbar"
		}
		result = append(result, Exclusion{Kind: "Fahrer", Name: driver.Name, Reason: reason})
	}
	for _, resource := range snapshot.Resources {
		relevant := resource.Type == "chipper" || (snapshot.Job.Type == "chipping_with_transport" && snapshot.Job.TransportMode == "internal" && resource.Type == "transport_vehicle")
		if relevant && !usedResources[resource.ID] {
			reason := "nicht in den drei bestbewerteten Vorschlägen enthalten"
			if conflicts(snapshot.Reservations, from, to, "", []string{resource.ID}) {
				reason = "im Planungszeitraum nicht durchgehend verfügbar"
			}
			result = append(result, Exclusion{Kind: "Ressource", Name: resource.Name, Reason: reason})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func generate(ctx context.Context, snapshot Snapshot, router Router, cfg Config, defaultStart Point, now time.Time) ([]Suggestion, error) {
	if cfg.Validate() != nil || snapshot.Job.HackMinutes <= 0 || len(snapshot.Drivers) == 0 {
		return nil, ErrValidation
	}
	resourceChoices := selectResourceChoices(snapshot.Job, snapshot.Resources)
	if len(resourceChoices) == 0 {
		return nil, ErrNoCapacity
	}
	duration := time.Duration(snapshot.Job.HackMinutes+snapshot.Job.TransportMinutes+cfg.BufferMinutes) * time.Minute
	points := make([]Point, 0, 25)
	if snapshot.Job.Location.Valid() {
		points = append(points, snapshot.Job.Location)
	}
	if defaultStart.Valid() {
		points = append(points, defaultStart)
	}
	reservationPoint := make(map[string]int)
	if snapshot.Job.Location.Valid() {
		for _, reservation := range snapshot.Reservations {
			if len(points) >= 25 {
				break
			}
			if reservation.Location.Valid() {
				reservationPoint[reservation.ID] = len(points)
				points = append(points, reservation.Location)
			}
		}
	}
	matrix := Matrix{Source: "unavailable"}
	if len(points) >= 2 && snapshot.Job.Location.Valid() {
		if routed, err := router.Matrix(ctx, points); err == nil {
			matrix = routed
		}
	}
	result := make([]Suggestion, 0, 64)
	checked := 0
	startLocal := now.In(cfg.Location)
	for day := 0; day <= cfg.HorizonDays && checked < cfg.CandidateLimit; day++ {
		date := startLocal.AddDate(0, 0, day)
		if date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			continue
		}
		for minute := cfg.BusinessOpen; minute+int(duration/time.Minute) <= cfg.BusinessClose && checked < cfg.CandidateLimit; minute += cfg.SlotMinutes {
			startsLocal := time.Date(date.Year(), date.Month(), date.Day(), minute/60, minute%60, 0, 0, cfg.Location)
			starts := startsLocal.UTC()
			ends := starts.Add(duration)
			if starts.Before(now) {
				continue
			}
			for _, driver := range snapshot.Drivers {
				for _, choice := range resourceChoices {
					checked++
					if !available(driver.Availability, starts, ends) || conflicts(snapshot.Reservations, starts, ends, driver.ID, choice.ids) || !travelFeasible(snapshot.Reservations, starts, ends, matrix, reservationPoint, cfg.Location) {
						continue
					}
					result = append(result, score(snapshot, driver, choice.ids, choice.purposes, choice.names, starts, ends, matrix, reservationPoint, cfg, now))
					if checked >= cfg.CandidateLimit {
						break
					}
				}
				if checked >= cfg.CandidateLimit {
					break
				}
			}
		}
	}
	if len(result) == 0 {
		return nil, ErrNoCapacity
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		if result[i].Components.Preference != result[j].Components.Preference {
			return result[i].Components.Preference > result[j].Components.Preference
		}
		if result[i].DurationSeconds != result[j].DurationSeconds {
			return result[i].DurationSeconds < result[j].DurationSeconds
		}
		if !result[i].StartsAt.Equal(result[j].StartsAt) {
			return result[i].StartsAt.Before(result[j].StartsAt)
		}
		if result[i].DriverID != result[j].DriverID {
			return result[i].DriverID < result[j].DriverID
		}
		return strings.Join(result[i].ResourceIDs, ",") < strings.Join(result[j].ResourceIDs, ",")
	})
	result = deduplicate(result, 3)
	for index := range result {
		result[index].Rank = index + 1
	}
	return result, nil
}

type resourceChoice struct{ ids, purposes, names []string }

func selectResourceChoices(job Job, values []Resource) []resourceChoice {
	active := append([]Resource(nil), values...)
	sort.Slice(active, func(i, j int) bool {
		if active[i].Type != active[j].Type {
			return active[i].Type < active[j].Type
		}
		return active[i].ID < active[j].ID
	})
	chippers, vehicles := make([]Resource, 0), make([]Resource, 0)
	for _, value := range active {
		if value.Type == "chipper" {
			chippers = append(chippers, value)
		}
		if value.Type == "transport_vehicle" {
			vehicles = append(vehicles, value)
		}
	}
	if len(chippers) == 0 {
		return nil
	}
	result := make([]resourceChoice, 0)
	if job.Type == "chipping_with_transport" {
		switch job.TransportMode {
		case "internal":
			for _, chipper := range chippers {
				for _, vehicle := range vehicles {
					result = append(result, resourceChoice{ids: []string{chipper.ID, vehicle.ID}, purposes: []string{"chipping", "transport"}, names: []string{chipper.Name, vehicle.Name}})
				}
			}
		case "external":
			if !job.ExternalTransportConfirmed {
				return nil
			}
			for _, chipper := range chippers {
				result = append(result, resourceChoice{ids: []string{chipper.ID}, purposes: []string{"chipping"}, names: []string{chipper.Name}})
			}
		default:
			return nil
		}
	} else {
		for _, chipper := range chippers {
			result = append(result, resourceChoice{ids: []string{chipper.ID}, purposes: []string{"chipping"}, names: []string{chipper.Name}})
		}
	}
	return result
}

func available(intervals []Interval, from, to time.Time) bool {
	return slices.ContainsFunc(intervals, func(value Interval) bool {
		return value.Status == "available" && !from.Before(value.StartsAt) && !to.After(value.EndsAt)
	})
}

func conflicts(values []Reservation, from, to time.Time, driverID string, resources []string) bool {
	for _, value := range values {
		if from.Before(value.EndsAt) && to.After(value.StartsAt) && (slices.Contains(value.DriverIDs, driverID) || overlap(value.ResourceIDs, resources)) {
			return true
		}
	}
	return false
}
func overlap(a, b []string) bool {
	return slices.ContainsFunc(a, func(v string) bool { return slices.Contains(b, v) })
}

func travelFeasible(values []Reservation, starts, ends time.Time, matrix Matrix, pointIndex map[string]int, location *time.Location) bool {
	if matrix.Source == "" || matrix.Source == "unavailable" || len(matrix.Cells) == 0 {
		return true
	}
	var previous, next *Reservation
	day := starts.In(location).Format(time.DateOnly)
	for index := range values {
		value := &values[index]
		if value.StartsAt.In(location).Format(time.DateOnly) != day {
			continue
		}
		if !value.EndsAt.After(starts) && (previous == nil || value.EndsAt.After(previous.EndsAt)) {
			previous = value
		}
		if !value.StartsAt.Before(ends) && (next == nil || value.StartsAt.Before(next.StartsAt)) {
			next = value
		}
	}
	for _, adjacent := range []*Reservation{previous, next} {
		if adjacent == nil || !adjacent.Location.Valid() {
			continue
		}
		index, ok := pointIndex[adjacent.ID]
		if !ok || index >= len(matrix.Cells[0]) {
			continue
		}
		gap := starts.Sub(adjacent.EndsAt)
		if adjacent == next {
			gap = adjacent.StartsAt.Sub(ends)
		}
		if matrix.Cells[0][index].Duration > gap {
			return false
		}
	}
	return true
}

func score(snapshot Snapshot, driver Driver, resourceIDs, purposes, names []string, starts, ends time.Time, matrix Matrix, pointIndex map[string]int, cfg Config, now time.Time) Suggestion {
	pref := preference(snapshot.Job, starts, cfg.Location)
	urgency := urgencyScore(snapshot.Job, now)
	travel := 0.35
	source := "unavailable"
	warnings := []string{"Keine belastbaren Koordinaten; Routenanteil neutral reduziert."}
	distance, seconds := 0, 0
	if matrix.Source != "" && matrix.Source != "unavailable" && len(matrix.Cells) > 0 {
		source = matrix.Source
		warnings = nil
		if matrix.Estimated {
			warnings = []string{"Fahrzeit basiert auf einer Luftlinien-Schätzung."}
		}
		best := math.MaxInt
		for _, reservation := range snapshot.Reservations {
			if reservation.StartsAt.In(cfg.Location).Format(time.DateOnly) != starts.In(cfg.Location).Format(time.DateOnly) {
				continue
			}
			idx, ok := pointIndex[reservation.ID]
			if ok && idx < len(matrix.Cells[0]) && matrix.Cells[0][idx].DistanceMeters < best {
				best = matrix.Cells[0][idx].DistanceMeters
				seconds = int(matrix.Cells[0][idx].Duration.Seconds())
			}
		}
		if best == math.MaxInt && len(matrix.Cells[0]) > 1 {
			best = matrix.Cells[0][1].DistanceMeters
			seconds = int(matrix.Cells[0][1].Duration.Seconds())
		}
		if best != math.MaxInt {
			distance = best
			travel = clamp(1 - float64(best)/80000)
		}
	}
	util := utilization(snapshot.Reservations, starts, ends, cfg.Location)
	region := 0.5
	if travel > .7 {
		region = .9
	}
	c := Component{Preference: pref, Travel: travel, Driver: 1, Resource: 1, Utilization: util, Urgency: urgency, Region: region}
	totalWeight := cfg.Weights.Preference + cfg.Weights.Travel + cfg.Weights.Driver + cfg.Weights.Resource + cfg.Weights.Utilization + cfg.Weights.Urgency + cfg.Weights.Region
	scoreValue := 100 * (pref*cfg.Weights.Preference + travel*cfg.Weights.Travel + cfg.Weights.Driver + cfg.Weights.Resource + util*cfg.Weights.Utilization + urgency*cfg.Weights.Urgency + region*cfg.Weights.Region) / totalWeight
	reasons := []string{"Fahrer " + driver.Name + " verfügbar", strings.Join(names, " und ") + " frei"}
	if pref == 1 {
		reasons = append(reasons, "vollständig im gewünschten Zeitraum")
	}
	if urgency >= .8 {
		reasons = append(reasons, "hohe Dringlichkeit berücksichtigt")
	}
	if distance > 0 {
		reasons = append(reasons, fmt.Sprintf("ca. %d km zur geografisch nächsten Tagesstation", int(math.Round(float64(distance)/1000))))
	}
	if util > .7 {
		reasons = append(reasons, "nutzt eine bestehende Tageslücke")
	}
	return Suggestion{DriverID: driver.ID, DriverName: driver.Name, StartsAt: starts, EndsAt: ends, ResourceIDs: append([]string(nil), resourceIDs...), ResourcePurposes: append([]string(nil), purposes...), ResourceNames: append([]string(nil), names...), Score: math.Round(clamp(scoreValue/100)*1000) / 10, Components: c, Reasons: reasons, Warnings: warnings, RoutingSource: source, DistanceMeters: distance, DurationSeconds: seconds, Status: "pending"}
}

func preference(job Job, starts time.Time, location *time.Location) float64 {
	date := starts.In(location).Format(time.DateOnly)
	if job.PreferredStart == "" && job.PreferredEnd == "" {
		return .7
	}
	if (job.PreferredStart == "" || date >= job.PreferredStart) && (job.PreferredEnd == "" || date <= job.PreferredEnd) {
		return 1
	}
	return .2
}
func urgencyScore(job Job, now time.Time) float64 {
	base := map[string]float64{"low": .2, "normal": .5, "high": .8, "urgent": 1}[job.Urgency]
	age := now.Sub(job.EnteredAt).Hours() / (24 * 60)
	return clamp(base + math.Min(.2, math.Max(0, age*.2)))
}
func utilization(values []Reservation, starts, ends time.Time, location *time.Location) float64 {
	result := .5
	for _, v := range values {
		if v.StartsAt.In(location).Format(time.DateOnly) != starts.In(location).Format(time.DateOnly) {
			continue
		}
		gap := v.StartsAt.Sub(ends)
		if gap >= 0 && gap <= time.Hour {
			result = .9
		}
		gap = starts.Sub(v.EndsAt)
		if gap >= 0 && gap <= time.Hour {
			result = .9
		}
	}
	return result
}
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
func ceilTime(value time.Time, step time.Duration) time.Time { return value.Truncate(step).Add(step) }
func deduplicate(values []Suggestion, limit int) []Suggestion {
	result := make([]Suggestion, 0, limit)
	for _, v := range values {
		duplicate := slices.ContainsFunc(result, func(x Suggestion) bool {
			return x.DriverID == v.DriverID && x.StartsAt.Sub(v.StartsAt).Abs() < 2*time.Hour
		})
		if !duplicate {
			result = append(result, v)
			if len(result) == limit {
				break
			}
		}
	}
	return result
}

func Fingerprint(snapshot Snapshot, cfg Config) ([]byte, []byte, error) {
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return nil, nil, err
	}
	input, err := json.Marshal(snapshot)
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256(append(input, configJSON...))
	return sum[:], configJSON, nil
}
