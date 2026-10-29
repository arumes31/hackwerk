package postgres

import (
	"errors"
	"testing"

	"example.invalid/hackplan/internal/driver"
)

func TestExceptionDensityErrorEnforcesWindowLimit(t *testing.T) {
	if err := exceptionDensityError(driver.MaxExceptionsPerWindow - 1); err != nil {
		t.Fatalf("exceptionDensityError(limit-1) = %v", err)
	}
	for _, existing := range []int64{driver.MaxExceptionsPerWindow, driver.MaxExceptionsPerWindow + 50} {
		if err := exceptionDensityError(existing); !errors.Is(err, driver.ErrExceptionLimit) || !errors.Is(err, driver.ErrValidation) {
			t.Fatalf("exceptionDensityError(%d) = %v", existing, err)
		}
	}
}
