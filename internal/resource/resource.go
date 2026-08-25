// Package resource implements generic operational resource rules.
package resource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
)

var (
	ErrConflict   = errors.New("resource: version conflict")
	ErrNotFound   = errors.New("resource: not found")
	ErrValidation = errors.New("resource: validation failed")
)

type Type string

const (
	TypeChipper          Type = "chipper"
	TypeTransportVehicle Type = "transport_vehicle"
	TypeTrailer          Type = "trailer"
	TypeOther            Type = "other"
)

type Capacity struct {
	VolumeM3  *float64 `json:"volume_m3,omitempty"`
	PayloadKG *int32   `json:"payload_kg,omitempty"`
	Seats     *int32   `json:"seats,omitempty"`
}

type Input struct {
	Type         Type
	Name         string
	IsExclusive  bool
	Capacity     Capacity
	InternalNote string
}

type Resource struct {
	ID           string
	Type         Type
	Name         string
	IsExclusive  bool
	IsActive     bool
	Capacity     Capacity
	InternalNote string
	Version      int32
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Store interface {
	List(context.Context) ([]Resource, error)
	Create(context.Context, auth.Actor, Input, string) (string, error)
	Update(context.Context, auth.Actor, string, int32, Input, string) error
	Deactivate(context.Context, auth.Actor, string, int32, string) error
}

type Service struct{ store Store }

func New(store Store) (*Service, error) {
	if store == nil {
		return nil, errors.New("resource: store is required")
	}
	return &Service{store: store}, nil
}

func (s *Service) List(ctx context.Context, actor auth.Actor) ([]Resource, error) {
	if err := actor.Require(auth.PermissionResourceManage); err != nil {
		return nil, err
	}
	return s.store.List(ctx)
}

func (s *Service) Create(ctx context.Context, actor auth.Actor, input Input, requestID string) (string, error) {
	if err := actor.Require(auth.PermissionResourceManage); err != nil {
		return "", err
	}
	normalize(&input)
	if err := input.Validate(); err != nil {
		return "", err
	}
	return s.store.Create(ctx, actor, input, requestID)
}

func (s *Service) Update(ctx context.Context, actor auth.Actor, id string, version int32, input Input, requestID string) error {
	if err := actor.Require(auth.PermissionResourceManage); err != nil {
		return err
	}
	normalize(&input)
	if strings.TrimSpace(id) == "" || version < 1 {
		return ErrValidation
	}
	if err := input.Validate(); err != nil {
		return err
	}
	return s.store.Update(ctx, actor, id, version, input, requestID)
}

func (s *Service) Deactivate(ctx context.Context, actor auth.Actor, id string, version int32, requestID string) error {
	if err := actor.Require(auth.PermissionResourceManage); err != nil {
		return err
	}
	if strings.TrimSpace(id) == "" || version < 1 {
		return ErrValidation
	}
	return s.store.Deactivate(ctx, actor, id, version, requestID)
}

func (i Input) Validate() error {
	if !i.Type.Valid() || i.Name == "" || len([]rune(i.Name)) > 200 || len([]rune(i.InternalNote)) > 4000 {
		return ErrValidation
	}
	if i.Capacity.VolumeM3 != nil && (*i.Capacity.VolumeM3 <= 0 || *i.Capacity.VolumeM3 > 1_000_000 || math.IsNaN(*i.Capacity.VolumeM3) || math.IsInf(*i.Capacity.VolumeM3, 0)) {
		return fmt.Errorf("%w: invalid volume capacity", ErrValidation)
	}
	if i.Capacity.PayloadKG != nil && (*i.Capacity.PayloadKG <= 0 || *i.Capacity.PayloadKG > 10_000_000) {
		return fmt.Errorf("%w: invalid payload capacity", ErrValidation)
	}
	if i.Capacity.Seats != nil && (*i.Capacity.Seats <= 0 || *i.Capacity.Seats > 1000) {
		return fmt.Errorf("%w: invalid seat capacity", ErrValidation)
	}
	return nil
}

func (t Type) Valid() bool {
	return t == TypeChipper || t == TypeTransportVehicle || t == TypeTrailer || t == TypeOther
}

func EncodeCapacity(capacity Capacity) ([]byte, error) {
	payload, err := json.Marshal(capacity)
	if err != nil {
		return nil, fmt.Errorf("resource: encoding capacity: %w", err)
	}
	return payload, nil
}

func DecodeCapacity(payload []byte) (Capacity, error) {
	var capacity Capacity
	if err := json.Unmarshal(payload, &capacity); err != nil {
		return Capacity{}, fmt.Errorf("resource: decoding capacity: %w", err)
	}
	return capacity, nil
}

func normalize(input *Input) {
	input.Name = strings.TrimSpace(input.Name)
	input.InternalNote = strings.TrimSpace(input.InternalNote)
}
