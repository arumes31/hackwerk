package resource

import (
	"context"
	"errors"
	"math"
	"testing"

	"example.invalid/hackplan/internal/auth"
)

type storeStub struct {
	resources       []Resource
	err             error
	createCalls     int
	updateCalls     int
	deactivateCalls int
	lastInput       Input
	lastID          string
	lastVersion     int32
}

func (s *storeStub) List(context.Context) ([]Resource, error) { return s.resources, s.err }
func (s *storeStub) Create(_ context.Context, _ auth.Actor, input Input, _ string) (string, error) {
	s.createCalls++
	s.lastInput = input
	return "resource-id", s.err
}
func (s *storeStub) Update(_ context.Context, _ auth.Actor, id string, version int32, input Input, _ string) error {
	s.updateCalls++
	s.lastID, s.lastVersion, s.lastInput = id, version, input
	return s.err
}
func (s *storeStub) Deactivate(_ context.Context, _ auth.Actor, id string, version int32, _ string) error {
	s.deactivateCalls++
	s.lastID, s.lastVersion = id, version
	return s.err
}

func TestNewAndListRequireStoreAndAdmin(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) error = nil")
	}
	storeErr := errors.New("store unavailable")
	store := &storeStub{resources: []Resource{{ID: "resource-1"}}, err: storeErr}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(t.Context(), auth.Actor{Role: auth.RoleDriver}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver List() error = %v, want %v", err, auth.ErrForbidden)
	}
	if _, err := service.List(t.Context(), auth.Actor{UserID: "admin", Role: auth.RoleAdmin}); !errors.Is(err, storeErr) {
		t.Fatalf("admin List() error = %v, want %v", err, storeErr)
	}
}

func TestCreateValidatesCapacityAndPermission(t *testing.T) {
	store := &storeStub{}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	volume := 12.5
	_, err = service.Create(t.Context(), auth.Actor{UserID: "driver", Role: auth.RoleDriver}, Input{
		Type: TypeChipper, Name: "Hackmaschine", IsExclusive: true, Capacity: Capacity{VolumeM3: &volume},
	}, "request")
	if !errors.Is(err, auth.ErrForbidden) || store.createCalls != 0 {
		t.Fatalf("driver create error = %v, calls = %d", err, store.createCalls)
	}
	volume = -1
	_, err = service.Create(t.Context(), auth.Actor{UserID: "admin", Role: auth.RoleAdmin}, Input{
		Type: TypeChipper, Name: "Hackmaschine", IsExclusive: true, Capacity: Capacity{VolumeM3: &volume},
	}, "request")
	if !errors.Is(err, ErrValidation) || store.createCalls != 0 {
		t.Fatalf("invalid capacity error = %v, calls = %d", err, store.createCalls)
	}
	volume = 12.5
	id, err := service.Create(t.Context(), auth.Actor{UserID: "admin", Role: auth.RoleAdmin}, Input{
		Type: TypeChipper, Name: "  Hackmaschine  ", IsExclusive: true,
		Capacity: Capacity{VolumeM3: &volume}, InternalNote: "  geprüft  ",
	}, "request")
	if err != nil || id != "resource-id" {
		t.Fatalf("valid Create() = %q, %v", id, err)
	}
	if store.createCalls != 1 || store.lastInput.Name != "Hackmaschine" || store.lastInput.InternalNote != "geprüft" {
		t.Fatalf("normalized Create() input/calls = %#v/%d", store.lastInput, store.createCalls)
	}
}

