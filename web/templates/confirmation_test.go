package templates

import (
	"testing"
	"time"
)

func TestConfirmationDateTimeUsesGermanWeekdayAndViennaTime(t *testing.T) {
	t.Parallel()

	got := confirmationDateTime(time.Date(2026, time.September, 1, 6, 30, 0, 0, time.UTC))
	want := "Dienstag, 01.09.2026 · 08:30"
	if got != want {
		t.Fatalf("confirmationDateTime() = %q, want %q", got, want)
	}
}
