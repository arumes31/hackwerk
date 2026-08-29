package web

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/customers"
	"example.invalid/hackplan/internal/voice"
	"example.invalid/hackplan/web/templates"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func registerVoiceRoutes(router chi.Router, dependencies Dependencies, page templates.PageData) {
	csrfCookie := dependencies.Config.Auth.CSRFCookieName
	router.Get("/voice", voicePage(dependencies.Voice, dependencies.Config, page, csrfCookie, dependencies.Logger))
	router.Post("/api/v1/voice/drafts", uploadVoice(dependencies.Voice, dependencies.Config.Voice, dependencies.Logger))
	router.Get("/api/v1/voice/drafts/{draftID}", voiceDraftStatus(dependencies.Voice))
	router.Get("/voice/drafts/{draftID}", voiceReview(dependencies.Voice, page, csrfCookie, dependencies.Logger))
	router.Post("/voice/drafts/{draftID}/commit", commitVoice(dependencies.Voice, page, csrfCookie, dependencies.Logger))
	router.Post("/voice/drafts/{draftID}/discard", discardVoice(dependencies.Voice, dependencies.Logger))
	router.With(requirePermission(auth.PermissionAuditView, page, dependencies.Logger)).Get("/admin/voice-recordings", voiceRecordingsPage(dependencies.Voice, page, csrfCookie, dependencies.Logger))
	router.With(requirePermission(auth.PermissionAuditView, page, dependencies.Logger)).Get("/admin/voice-recordings/{recordingID}/audio", voiceRecordingAudio(dependencies.Voice, dependencies.Logger))
}

func voicePage(service *voice.Service, cfg config.Config, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		render(response, request, templates.VoiceCapture(voiceCaptureData(request, service, cfg, page, csrfCookie, "")), http.StatusOK, logger)
	}
}

func voiceCaptureData(request *http.Request, service *voice.Service, cfg config.Config, page templates.PageData, csrfCookie, message string) templates.VoiceCaptureData {
	return templates.VoiceCaptureData{
		Shell: shell(request, page, csrfCookie), Enabled: service.Enabled(), MaxBytes: cfg.Voice.MaxBytes,
		MaxSeconds: int(cfg.Voice.MaxDuration.Seconds()), ExternalProvider: cfg.Voice.Transcriber == "openai" || cfg.Voice.Extractor == "openai",
		LocalProvider: cfg.Voice.Transcriber == "whisper-local" || cfg.Voice.Transcriber == "whisper-tailscale", ProcessingMinutes: int(cfg.Voice.ProviderTimeout.Minutes()),
		ProviderNotice: cfg.Voice.ExternalProviderNote, Error: message,
	}
}

func uploadVoice(service *voice.Service, cfg config.Voice, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if !service.Enabled() {
			writeVoiceError(response, request, http.StatusServiceUnavailable, "voice_disabled", "Die Spracheingabe ist deaktiviert. Das manuelle Formular bleibt verfügbar.")
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, int64(cfg.MaxBytes)+(128<<10))
		reader, err := request.MultipartReader()
		if err != nil {
			writeVoiceError(response, request, http.StatusBadRequest, "invalid_upload", "Die Audiodatei ist ungültig. Bitte verwenden Sie die manuelle Erfassung.")
			return
		}
		file, duration, mediaType, err := receiveVoiceUpload(reader, cfg)
		if err != nil {
			writeVoiceUploadError(response, request, err)
			return
		}
		defer func() {
			name := file.Name()
			_ = file.Close()
			// #nosec G703 -- name comes only from os.CreateTemp in the configured private temp directory.
			if removeErr := os.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				logger.WarnContext(request.Context(), "temporary voice audio cleanup failed", slog.String("error_code", "voice_temp_cleanup_failed"))
			}
		}()
		draft, err := processVoiceUpload(request, service, cfg, file, duration, mediaType)
		if err != nil {
			if errors.Is(err, errVoiceType) || errors.Is(err, errVoiceDuration) {
				writeVoiceUploadError(response, request, err)
				return
			}
			status, code, message := mapVoiceError(err)
			logger.WarnContext(request.Context(), "voice upload rejected", slog.String("error_code", code))
			writeVoiceError(response, request, status, code, message)
			return
		}
		location := "/voice/drafts/" + draft.ID
		response.Header().Set("Location", location)
		writeJSON(response, http.StatusAccepted, map[string]any{"draft_id": draft.ID, "status": draft.Status, "location": location})
	}
}

