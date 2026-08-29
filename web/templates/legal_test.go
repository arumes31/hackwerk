package templates

import (
	"testing"
	"time"
)

func TestLegalDurationUsesGermanWholeMinuteText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value time.Duration
		want  string
	}{
		{name: "nonpositive", want: "bis zum Sitzungsende"},
		{name: "one minute", value: time.Minute, want: "1 Minute"},
		{name: "whole minutes", value: 45 * time.Minute, want: "45 Minuten"},
		{name: "one hour", value: time.Hour, want: "1 Stunde"},
		{name: "whole hours", value: 8 * time.Hour, want: "8 Stunden"},
		{name: "partial minute", value: 90 * time.Second, want: "1m30s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := legalDuration(test.value); got != test.want {
				t.Fatalf("legalDuration(%s) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}
