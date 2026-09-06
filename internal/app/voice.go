package app

import (
	"errors"
	"time"

	"example.invalid/hackplan/internal/adapters/postgres"
	"example.invalid/hackplan/internal/config"
	"example.invalid/hackplan/internal/voice"
	"github.com/jackc/pgx/v5/pgxpool"
)

func VoiceService(cfg config.Config, pool *pgxpool.Pool, observers ...voice.Observer) (*voice.Service, error) {
	var transcriber voice.Transcriber
	switch cfg.Voice.Transcriber {
	case "disabled":
		transcriber = voice.DisabledTranscriber{}
	case "fake":
		transcriber = voice.FakeTranscriber{Text: cfg.Voice.FakeTranscript}
	case "openai":
		provider, err := voice.NewOpenAITranscriber(cfg.Voice.OpenAIAPIKey, cfg.Voice.OpenAIModel, cfg.Voice.ProviderTimeout, int64(cfg.Voice.MaxResponseBytes))
		if err != nil {
			return nil, err
		}
		transcriber = provider
	case "whisper-local":
		provider, err := voice.NewWhisperCPPTranscriber(cfg.Voice.WhisperModel, cfg.Voice.ProviderTimeout, int64(cfg.Voice.MaxResponseBytes))
		if err != nil {
			return nil, err
		}
		transcriber = provider
	case "whisper-tailscale":
		provider, err := voice.NewWhisperCPPTailscaleTranscriber(cfg.Voice.WhisperModel, cfg.Voice.WhisperURL, cfg.Voice.ProviderTimeout, int64(cfg.Voice.MaxResponseBytes))
		if err != nil {
			return nil, err
		}
		transcriber = provider
	default:
		return nil, errors.New("app: unsupported voice transcriber")
	}
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	var extractor voice.Extractor = voice.RuleExtractor{}
	if cfg.Voice.Extractor == "openai" {
		provider, providerErr := voice.NewOpenAIStructuredProvider(cfg.Voice.OpenAIAPIKey, cfg.Voice.OpenAIExtractionModel, cfg.Voice.ProviderTimeout, int64(cfg.Voice.MaxResponseBytes))
		if providerErr != nil {
			return nil, providerErr
		}
		extractor = voice.HybridExtractor{Rules: voice.RuleExtractor{}, Provider: provider}
	}
	options := make([]voice.Option, 0, len(observers))
	for _, observer := range observers {
		options = append(options, voice.WithObserver(observer))
	}
	return voice.New(postgres.NewVoiceStore(pool), transcriber, extractor, voice.Config{Enabled: cfg.Voice.Enabled, Retention: cfg.Voice.DraftRetention, RecordingRetention: cfg.Voice.RecordingRetention, RateLimitPerMinute: cfg.Voice.RateLimitPerMinute, ConcurrentPerUser: cfg.Voice.ConcurrentPerUser, Timezone: location}, time.Now, options...)
}