func cleanupVoiceFile(ctx context.Context, file *os.File, logger *slog.Logger) {
	if file == nil {
		return
	}
	name := file.Name()
	_ = file.Close()
	// #nosec G703 -- name comes only from os.CreateTemp in the configured private temp directory.
	if removeErr := os.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		logger.WarnContext(ctx, "temporary voice audio cleanup failed", slog.String("error_code", "voice_temp_cleanup_failed"))
	}
}

func processVoiceUpload(request *http.Request, service *voice.Service, cfg config.Voice, file *os.File, duration time.Duration, mediaType string) (voice.Draft, error) {
	session, _ := sessionFromContext(request.Context())
	return service.EnqueuePrepared(request.Context(), session.Actor, func() (voice.Audio, voice.Metadata, error) {
		actualDuration, err := inspectAudioDuration(file, mediaType)
		if err != nil {
			return voice.Audio{}, voice.Metadata{}, errVoiceType
		}
		if actualDuration > cfg.MaxDuration || !voiceDurationsMatch(duration, actualDuration) {
			return voice.Audio{}, voice.Metadata{}, errVoiceDuration
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return voice.Audio{}, voice.Metadata{}, err
		}
		return voice.Audio{
			Reader: file, Filename: "aufnahme" + mediaExtension(mediaType), ContentType: mediaType, Size: fileSize(file),
		}, voice.Metadata{RecordedAt: time.Now(), Duration: actualDuration}, nil
	})
}

func uploadVoiceNative(dependencies Dependencies, page templates.PageData) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		service := dependencies.Voice
		cfg := dependencies.Config
		if !service.Enabled() {
			renderNativeVoiceError(response, request, service, cfg, page, http.StatusServiceUnavailable, "Die Spracheingabe ist deaktiviert. Das manuelle Formular bleibt verfügbar.", dependencies.Logger)
			return
		}
		if !sameOrigin(request) {
			renderNativeVoiceError(response, request, service, cfg, page, http.StatusForbidden, "Das Sicherheitsmerkmal ist ungültig oder abgelaufen. Bitte laden Sie die Seite neu und wählen Sie die Datei erneut.", dependencies.Logger)
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, int64(cfg.Voice.MaxBytes)+(128<<10))
		reader, err := request.MultipartReader()
		if err != nil {
			renderNativeVoiceError(response, request, service, cfg, page, http.StatusBadRequest, "Die Audiodatei ist ungültig. Bitte wählen Sie eine unterstützte Datei oder erfassen Sie den Auftrag manuell.", dependencies.Logger)
			return
		}
		received, err := receiveNativeVoiceUpload(reader, cfg.Voice, func(presented string) bool {
			return validNativeVoiceCSRF(request, dependencies.Identity, cfg.Auth.CSRFCookieName, presented)
		})
		if err != nil {
			if errors.Is(err, errVoiceCSRF) {
				renderNativeVoiceError(response, request, service, cfg, page, http.StatusForbidden, "Das Sicherheitsmerkmal ist ungültig oder abgelaufen. Bitte laden Sie die Seite neu und wählen Sie die Datei erneut.", dependencies.Logger)
				return
			}
			status, _, message := voiceUploadError(err)
			renderNativeVoiceError(response, request, service, cfg, page, status, message, dependencies.Logger)
			return
		}
		defer cleanupVoiceFile(request.Context(), received.file, dependencies.Logger)
		draft, err := processVoiceUpload(request, service, cfg.Voice, received.file, received.duration, received.mediaType)
		if err != nil {
			if errors.Is(err, errVoiceType) || errors.Is(err, errVoiceDuration) {
				status, _, message := voiceUploadError(err)
				renderNativeVoiceError(response, request, service, cfg, page, status, message, dependencies.Logger)
				return
			}
			status, _, message := mapVoiceError(err)
			dependencies.Logger.WarnContext(request.Context(), "native voice upload rejected", slog.String("error_code", "voice_native_rejected"))
			renderNativeVoiceError(response, request, service, cfg, page, status, message, dependencies.Logger)
			return
		}
		http.Redirect(response, request, "/voice/drafts/"+draft.ID, http.StatusSeeOther)
	}
}

