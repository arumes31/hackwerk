package web

import (
	"context"
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
	router.Get("/voice/drafts/{draftID}", voiceReview(dependencies.Voice, page, csrfCookie, dependencies.Logger))
	router.Post("/voice/drafts/{draftID}/commit", commitVoice(dependencies.Voice, page, csrfCookie, dependencies.Logger))
	router.Post("/voice/drafts/{draftID}/discard", discardVoice(dependencies.Voice, dependencies.Logger))
}

func voicePage(service *voice.Service, cfg config.Config, page templates.PageData, csrfCookie string, logger *slog.Logger) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		render(response, request, templates.VoiceCapture(templates.VoiceCaptureData{Shell: shell(request, page, csrfCookie), Enabled: service.Enabled(), MaxBytes: cfg.Voice.MaxBytes, MaxSeconds: int(cfg.Voice.MaxDuration.Seconds()), ExternalProvider: cfg.Voice.Transcriber == "openai" || cfg.Voice.Extractor == "openai", ProviderNotice: cfg.Voice.ExternalProviderNote}), http.StatusOK, logger)
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
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			writeVoiceError(response, request, http.StatusInternalServerError, "audio_unavailable", "Die Aufnahme konnte nicht verarbeitet werden.")
			return
		}
		session, _ := sessionFromContext(request.Context())
		ctx, cancel := context.WithTimeout(request.Context(), cfg.ProviderTimeout+5*time.Second)
		defer cancel()
		draft, err := service.Process(ctx, session.Actor, voice.Audio{Reader: file, Filename: "aufnahme" + mediaExtension(mediaType), ContentType: mediaType, Size: fileSize(file)}, voice.Metadata{RecordedAt: time.Now(), Duration: duration})
		if err != nil {
			status, code, message := mapVoiceError(err)
			logger.WarnContext(request.Context(), "voice upload rejected", slog.String("error_code", code))
			writeVoiceError(response, request, status, code, message)
			return
		}
		location := "/voice/drafts/" + draft.ID
		response.Header().Set("Location", location)
		writeJSON(response, http.StatusCreated, map[string]any{"draft_id": draft.ID, "status": draft.Status, "location": location})
	}
}

var errVoiceTooLarge = errors.New("voice upload too large")
var errVoiceEmpty = errors.New("voice upload empty")
var errVoiceType = errors.New("voice upload type")
var errVoiceDuration = errors.New("voice upload duration")

func receiveVoiceUpload(reader *multipart.Reader, cfg config.Voice) (*os.File, time.Duration, string, error) {
	if err := os.MkdirAll(cfg.TempDir, 0o700); err != nil {
		return nil, 0, "", err
	}
	// #nosec G302 -- this is a directory; 0700 is the required owner-only directory mode.
	if err := os.Chmod(cfg.TempDir, 0o700); err != nil {
		return nil, 0, "", err
	}
	var file *os.File
	var duration time.Duration
	var declared string
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
			return nil, 0, "", err
		}
		name := part.FormName()
		switch name {
		case "duration_ms":
			data, readErr := io.ReadAll(io.LimitReader(part, 32))
			if readErr != nil {
				cleanup()
				return nil, 0, "", readErr
			}
			millis, parseErr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
			if parseErr != nil || millis <= 0 {
				cleanup()
				return nil, 0, "", errVoiceDuration
			}
			duration = time.Duration(millis) * time.Millisecond
		case "audio":
			if file != nil {
				cleanup()
				return nil, 0, "", errVoiceType
			}
			declared = strings.ToLower(strings.TrimSpace(part.Header.Get("Content-Type")))
			file, err = os.CreateTemp(cfg.TempDir, "voice-*.audio")
			if err != nil {
				cleanup()
				return nil, 0, "", err
			}
			if err = os.Chmod(file.Name(), 0o600); err != nil {
				cleanup()
				return nil, 0, "", err
			}
			written, copyErr := io.Copy(file, io.LimitReader(part, int64(cfg.MaxBytes)+1))
			if copyErr != nil {
				cleanup()
				return nil, 0, "", copyErr
			}
			if written == 0 {
				cleanup()
				return nil, 0, "", errVoiceEmpty
			}
			if written > int64(cfg.MaxBytes) {
				cleanup()
				return nil, 0, "", errVoiceTooLarge
			}
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1024))
		}
		_ = part.Close()
	}
	if file == nil {
		cleanup()
		return nil, 0, "", errVoiceEmpty
	}
	if duration <= 0 || duration > cfg.MaxDuration {
		cleanup()
		return nil, 0, "", errVoiceDuration
	}
	mediaType, err := validateAudioFile(file, declared)
	if err != nil {
		cleanup()
		return nil, 0, "", err
	}
	return file, duration, mediaType, nil
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
	case len(buffer) >= 12 && string(buffer[4:8]) == "ftyp":
		detected = "audio/mp4"
	default:
		return "", errVoiceType
	}
	allowedDeclared := map[string]bool{"audio/webm": true, "video/webm": true, "audio/ogg": true, "application/ogg": true, "audio/wav": true, "audio/x-wav": true, "audio/mp4": true, "video/mp4": true, "audio/m4a": true}
	if !allowedDeclared[strings.Split(declared, ";")[0]] {
		return "", errVoiceType
	}
	return detected, nil
}
func mediaExtension(mediaType string) string {
	return map[string]string{"audio/webm": ".webm", "audio/ogg": ".ogg", "audio/wav": ".wav", "audio/mp4": ".mp4"}[mediaType]
}
func fileSize(file *os.File) int64 {
	info, err := file.Stat()
	if err != nil {
		return 0
	}
	return info.Size()
}
func writeVoiceUploadError(response http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, errVoiceTooLarge):
		writeVoiceError(response, request, http.StatusRequestEntityTooLarge, "audio_too_large", "Die Aufnahme überschreitet das erlaubte Größenlimit. Bitte kürzer aufnehmen oder manuell erfassen.")
	case errors.Is(err, errVoiceType):
		writeVoiceError(response, request, http.StatusUnsupportedMediaType, "unsupported_audio", "Dieses Audioformat wird nicht unterstützt. Bitte neu aufnehmen oder manuell erfassen.")
	case errors.Is(err, errVoiceEmpty):
		writeVoiceError(response, request, http.StatusUnprocessableEntity, "empty_audio", "Die Aufnahme ist leer. Bitte neu aufnehmen oder manuell erfassen.")
	case errors.Is(err, errVoiceDuration):
		writeVoiceError(response, request, http.StatusUnprocessableEntity, "invalid_duration", "Die Aufnahmedauer fehlt oder überschreitet das erlaubte Limit.")
	default:
		writeVoiceError(response, request, http.StatusInternalServerError, "upload_failed", "Die Aufnahme konnte nicht sicher zwischengespeichert werden.")
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
		duplicates, _ := service.Duplicates(request.Context(), session.Actor, draft)
		render(response, request, templates.VoiceReview(templates.VoiceReviewData{Shell: shell(request, page, csrfCookie), Draft: draft, Values: voiceValues(draft), Duplicates: duplicates}), http.StatusOK, logger)
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
