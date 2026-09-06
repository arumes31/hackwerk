package assets

import (
	"os"
	"strings"
	"testing"
)

func TestVoiceCapturePrefillsSelectedAudioDurationWithoutSubmitting(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("src/app.js")
	if err != nil {
		t.Fatal(err)
	}
	javascript := string(source)
	start := strings.Index(javascript, "const prefillAudioDuration =")
	end := strings.Index(javascript, "const newUploadKey =")
	if start < 0 || end <= start {
		t.Fatal("voice duration metadata helper is missing")
	}
	helper := javascript[start:end]
	for _, contract := range []string{
		`[data-voice-file]`,
		`[data-voice-duration]`,
		`loadedmetadata`,
		`durationchange`,
		`URL.createObjectURL(blob)`,
		`URL.revokeObjectURL(objectURL)`,
		`Math.ceil(seconds)`,
	} {
		if !strings.Contains(javascript, contract) {
			t.Errorf("voice duration enhancement is missing %q", contract)
		}
	}
	for _, forbidden := range []string{"uploadAudio(", "request.send(", ".submit("} {
		if strings.Contains(helper, forbidden) {
			t.Errorf("duration metadata helper must not submit automatically; found %q", forbidden)
		}
	}
}