func validNativeVoiceCSRF(request *http.Request, identity *auth.Service, csrfCookieName, presented string) bool {
	if identity == nil || !sameOrigin(request) || presented == "" {
		return false
	}
	csrfCookie, err := request.Cookie(csrfCookieName)
	if err != nil || subtle.ConstantTimeCompare(auth.TokenHash(csrfCookie.Value), auth.TokenHash(presented)) != 1 {
		return false
	}
	session, ok := sessionFromContext(request.Context())
	return ok && identity.ValidateCSRF(session, presented)
}

func renderNativeVoiceError(response http.ResponseWriter, request *http.Request, service *voice.Service, cfg config.Config, page templates.PageData, status int, message string, logger *slog.Logger) {
	render(response, request, templates.VoiceCapture(voiceCaptureData(request, service, cfg, page, cfg.Auth.CSRFCookieName, message)), status, logger)
}

type receivedVoiceUpload struct {
	file      *os.File
	duration  time.Duration
	mediaType string
	csrfToken string
}

var errVoiceTooLarge = errors.New("voice upload too large")
var errVoiceEmpty = errors.New("voice upload empty")
var errVoiceType = errors.New("voice upload type")
var errVoiceDuration = errors.New("voice upload duration")
var errVoiceCSRF = errors.New("voice upload csrf")

func receiveVoiceUpload(reader *multipart.Reader, cfg config.Voice) (*os.File, time.Duration, string, error) {
	received, err := receiveVoiceUploadFields(reader, cfg)
	if err != nil {
		return nil, 0, "", err
	}
	return received.file, received.duration, received.mediaType, nil
}

func receiveVoiceUploadFields(reader *multipart.Reader, cfg config.Voice) (receivedVoiceUpload, error) {
	return receiveVoiceUploadFieldsVerified(reader, cfg, nil)
}

func receiveNativeVoiceUpload(reader *multipart.Reader, cfg config.Voice, verifyCSRF func(string) bool) (receivedVoiceUpload, error) {
	if verifyCSRF == nil {
		return receivedVoiceUpload{}, errVoiceCSRF
	}
	return receiveVoiceUploadFieldsVerified(reader, cfg, verifyCSRF)
}

func receiveVoiceUploadFieldsVerified(reader *multipart.Reader, cfg config.Voice, verifyCSRF func(string) bool) (receivedVoiceUpload, error) {
	var file *os.File
	var duration time.Duration
	var declared string
	var csrfToken string
	csrfVerified := verifyCSRF == nil
	cleanup := func() {
		if file != nil {
			name := file.Name()
			_ = file.Close()
			_ = os.Remove(name)
		}
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			return receivedVoiceUpload{}, err
		}
		name := part.FormName()
		switch name {
		case "csrf_token":
			if csrfToken != "" {
				cleanup()
				return receivedVoiceUpload{}, errVoiceType
			}
			data, readErr := io.ReadAll(io.LimitReader(part, 257))
			if readErr != nil || len(data) > 256 {
				cleanup()
				return receivedVoiceUpload{}, errVoiceType
			}
			csrfToken = strings.TrimSpace(string(data))
			if verifyCSRF != nil && !verifyCSRF(csrfToken) {
				cleanup()
				return receivedVoiceUpload{}, errVoiceCSRF
			}
			csrfVerified = true
		case "duration_ms":
			data, readErr := io.ReadAll(io.LimitReader(part, 32))
			if readErr != nil {
				cleanup()
				return receivedVoiceUpload{}, readErr
			}
			millis, parseErr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			if parseErr != nil || millis <= 0 {
				cleanup()
				return receivedVoiceUpload{}, errVoiceDuration
			}
			duration = time.Duration(millis) * time.Millisecond
		case "duration_seconds":
			data, readErr := io.ReadAll(io.LimitReader(part, 32))
			if readErr != nil {
				cleanup()
				return receivedVoiceUpload{}, readErr
			}
			seconds, parseErr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			if parseErr != nil || seconds <= 0 {
				cleanup()
				return receivedVoiceUpload{}, errVoiceDuration
			}
			duration = time.Duration(seconds) * time.Second
		case "audio":
			if !csrfVerified {
				cleanup()
				return receivedVoiceUpload{}, errVoiceCSRF
			}
			if file != nil {
				cleanup()
				return receivedVoiceUpload{}, errVoiceType
			}
			declared = strings.ToLower(strings.TrimSpace(part.Header.Get("Content-Type")))
			if err = os.MkdirAll(cfg.TempDir, 0o700); err != nil {
				cleanup()
				return receivedVoiceUpload{}, err
			}
			// #nosec G302 -- this is a directory; 0700 is the required owner-only directory mode.
			if err = os.Chmod(cfg.TempDir, 0o700); err != nil {
				cleanup()
				return receivedVoiceUpload{}, err
			}
			file, err = os.CreateTemp(cfg.TempDir, "voice-*.audio")
			if err != nil {
				cleanup()
				return receivedVoiceUpload{}, err
			}
			if err = os.Chmod(file.Name(), 0o600); err != nil {
				cleanup()
				return receivedVoiceUpload{}, err
			}
			written, copyErr := io.Copy(file, io.LimitReader(part, int64(cfg.MaxBytes)+1))
			if copyErr != nil {
				cleanup()
				return receivedVoiceUpload{}, copyErr
			}
			if written == 0 {
				cleanup()
				return receivedVoiceUpload{}, errVoiceEmpty
			}
			if written > int64(cfg.MaxBytes) {
				cleanup()
				return receivedVoiceUpload{}, errVoiceTooLarge
			}
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1024))
		}
		_ = part.Close()
	}
	if file == nil {
		cleanup()
		return receivedVoiceUpload{}, errVoiceEmpty
	}
	if duration <= 0 || duration > cfg.MaxDuration {
		cleanup()
		return receivedVoiceUpload{}, errVoiceDuration
	}
	mediaType, err := validateAudioFile(file, declared)
	if err != nil {
		cleanup()
		return receivedVoiceUpload{}, err
	}
	return receivedVoiceUpload{file: file, duration: duration, mediaType: mediaType, csrfToken: csrfToken}, nil
}

