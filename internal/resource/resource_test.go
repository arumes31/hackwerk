package resource

import (
	"context"
	"errors"
	"testing"

	"example.invalid/hackplan/internal/auth"
)

type storeStub struct{ createCalls int }

func (s *storeStub) List(context.Context) ([]Resource, error) { return nil, nil }
func (s *storeStub) Create(context.Context, auth.Actor, Input, string) (string, error) {
	s.createCalls++
	return "resource-id", nil
}
func (s *storeStub) Update(context.Context, auth.Actor, string, int32, Input, string) error {
	return nil
}
func (s *storeStub) Deactivate(context.Context, auth.Actor, string, int32, string) error {
	return nil
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
}
