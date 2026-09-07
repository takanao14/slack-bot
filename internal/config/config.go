package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Defaults live here so that a single place defines them; the consumers use
// whatever Load hands them.
const (
	defaultEmojiListCacheTTL  = 24 * time.Hour
	defaultEmojiImageCacheTTL = 24 * time.Hour
)

// Config holds the application configuration.
type Config struct {
	BotToken                string
	AppToken                string
	FontPath                string
	HealthAddr              string
	LEDAddr                 string
	LEDConnectTimeout       time.Duration
	LEDOperationTimeout     time.Duration
	EmojiListCacheTTL       time.Duration
	EmojiImageCacheTTL      time.Duration
	Debug                   bool
	Logger                  *slog.Logger
	LEDImageDurationSeconds int32
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

	fontPath, err := getRequiredEnv("SLACK_BOT_FONT_PATH")
	if err != nil {
		return nil, err
	}

	debug := os.Getenv("DEBUG") == "true"
	logLevel := slog.LevelInfo
	if debug {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))

	// An empty address disables the listener, which suits deployments that do
	// not probe the bot.
	healthAddr := getEnv("SLACK_BOT_HEALTH_ADDR", ":8080")
	ledAddr := getEnv("SLACK_BOT_LED_ADDR", "localhost:50051")
	ledImageDurationSeconds := getEnvAsInt32(logger, "SLACK_BOT_LED_IMAGE_DURATION_SECONDS", 10)
	ledConnectTimeout := getEnvAsDuration(logger, "SLACK_BOT_LED_CONNECT_TIMEOUT_SECONDS", 10*time.Second)
	ledOperationTimeout := getEnvAsDuration(logger, "SLACK_BOT_LED_OPERATION_TIMEOUT_SECONDS", 30*time.Second)
	emojiListCacheTTL := getEnvAsDuration(logger, "SLACK_BOT_EMOJI_LIST_CACHE_TTL_SECONDS", defaultEmojiListCacheTTL)
	emojiImageCacheTTL := getEnvAsDuration(logger, "SLACK_BOT_EMOJI_IMAGE_CACHE_TTL_SECONDS", defaultEmojiImageCacheTTL)

	return &Config{
		BotToken:                botToken,
		AppToken:                appToken,
		FontPath:                fontPath,
		HealthAddr:              healthAddr,
		LEDAddr:                 ledAddr,
		LEDConnectTimeout:       ledConnectTimeout,
		LEDOperationTimeout:     ledOperationTimeout,
		EmojiListCacheTTL:       emojiListCacheTTL,
		EmojiImageCacheTTL:      emojiImageCacheTTL,
		Debug:                   debug,
		Logger:                  logger,
		LEDImageDurationSeconds: ledImageDurationSeconds,
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

// getEnvAsInt32 retrieves an environment variable as a positive int32 or returns
// a default value. Parsing at 32 bits rejects the overflow a plain Atoi would
// have wrapped silently.
func getEnvAsInt32(logger *slog.Logger, key string, defaultValue int32) int32 {
	strValue := getEnv(key, "")
	if strValue == "" {
		return defaultValue
	}
	intValue, err := strconv.ParseInt(strValue, 10, 32)
	if err != nil {
		logger.Warn("Failed to parse environment variable as integer, using default", "key", key, "value", strValue, "default", defaultValue)
		return defaultValue
	}
	if intValue <= 0 {
		logger.Warn("Environment variable must be positive, using default", "key", key, "value", strValue, "default", defaultValue)
		return defaultValue
	}
	return int32(intValue)
}

// getEnvAsDuration retrieves an environment variable as a positive number of
// seconds or returns a default value.
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
	if intValue <= 0 {
		logger.Warn("Environment variable must be positive, using default", "key", key, "value", strValue, "default", defaultValue)
		return defaultValue
	}
	return time.Duration(intValue) * time.Second
}