func validateAudioFile(file *os.File, declared string) (string, error) {
	buffer := make([]byte, 512)
	count, err := file.ReadAt(buffer, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	buffer = buffer[:count]
	detected := ""
	switch {
	case len(buffer) >= 4 && string(buffer[:4]) == "OggS":
		detected = "audio/ogg"
	case len(buffer) >= 12 && string(buffer[:4]) == "RIFF" && string(buffer[8:12]) == "WAVE":
		detected = "audio/wav"
	case len(buffer) >= 4 && buffer[0] == 0x1a && buffer[1] == 0x45 && buffer[2] == 0xdf && buffer[3] == 0xa3:
		detected = "audio/webm"
	default:
		return "", errVoiceType
	}
	allowedDeclared := map[string]bool{"audio/webm": true, "video/webm": true, "audio/ogg": true, "application/ogg": true, "audio/wav": true, "audio/x-wav": true}
	if !allowedDeclared[strings.Split(declared, ";")[0]] {
		return "", errVoiceType
	}
	return detected, nil
}
func mediaExtension(mediaType string) string {
	return map[string]string{"audio/webm": ".webm", "audio/ogg": ".ogg", "audio/wav": ".wav"}[mediaType]
}
func fileSize(file *os.File) int64 {
	info, err := file.Stat()
	if err != nil {
		return 0
	}
	return info.Size()
}
func writeVoiceUploadError(response http.ResponseWriter, request *http.Request, err error) {
	status, code, message := voiceUploadError(err)
	writeVoiceError(response, request, status, code, message)
}

func voiceUploadError(err error) (int, string, string) {
	switch {
	case errors.Is(err, errVoiceTooLarge):
		return http.StatusRequestEntityTooLarge, "audio_too_large", "Die Aufnahme überschreitet das erlaubte Größenlimit. Bitte kürzer aufnehmen oder manuell erfassen."
	case errors.Is(err, errVoiceType):
		return http.StatusUnsupportedMediaType, "unsupported_audio", "Dieses Audioformat wird nicht unterstützt. Bitte neu aufnehmen oder manuell erfassen."
	case errors.Is(err, errVoiceEmpty):
		return http.StatusUnprocessableEntity, "empty_audio", "Die Aufnahme ist leer. Bitte neu aufnehmen oder manuell erfassen."
	case errors.Is(err, errVoiceDuration):
		return http.StatusUnprocessableEntity, "invalid_duration", "Die Aufnahmedauer fehlt oder überschreitet das erlaubte Limit."
	default:
		return http.StatusInternalServerError, "upload_failed", "Die Aufnahme konnte nicht sicher zwischengespeichert werden."
	}
}

func voiceReview(service *voice.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		draft, err := service.Get(request.Context(), session.Actor, chi.URLParam(request, "draftID"))
		if err != nil {
			renderVoiceError(response, request, page, logger, err)
			return
		}
		var duplicates []customers.Duplicate
		if draft.Status == voice.StatusNeedsReview {
			duplicates, _ = service.Duplicates(request.Context(), session.Actor, draft)
		}
		render(response, request, templates.VoiceReview(templates.VoiceReviewData{Shell: shell(request, page, csrfCookie), Draft: draft, Values: voiceValues(draft), Duplicates: duplicates}), http.StatusOK, logger)
	}
}

