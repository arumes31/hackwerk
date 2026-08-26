package postgres

import (
	"errors"
	"math"
	"testing"

	"example.invalid/hackplan/internal/customers"
)

func TestPageValuesRejectsOverflow(t *testing.T) {
	t.Parallel()

	if _, _, err := pageValues(math.MaxInt, 100); !errors.Is(err, customers.ErrValidation) {
		t.Fatalf("pageValues() error = %v, want validation error", err)
	}
	offset, size, err := pageValues(2, 25)
	if err != nil || offset != 25 || size != 25 {
		t.Fatalf("pageValues() = %d, %d, %v", offset, size, err)
	}
}

func TestJobIntegerValuesRejectsOutOfRangeValues(t *testing.T) {
	t.Parallel()

	input := customers.JobInput{
		EstimatedHackMinutes:      customers.MaxJobDurationMinutes + 1,
		EstimatedTransportMinutes: 0,
		TransportTripCount:        0,
	}
	if _, _, _, err := jobIntegerValues(input); !errors.Is(err, customers.ErrValidation) {
		t.Fatalf("jobIntegerValues() error = %v, want validation error", err)
	}
}
