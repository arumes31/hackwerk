// Package routelocation manages saved start and end locations for route drafts.
package routelocation

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"example.invalid/hackplan/internal/auth"
)

var (
	ErrConflict   = errors.New("route location: version conflict")
	ErrNotFound   = errors.New("route location: not found")
	ErrValidation = errors.New("route location: validation failed")
)

const (
	maxLabelRunes   = 120
	maxAddressRunes = 500
)

// Input is the editable, confirmed location data.
type Input struct {
	Label, Address           string
	Latitude, Longitude      float64
	DefaultStart, DefaultEnd bool
}

// Location is a saved route start or end point.
type Location struct {
	ID                       string
	Label, Address           string
	Latitude, Longitude      float64
	Active                   bool
	DefaultStart, DefaultEnd bool
	Version                  int32
	CreatedAt, UpdatedAt     time.Time
}

// Store persists route locations.
type Store interface {
	List(context.Context) ([]Location, error)
	ListActive(context.Context) ([]Location, error)
	DefaultStart(context.Context) (Location, error)
	Resolve(context.Context, string, int32) (Location, error)
	Create(context.Context, auth.Actor, Input, string) (Location, error)
	Update(context.Context, auth.Actor, string, int32, Input, string) (Location, error)
	Deactivate(context.Context, auth.Actor, string, int32, string) error
}

// Service applies authorization and validation before persistence.
type Service struct{ store Store }

func New(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("route location: store is required")
	}
	return &Service{store: store}, nil
}

// List returns active and inactive locations for settings administration.
func (s *Service) List(ctx context.Context, actor auth.Actor) ([]Location, error) {
	if err := actor.Require(auth.PermissionSettingsManage); err != nil {
		return nil, err
	}
	return s.store.List(ctx)
}

// ListActive returns selectable locations to route planners.
func (s *Service) ListActive(ctx context.Context, actor auth.Actor) ([]Location, error) {
	if err := actor.Require(auth.PermissionRoutePlan); err != nil {
		return nil, err
	}
	return s.store.ListActive(ctx)
}

// Resolve looks up a saved active location and verifies the version selected
// by the caller before its coordinates are used for planning.
func (s *Service) Resolve(ctx context.Context, actor auth.Actor, id string, version int32) (Location, error) {
	if err := actor.Require(auth.PermissionRoutePlan); err != nil {
		return Location{}, err
	}
	if strings.TrimSpace(id) == "" || version < 1 {
		return Location{}, ErrNotFound
	}
	return s.store.Resolve(ctx, id, version)
}

func (s *Service) Create(ctx context.Context, actor auth.Actor, input Input, requestID string) (Location, error) {
	if err := actor.Require(auth.PermissionSettingsManage); err != nil {
		return Location{}, err
	}
	normalize(&input)
	if err := input.Validate(); err != nil {
		return Location{}, err
	}
	return s.store.Create(ctx, actor, input, requestID)
}

func (s *Service) Update(ctx context.Context, actor auth.Actor, id string, version int32, input Input, requestID string) (Location, error) {
	if err := actor.Require(auth.PermissionSettingsManage); err != nil {
		return Location{}, err
	}
	normalize(&input)
	if strings.TrimSpace(id) == "" || version < 1 {
		return Location{}, ErrValidation
	}
	if err := input.Validate(); err != nil {
		return Location{}, err
	}
	return s.store.Update(ctx, actor, id, version, input, requestID)
}

func (s *Service) Deactivate(ctx context.Context, actor auth.Actor, id string, version int32, confirmWithoutDefault bool, requestID string) error {
	if err := actor.Require(auth.PermissionSettingsManage); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" || version < 1 {
		return ErrValidation
	}
	current, err := s.store.Resolve(ctx, id, version)
	if err != nil {
		return err
	}
	if (current.DefaultStart || current.DefaultEnd) && !confirmWithoutDefault {
		return ErrValidation
	}
	return s.store.Deactivate(ctx, actor, id, version, requestID)
}

func (input Input) Validate() error {
	if input.Label == "" || input.Address == "" || utf8.RuneCountInString(input.Label) > maxLabelRunes ||
		utf8.RuneCountInString(input.Address) > maxAddressRunes || !validLatitude(input.Latitude) || !validLongitude(input.Longitude) ||
		(input.Latitude == 0 && input.Longitude == 0) {
		return ErrValidation
	}
	return nil
}

func normalize(input *Input) {
	input.Label = strings.TrimSpace(input.Label)
	input.Address = strings.TrimSpace(input.Address)
}

func validLatitude(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= -90 && value <= 90
}

func validLongitude(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= -180 && value <= 180
}