func voiceDraftStatus(service *voice.Service) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		draft, err := service.Get(request.Context(), session.Actor, chi.URLParam(request, "draftID"))
		if err != nil {
			status, code, message := mapVoiceError(err)
			if errors.Is(err, voice.ErrNotFound) {
				status, code, message = http.StatusNotFound, "voice_not_found", "Der Entwurf wurde nicht gefunden."
			}
			writeVoiceError(response, request, status, code, message)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"status": draft.Status, "version": draft.Version})
	}
}

func voiceRecordingsPage(service *voice.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		const pageSize = int32(50)
		pageNumber := 1
		if raw := strings.TrimSpace(request.URL.Query().Get("page")); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 20_000 {
				http.Error(response, "Ungültige Seite.", http.StatusBadRequest)
				return
			}
			pageNumber = parsed
		}
		session, _ := sessionFromContext(request.Context())
		offset := int32(pageNumber-1) * pageSize
		recordings, err := service.ListRecordings(request.Context(), session.Actor, pageSize+1, offset)
		if err != nil {
			renderVoiceError(response, request, page, logger, err)
			return
		}
		hasNext := len(recordings) > int(pageSize)
		if hasNext {
			recordings = recordings[:pageSize]
		}
		render(response, request, templates.VoiceRecordings(templates.VoiceRecordingsData{Shell: shell(request, page, csrfCookie), Recordings: recordings, Page: pageNumber, HasPrevious: pageNumber > 1, HasNext: hasNext}), http.StatusOK, logger)
	}
}

func voiceRecordingAudio(service *voice.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		recording, err := service.RecordingAudio(request.Context(), session.Actor, chi.URLParam(request, "recordingID"))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, voice.ErrNotFound) {
				status = http.StatusNotFound
			} else if errors.Is(err, auth.ErrForbidden) {
				status = http.StatusForbidden
			}
			http.Error(response, http.StatusText(status), status)
			if status >= 500 {
				logger.ErrorContext(request.Context(), "voice recording playback failed", slog.String("error_code", "voice_recording_playback_failed"))
			}
			return
		}
		response.Header().Set("Cache-Control", "private, no-store")
		response.Header().Set("Content-Type", recording.ContentType)
		response.Header().Set("Content-Disposition", `inline; filename="aufnahme`+mediaExtension(recording.ContentType)+`"`)
		response.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(response, request, "", recording.RecordedAt, bytes.NewReader(recording.Bytes))
	}
}

