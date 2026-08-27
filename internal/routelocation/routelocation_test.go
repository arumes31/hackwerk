package routelocation

import (
	"context"
	"errors"
	"math"
	"testing"

	"example.invalid/hackplan/internal/auth"
)

type storeStub struct {
	locations       []Location
	activeLocations []Location
	err             error
	createCalls     int
	updateCalls     int
	deactivateCalls int
	lastInput       Input
	lastID          string
	lastVersion     int32
	defaultStart    Location
	resolved        Location
}

func (store *storeStub) List(context.Context) ([]Location, error) { return store.locations, store.err }
func (store *storeStub) ListActive(context.Context) ([]Location, error) {
	return store.activeLocations, store.err
}
func (store *storeStub) DefaultStart(context.Context) (Location, error) {
	return store.defaultStart, store.err
}
func (store *storeStub) Resolve(_ context.Context, id string, version int32) (Location, error) {
	store.lastID, store.lastVersion = id, version
	return store.resolved, store.err
}
func (store *storeStub) Create(_ context.Context, _ auth.Actor, input Input, _ string) (Location, error) {
	store.createCalls++
	store.lastInput = input
	return Location{ID: "location-1"}, store.err
}
func (store *storeStub) Update(_ context.Context, _ auth.Actor, id string, version int32, input Input, _ string) (Location, error) {
	store.updateCalls++
	store.lastID, store.lastVersion, store.lastInput = id, version, input
	return Location{ID: id, Version: version + 1}, store.err
}
func (store *storeStub) Deactivate(_ context.Context, _ auth.Actor, id string, version int32, _ string) error {
	store.deactivateCalls++
	store.lastID, store.lastVersion = id, version
	return store.err
}

func testAdmin() auth.Actor { return auth.Actor{UserID: "admin", Role: auth.RoleAdmin} }

func validInput() Input {
	return Input{Label: "Betriebshof", Address: "Waldweg 1, 4710 Grieskirchen", Latitude: 48.234567, Longitude: 13.987654}
}

func TestNewAndListsEnforceTheirPermissions(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) error = nil")
	}
	store := &storeStub{locations: []Location{{ID: "all"}}, activeLocations: []Location{{ID: "active", Active: true}}}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.List(t.Context(), auth.Actor{UserID: "driver", Role: auth.RoleDriver}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("List() driver error = %v, want forbidden", err)
	}
	if got, err := service.List(t.Context(), testAdmin()); err != nil || len(got) != 1 || got[0].ID != "all" {
		t.Fatalf("List() = %#v, %v", got, err)
	}
	if _, err := service.ListActive(t.Context(), auth.Actor{UserID: "driver", Role: auth.RoleDriver}); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("ListActive() driver error = %v, want forbidden", err)
	}
	if got, err := service.ListActive(t.Context(), testAdmin()); err != nil || len(got) != 1 || got[0].ID != "active" {
		t.Fatalf("ListActive() = %#v, %v", got, err)
	}
}

func TestCreateNormalizesConfirmedLocationAndRequiresSettingsPermission(t *testing.T) {
	store := &storeStub{}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	input := validInput()
	if _, err := service.Create(t.Context(), auth.Actor{UserID: "driver", Role: auth.RoleDriver}, input, "request"); !errors.Is(err, auth.ErrForbidden) || store.createCalls != 0 {
		t.Fatalf("driver Create() error/calls = %v/%d", err, store.createCalls)
	}
	input.Label, input.Address = "  Betriebshof  ", "  Waldweg 1, 4710 Grieskirchen  "
	input.DefaultStart = true
	created, err := service.Create(t.Context(), testAdmin(), input, "request")
	if err != nil || created.ID != "location-1" {
		t.Fatalf("Create() = %#v, %v", created, err)
	}
	if store.createCalls != 1 || store.lastInput.Label != "Betriebshof" || store.lastInput.Address != "Waldweg 1, 4710 Grieskirchen" || !store.lastInput.DefaultStart {
		t.Fatalf("normalized Create() = %#v", store.lastInput)
	}
}

