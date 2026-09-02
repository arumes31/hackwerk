package templates

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestLoginOriginalSceneDoesNotRequireInlineStyleCSPExceptions(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := loginOriginalScene().Render(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), ` style=`) {
		t.Fatal("legacy login illustration must not emit inline style attributes")
	}
}