func commitVoice(service *voice.Service, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		values := intakeValues(request)
		values.Source = "voice"
		input, parseErr := intakeInput(values)
		version, versionErr := parseVersion(request.Form.Get("version"))
		session, _ := sessionFromContext(request.Context())
		var created customers.CreatedIntake
		err := parseErr
		if err == nil {
			err = versionErr
		}
		if err == nil {
			created, err = service.Commit(request.Context(), session.Actor, voice.CommitInput{DraftID: chi.URLParam(request, "draftID"), ExistingCustomerID: request.Form.Get("existing_customer_id"), RequestID: middleware.GetReqID(request.Context()), ExpectedVersion: version, Reviewed: request.Form.Get("reviewed") == "true", DuplicateReviewed: request.Form.Get("duplicate_reviewed") == "true", Intake: input})
		}
		if err != nil {
			draft, getErr := service.Get(request.Context(), session.Actor, chi.URLParam(request, "draftID"))
			if getErr != nil {
				renderVoiceError(response, request, page, logger, getErr)
				return
			}
			duplicates, _ := service.Duplicates(request.Context(), session.Actor, draft)
			render(response, request, templates.VoiceReview(templates.VoiceReviewData{Shell: shell(request, page, csrfCookie), Draft: draft, Values: values, Duplicates: duplicates, Error: "Die Daten wurden nicht gespeichert. Prüfen Sie Pflichtfelder, Transport, Dublettenentscheidung und die Bestätigung ‚Daten geprüft‘."}), http.StatusUnprocessableEntity, logger)
			return
		}
		http.Redirect(response, request, "/customers/"+created.CustomerID, http.StatusSeeOther)
	}
}

func discardVoice(service *voice.Service, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		session, _ := sessionFromContext(request.Context())
		if err := service.Discard(request.Context(), session.Actor, chi.URLParam(request, "draftID")); err != nil {
			mutationError(response, err, logger, request, "voice_discard_rejected")
			return
		}
		http.Redirect(response, request, "/voice", http.StatusSeeOther)
	}
}

func voiceValues(draft voice.Draft) templates.IntakeValues {
	input := voice.IntakeFromFields(draft.Fields)
	values := defaultIntakeValues()
	values.FirstName = input.Customer.FirstName
	values.LastName = input.Customer.LastName
	values.CompanyName = input.Customer.CompanyName
	values.AddressFreeform = input.Customer.AddressFreeform
	values.Phone = input.Customer.PhoneRaw
	values.Email = input.Customer.Email
	values.Region = input.Job.Region
	values.JobType = string(input.Job.JobType)
	values.Volume = input.Job.VolumeM3
	values.HackDuration = strconv.Itoa(input.Job.EstimatedHackMinutes)
	if input.Job.EstimatedTransportMinutes > 0 {
		values.TransportDuration = strconv.Itoa(input.Job.EstimatedTransportMinutes)
	}
	if input.Job.TransportTripCount > 0 {
		values.Trips = strconv.Itoa(input.Job.TransportTripCount)
	}
	values.TransportMode = string(input.Job.TransportMode)
	values.PreferredStart = input.Job.PreferredStartDate
	values.PreferredEnd = input.Job.PreferredEndDate
	values.PreferenceText = input.Job.PreferenceText
	values.Urgency = string(input.Job.Urgency)
	values.Source = "voice"
	values.Note = input.InitialNote
	return values
}
func mapVoiceError(err error) (int, string, string) {
	switch {
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, "voice_forbidden", "Für diese Aktion fehlt die Berechtigung."
	case errors.Is(err, voice.ErrDisabled):
		return http.StatusServiceUnavailable, "voice_disabled", "Die Spracheingabe ist deaktiviert. Das manuelle Formular bleibt verfügbar."
	case errors.Is(err, voice.ErrRateLimit):
		return http.StatusTooManyRequests, "voice_rate_limited", "Zu viele Aufnahmen. Bitte kurz warten oder manuell erfassen."
	case errors.Is(err, voice.ErrValidation):
		return http.StatusUnprocessableEntity, "voice_invalid", "Die Aufnahme ist ungültig oder unvollständig."
	default:
		return http.StatusInternalServerError, "voice_failed", "Die Spracheingabe ist derzeit nicht verfügbar. Bitte manuell erfassen."
	}
}
func writeVoiceError(response http.ResponseWriter, request *http.Request, status int, code, message string) {
	writeJSON(response, status, map[string]any{"error": map[string]string{"code": code, "message": message, "request_id": middleware.GetReqID(request.Context())}})
}
func renderVoiceError(response http.ResponseWriter, request *http.Request, page templates.PageData, logger *slog.Logger, err error) {
	status := http.StatusInternalServerError
	message := "Der Entwurf ist derzeit nicht verfügbar."
	if errors.Is(err, voice.ErrNotFound) {
		status = http.StatusNotFound
		message = "Der Entwurf wurde nicht gefunden oder gehört einem anderen Benutzer."
	}
	render(response, request, templates.Error(page, status, "Sprachentwurf nicht verfügbar", message), status, logger)
}