func TestUpdateAndDeactivateValidateVersionAndForward(t *testing.T) {
	store := &storeStub{}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		id      string
		version int32
	}{
		{name: "blank id", version: 1},
		{name: "zero version", id: "location-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Update(t.Context(), testAdmin(), test.id, test.version, validInput(), "request"); !errors.Is(err, ErrValidation) {
				t.Fatalf("Update() error = %v, want validation", err)
			}
			if err := service.Deactivate(t.Context(), testAdmin(), test.id, test.version, false, "request"); !errors.Is(err, ErrValidation) {
				t.Fatalf("Deactivate() error = %v, want validation", err)
			}
		})
	}
	input := validInput()
	input.DefaultEnd = true
	updated, err := service.Update(t.Context(), testAdmin(), "location-1", 4, input, "request")
	if err != nil || updated.Version != 5 || store.updateCalls != 1 || store.lastID != "location-1" || store.lastVersion != 4 || !store.lastInput.DefaultEnd {
		t.Fatalf("Update() = %#v, %v; store=%#v", updated, err, store)
	}
	store.resolved = Location{ID: "location-1", Active: true, Version: 5}
	if err := service.Deactivate(t.Context(), testAdmin(), "location-1", 5, false, "request"); err != nil || store.deactivateCalls != 1 || store.lastVersion != 5 {
		t.Fatalf("Deactivate() error/store = %v/%#v", err, store)
	}
}

func TestDeactivateRequiresConfirmationForCurrentDefault(t *testing.T) {
	store := &storeStub{resolved: Location{ID: "location-1", Active: true, DefaultStart: true, Version: 3}}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Deactivate(t.Context(), testAdmin(), "location-1", 3, false, "request"); !errors.Is(err, ErrValidation) || store.deactivateCalls != 0 {
		t.Fatalf("unconfirmed Deactivate() error/calls = %v/%d", err, store.deactivateCalls)
	}
	if err := service.Deactivate(t.Context(), testAdmin(), "location-1", 3, true, "request"); err != nil || store.deactivateCalls != 1 {
		t.Fatalf("confirmed Deactivate() error/calls = %v/%d", err, store.deactivateCalls)
	}
}

func TestInputValidateRejectsInvalidLabelsAddressesAndCoordinates(t *testing.T) {
	tooLongLabel := string(make([]rune, maxLabelRunes+1))
	tooLongAddress := string(make([]rune, maxAddressRunes+1))
	tests := []struct {
		name  string
		input Input
	}{
		{name: "blank label", input: Input{Address: "Adresse", Latitude: 1, Longitude: 1}},
		{name: "blank address", input: Input{Label: "Ort", Latitude: 1, Longitude: 1}},
		{name: "long label", input: Input{Label: tooLongLabel, Address: "Adresse", Latitude: 1, Longitude: 1}},
		{name: "long address", input: Input{Label: "Ort", Address: tooLongAddress, Latitude: 1, Longitude: 1}},
		{name: "latitude range", input: Input{Label: "Ort", Address: "Adresse", Latitude: 90.1, Longitude: 1}},
		{name: "longitude range", input: Input{Label: "Ort", Address: "Adresse", Latitude: 1, Longitude: 180.1}},
		{name: "zero point", input: Input{Label: "Ort", Address: "Adresse", Latitude: 0, Longitude: 0}},
		{name: "not a number", input: Input{Label: "Ort", Address: "Adresse", Latitude: math.NaN(), Longitude: 1}},
		{name: "infinite", input: Input{Label: "Ort", Address: "Adresse", Latitude: 1, Longitude: math.Inf(1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.input.Validate(); !errors.Is(err, ErrValidation) {
				t.Fatalf("Validate() error = %v, want validation", err)
			}
		})
	}
}

func TestResolveRequiresRoutePlanningAndForwardsOnlyStoredReference(t *testing.T) {
	store := &storeStub{resolved: Location{ID: "location-1", Active: true, Version: 3}}
	service, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(t.Context(), auth.Actor{UserID: "driver", Role: auth.RoleDriver}, "location-1", 3); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("driver Resolve() error = %v, want forbidden", err)
	}
	if _, err := service.Resolve(t.Context(), testAdmin(), " ", 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("blank Resolve() error = %v, want not found", err)
	}
	value, err := service.Resolve(t.Context(), testAdmin(), "location-1", 3)
	if err != nil || value.ID != "location-1" || store.lastID != "location-1" || store.lastVersion != 3 {
		t.Fatalf("Resolve() = %#v, %v; store=%#v", value, err, store)
	}
}
