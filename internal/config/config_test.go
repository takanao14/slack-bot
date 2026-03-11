package config

import (
	"os"
	"testing"
	"time"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	t.Setenv("SLACK_APP_TOKEN", "xapp-test")
	t.Setenv("SLACK_BOT_FONT", "/tmp/test.ttf")
}

func TestLoadReadsEmojiCacheTTLsFromEnv(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SLACK_BOT_EMOJI_LIST_CACHE_TTL_SECONDS", "120")
	t.Setenv("SLACK_BOT_EMOJI_IMAGE_CACHE_TTL_SECONDS", "300")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if cfg.EmojiListCacheTTL != 120*time.Second {
		t.Fatalf("expected emoji list cache TTL 120s, got %v", cfg.EmojiListCacheTTL)
	}
	if cfg.EmojiImageCacheTTL != 300*time.Second {
		t.Fatalf("expected emoji image cache TTL 300s, got %v", cfg.EmojiImageCacheTTL)
	}
}

func TestLoadUsesZeroTTLsWhenEmojiCacheTTLsAreUnset(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if cfg.EmojiListCacheTTL != 0 {
		t.Fatalf("expected unset emoji list cache TTL to remain zero, got %v", cfg.EmojiListCacheTTL)
	}
	if cfg.EmojiImageCacheTTL != 0 {
		t.Fatalf("expected unset emoji image cache TTL to remain zero, got %v", cfg.EmojiImageCacheTTL)
	}
}

func TestLoadFallsBackWhenEmojiCacheTTLValuesAreInvalid(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SLACK_BOT_EMOJI_LIST_CACHE_TTL_SECONDS", "bad")
	t.Setenv("SLACK_BOT_EMOJI_IMAGE_CACHE_TTL_SECONDS", "oops")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if cfg.EmojiListCacheTTL != 0 {
		t.Fatalf("expected invalid emoji list cache TTL to fall back to zero, got %v", cfg.EmojiListCacheTTL)
	}
	if cfg.EmojiImageCacheTTL != 0 {
		t.Fatalf("expected invalid emoji image cache TTL to fall back to zero, got %v", cfg.EmojiImageCacheTTL)
	}
}

func TestLoadReturnsErrorWhenRequiredEnvVarsAreMissing(t *testing.T) {
	tests := []struct {
		name    string
		setup   func()
		wantErr string
	}{
		{
			name: "Missing Bot Token",
			setup: func() {
				t.Setenv("SLACK_APP_TOKEN", "app-token")
				t.Setenv("SLACK_BOT_FONT", "font.ttf")
			},
			wantErr: "SLACK_BOT_TOKEN environment variable is required",
		},
		{
			name: "Missing App Token",
			setup: func() {
				t.Setenv("SLACK_BOT_TOKEN", "bot-token")
				t.Setenv("SLACK_BOT_FONT", "font.ttf")
			},
			wantErr: "SLACK_APP_TOKEN environment variable is required",
		},
		{
			name: "Missing Font Path",
			setup: func() {
				t.Setenv("SLACK_BOT_TOKEN", "bot-token")
				t.Setenv("SLACK_APP_TOKEN", "app-token")
			},
			wantErr: "SLACK_BOT_FONT environment variable is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear env before each subtest to ensure isolation handled by t.Setenv in setup
			os.Clearenv()
			tt.setup()

			_, err := Load()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != tt.wantErr {
				t.Fatalf("expected error %q, got %q", tt.wantErr, err.Error())
			}
		})
	}
}

func TestLoadSetsDefaultValues(t *testing.T) {
	setRequiredEnv(t)
	// Ensure these are unset
	if err := os.Unsetenv("SLACK_BOT_GRPC_ADDR"); err != nil {
		t.Fatalf("failed to unset env: %v", err)
	}
	if err := os.Unsetenv("DEBUG"); err != nil {
		t.Fatalf("failed to unset env: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if cfg.GRPCAddr != "localhost:50051" {
		t.Errorf("expected default GRPCAddr 'localhost:50051', got %q", cfg.GRPCAddr)
	}
	if cfg.Debug != false {
		t.Errorf("expected default Debug to be false, got %v", cfg.Debug)
	}
	if cfg.ImageDuration != 10 {
		t.Errorf("expected default ImageDuration 10, got %d", cfg.ImageDuration)
	}
}

func TestLoadFallsBackToDefaultImageDurationOnInvalidInput(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("SLACK_BOT_IMAGE_DURATION", "invalid-number")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if cfg.ImageDuration != 10 { // Default is 10
		t.Errorf("expected default ImageDuration 10 when env var is invalid, got %d", cfg.ImageDuration)
	}
}
