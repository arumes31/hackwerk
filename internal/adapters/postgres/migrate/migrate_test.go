package migrate

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunRejectsDirection(t *testing.T) {
	t.Parallel()

	err := Run(context.Background(), "postgres://invalid", Direction("sideways"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "unsupported direction") {
		t.Fatalf("Run() error = %v", err)
	}
}
