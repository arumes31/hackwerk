package planning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"example.invalid/hackplan/internal/auth"
)

type startStoreFake struct {
	loadCalls int
}

func (store *startStoreFake) LoadSnapshot(_ context.Context, _ string, _, _ time.Time) (Snapshot, error) {
	store.loadCalls++
	return Snapshot{}, errors.New("LoadSnapshot must not be called")
}

func (*startStoreFake) SaveRun(context.Context, auth.Actor, Snapshot, time.Time, time.Time, []Suggestion, Config) (Run, error) {
	return Run{}, nil
}
func (*startStoreFake) ListRun(context.Context, string) (Run, error) { return Run{}, nil }
func (*startStoreFake) Adopt(context.Context, auth.Actor, string, string) (string, error) {
	return "", nil
}
func (*startStoreFake) ClusterEntries(context.Context) ([]ClusterEntry, error) { return nil, nil }

type startAvailabilityFake struct{}

func (startAvailabilityFake) Resolve(context.Context, auth.Actor, string, time.Time, time.Time) ([]Interval, error) {
	return nil, nil
}

type defaultStartFake struct {
	point Point
	err   error
}

func (provider defaultStartFake) DefaultStart(context.Context) (Point, error) {
	return provider.point, provider.err
}

func TestSuggestRequiresConfiguredDynamicDefaultStart(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 8, 0, 0, 0, time.UTC)
	actor := auth.Actor{UserID: "admin", Role: auth.RoleAdmin}
	tests := []struct {
		name     string
		option   Option
		wantText string
	}{
		{name: "missing provider"},
		{name: "invalid point", option: WithDefaultStartProvider(defaultStartFake{})},
		{name: "provider failure", option: WithDefaultStartProvider(defaultStartFake{err: errors.New("settings unavailable")}), wantText: "resolving default start"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &startStoreFake{}
			options := make([]Option, 0, 1)
			if test.option != nil {
				options = append(options, test.option)
			}
			service, err := New(store, startAvailabilityFake{}, NewHaversineRouter(1.3, 55), testConfig(t), func() time.Time { return now }, options...)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Suggest(t.Context(), actor, "job")
			if !errors.Is(err, ErrConfiguration) {
				t.Fatalf("Suggest() error = %v, want ErrConfiguration", err)
			}
			if test.wantText != "" && !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("Suggest() error = %v, want %q", err, test.wantText)
			}
			if store.loadCalls != 0 {
				t.Fatalf("LoadSnapshot() calls = %d, want 0", store.loadCalls)
			}
		})
	}
}

func TestGenerateWithDefaultStartRejectsMissingPoint(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 1, 5, 1, 0, 0, time.UTC)
	_, err := GenerateWithDefaultStart(t.Context(), testSnapshot(now), NewHaversineRouter(1.3, 55), testConfig(t), Point{}, now)
	if !errors.Is(err, ErrConfiguration) {
		t.Fatalf("GenerateWithDefaultStart() error = %v, want ErrConfiguration", err)
	}
}