func TestUpdateAndDeactivateValidateAndForward(t *testing.T) {
	store := &storeStub{}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	admin := auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
	driver := auth.Actor{UserID: "driver", Role: auth.RoleDriver}
	valid := Input{Type: TypeTrailer, Name: "  Anhänger  ", InternalNote: "  einsatzbereit  "}

	if err := service.Update(t.Context(), driver, "resource-1", 1, valid, "request"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver Update() error = %v, want %v", err, auth.ErrForbidden)
	}
	for _, test := range []struct {
		name    string
		id      string
		version int32
	}{
		{name: "missing id", version: 1},
		{name: "missing version", id: "resource-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := service.Update(t.Context(), admin, test.id, test.version, valid, "request"); !errors.Is(err, ErrValidation) {
				t.Fatalf("Update() error = %v, want %v", err, ErrValidation)
			}
		})
	}
	if err := service.Update(t.Context(), admin, "resource-1", 2, valid, "request"); err != nil {
		t.Fatal(err)
	}
	if store.updateCalls != 1 || store.lastID != "resource-1" || store.lastVersion != 2 || store.lastInput.Name != "Anhänger" || store.lastInput.InternalNote != "einsatzbereit" {
		t.Fatalf("forwarded Update() = %#v", store)
	}

	if err := service.Deactivate(t.Context(), driver, "resource-1", 2, "request"); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver Deactivate() error = %v, want %v", err, auth.ErrForbidden)
	}
	if err := service.Deactivate(t.Context(), admin, "", 2, "request"); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid Deactivate() error = %v, want %v", err, ErrValidation)
	}
	if err := service.Deactivate(t.Context(), admin, "resource-1", 2, "request"); err != nil {
		t.Fatal(err)
	}
	if store.deactivateCalls != 1 || store.lastID != "resource-1" || store.lastVersion != 2 {
		t.Fatalf("forwarded Deactivate() = %#v", store)
	}
}

func TestInputValidateRejectsInvalidValues(t *testing.T) {
	tooLargeVolume := 1_000_001.0
	nanVolume := math.NaN()
	infiniteVolume := math.Inf(1)
	zeroPayload := int32(0)
	tooLargePayload := int32(10_000_001)
	zeroSeats := int32(0)
	tooManySeats := int32(1001)
	tests := []struct {
		name  string
		input Input
	}{
		{name: "invalid type", input: Input{Type: "crane", Name: "Kran"}},
		{name: "missing name", input: Input{Type: TypeOther}},
		{name: "long name", input: Input{Type: TypeOther, Name: string(make([]rune, 201))}},
		{name: "long note", input: Input{Type: TypeOther, Name: "Gerät", InternalNote: string(make([]rune, 4001))}},
		{name: "large volume", input: Input{Type: TypeOther, Name: "Gerät", Capacity: Capacity{VolumeM3: &tooLargeVolume}}},
		{name: "nan volume", input: Input{Type: TypeOther, Name: "Gerät", Capacity: Capacity{VolumeM3: &nanVolume}}},
		{name: "infinite volume", input: Input{Type: TypeOther, Name: "Gerät", Capacity: Capacity{VolumeM3: &infiniteVolume}}},
		{name: "zero payload", input: Input{Type: TypeOther, Name: "Gerät", Capacity: Capacity{PayloadKG: &zeroPayload}}},
		{name: "large payload", input: Input{Type: TypeOther, Name: "Gerät", Capacity: Capacity{PayloadKG: &tooLargePayload}}},
		{name: "zero seats", input: Input{Type: TypeOther, Name: "Gerät", Capacity: Capacity{Seats: &zeroSeats}}},
		{name: "many seats", input: Input{Type: TypeOther, Name: "Gerät", Capacity: Capacity{Seats: &tooManySeats}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.input.Validate(); !errors.Is(err, ErrValidation) {
				t.Fatalf("Validate() error = %v, want %v", err, ErrValidation)
			}
		})
	}
	for _, value := range []Type{TypeChipper, TypeTransportVehicle, TypeTrailer, TypeOther} {
		if !value.Valid() {
			t.Fatalf("Type.Valid(%q) = false", value)
		}
	}
}

func TestCapacityJSONRoundTrip(t *testing.T) {
	volume := 16.25
	payload, err := EncodeCapacity(Capacity{VolumeM3: &volume})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCapacity(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.VolumeM3 == nil || *decoded.VolumeM3 != volume {
		t.Fatalf("decoded capacity = %#v", decoded)
	}
	if _, err := DecodeCapacity([]byte("{")); err == nil {
		t.Fatal("DecodeCapacity(invalid) error = nil")
	}
}
