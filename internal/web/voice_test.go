package web

import (
	"bytes"
	"errors"
	"mime/multipart"
	"os"
	"runtime"
	"testing"
	"time"

	"example.invalid/hackplan/internal/config"
)

func TestReceiveVoiceUploadValidWebMUsesRestrictiveTempFile(t *testing.T) {
	cfg := config.Voice{TempDir: t.TempDir(), MaxBytes: 1024, MaxDuration: 90 * time.Second}
	reader := voiceMultipart(t, []byte{0x1a, 0x45, 0xdf, 0xa3, 0, 0, 0, 0}, "audio/webm", "1000")
	file, duration, mediaType, err := receiveVoiceUpload(reader, cfg)
	if err != nil {
		t.Fatal(err)
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	defer func() { _ = file.Close() }()
	info, _ := file.Stat()
	if (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) || duration != time.Second || mediaType != "audio/webm" {
		t.Fatalf("mode/duration/type = %v/%v/%s", info.Mode().Perm(), duration, mediaType)
	}
}
func TestReceiveVoiceUploadRejectsEmptyWrongTypeLargeAndDuration(t *testing.T) {
	cfg := config.Voice{TempDir: t.TempDir(), MaxBytes: 8, MaxDuration: 2 * time.Second}
	tests := []struct {
		name              string
		audio             []byte
		content, duration string
		want              error
	}{{"empty", nil, "audio/webm", "1000", errVoiceEmpty}, {"type", []byte("nope"), "audio/webm", "1000", errVoiceType}, {"large", append([]byte{0x1a, 0x45, 0xdf, 0xa3}, make([]byte, 9)...), "audio/webm", "1000", errVoiceTooLarge}, {"duration", []byte{0x1a, 0x45, 0xdf, 0xa3}, "audio/webm", "3000", errVoiceDuration}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, _, _, err := receiveVoiceUpload(voiceMultipart(t, tt.audio, tt.content, tt.duration), cfg)
			if file != nil {
				_ = file.Close()
				_ = os.Remove(file.Name())
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			entries, _ := os.ReadDir(cfg.TempDir)
			if len(entries) != 0 {
				t.Fatalf("temporary files remain: %v", entries)
			}
		})
	}
}
func voiceMultipart(t *testing.T, audio []byte, contentType, duration string) *multipart.Reader {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("duration_ms", duration)
	header := make(map[string][]string)
	header["Content-Disposition"] = []string{`form-data; name="audio"; filename="input.bin"`}
	header["Content-Type"] = []string{contentType}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(audio)
	_ = writer.Close()
	return multipart.NewReader(bytes.NewReader(body.Bytes()), writer.Boundary())
}
