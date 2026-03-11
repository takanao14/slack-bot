package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Config holds the application configuration.
type Config struct {
	BotToken             string
	AppToken             string
	FontPath             string
	GRPCAddr             string
	GRPCConnectTimeout   time.Duration
	GRPCOperationTimeout time.Duration
	EmojiListCacheTTL    time.Duration
	EmojiImageCacheTTL   time.Duration
	Debug                bool
	Logger               *slog.Logger
	ImageDuration        int32
}

// Load loads configuration from environment variables.
func Load() (*Config, error) {
	botToken, err := getRequiredEnv("SLACK_BOT_TOKEN")
	if err != nil {
		return nil, err
	}

	appToken, err := getRequiredEnv("SLACK_APP_TOKEN")
	if err != nil {
		return nil, err
	}

	fontPath, err := getRequiredEnv("SLACK_BOT_FONT")
	if err != nil {
		return nil, err
	}

	debug := os.Getenv("DEBUG") == "true"
	logLevel := slog.LevelInfo
	if debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))

	grpcAddr := getEnv("SLACK_BOT_GRPC_ADDR", "localhost:50051")
	imageDuration := getEnvAsInt32(logger, "SLACK_BOT_IMAGE_DURATION", 10)
	grpcConnectTimeout := getEnvAsDuration(logger, "SLACK_BOT_GRPC_CONNECT_TIMEOUT_SECONDS", 10*time.Second)
	grpcOperationTimeout := getEnvAsDuration(logger, "SLACK_BOT_GRPC_OPERATION_TIMEOUT_SECONDS", 30*time.Second)
	emojiListCacheTTL := getEnvAsDuration(logger, "SLACK_BOT_EMOJI_LIST_CACHE_TTL_SECONDS", 0)
	emojiImageCacheTTL := getEnvAsDuration(logger, "SLACK_BOT_EMOJI_IMAGE_CACHE_TTL_SECONDS", 0)

	return &Config{
		BotToken:             botToken,
		AppToken:             appToken,
		FontPath:             fontPath,
		GRPCAddr:             grpcAddr,
		GRPCConnectTimeout:   grpcConnectTimeout,
		GRPCOperationTimeout: grpcOperationTimeout,
		EmojiListCacheTTL:    emojiListCacheTTL,
		EmojiImageCacheTTL:   emojiImageCacheTTL,
		Debug:                debug,
		Logger:               logger,
		ImageDuration:        imageDuration,
	}, nil
}

// getRequiredEnv retrieves an environment variable or returns an error if it's missing.
func getRequiredEnv(key string) (string, error) {
	value := os.Getenv(key)
	if value == "" {
		return "", fmt.Errorf("%s environment variable is required", key)
	}
	return value, nil
}

// getEnv retrieves an environment variable or returns a default value.
func getEnv(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// getEnvAsInt32 retrieves an environment variable as an int32 or returns a default value.
func getEnvAsInt32(logger *slog.Logger, key string, defaultValue int32) int32 {
	strValue := getEnv(key, "")
	if strValue == "" {
		return defaultValue
	}
	intValue, err := strconv.Atoi(strValue)
	if err != nil {
		logger.Warn("Failed to parse environment variable as integer, using default", "key", key, "value", strValue, "default", defaultValue)
		return defaultValue
	}
	return int32(intValue)
}

// getEnvAsDuration retrieves an environment variable as a time.Duration in seconds or returns a default value.
func getEnvAsDuration(logger *slog.Logger, key string, defaultValue time.Duration) time.Duration {
	strValue := getEnv(key, "")
	if strValue == "" {
		return defaultValue
	}
	intValue, err := strconv.Atoi(strValue)
	if err != nil {
		logger.Warn("Failed to parse environment variable as duration, using default", "key", key, "value", strValue, "default", defaultValue)
		return defaultValue
	}
	return time.Duration(intValue) * time.Second
}
