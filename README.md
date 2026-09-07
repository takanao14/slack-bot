# Slack Bot (Socket Mode)

A Slack Socket Mode bot built with [slack-go](https://github.com/slack-go/slack).

## Features

- App mention and message handling over Socket Mode
- Text and emoji rendering to PPM for an LED display
- Separate emoji list/image caches refreshed by `emoji_changed` events
- gRPC image delivery with retries and timeouts
- Environment-based configuration
- Structured logging with `log/slog`
- Graceful shutdown

## Project Structure

```
slack-bot/
├── cmd/slack-bot/     # Entrypoint (main.go)
├── internal/
│   ├── bot/           # Socket Mode event loop
│   ├── config/        # Environment configuration
│   ├── handlers/      # Slack event handlers
│   ├── health/        # Liveness probe endpoint
│   └── image/         # Text and emoji rendering
├── pkg/
│   └── led/client/    # LED service client
├── go.mod
└── README.md
```

## Setup

### 1. Environment Variables

Set the required variables and any optional overrides:

```bash
export SLACK_BOT_TOKEN="xoxb-your-bot-token"
export SLACK_APP_TOKEN="xapp-your-app-token"
export SLACK_BOT_FONT_PATH="/path/to/font.ttf"
export SLACK_BOT_HEALTH_ADDR=":8080"                            # Optional; default: :8080, empty disables
export SLACK_BOT_LED_ADDR="localhost:50051"                     # Optional; default: localhost:50051
export SLACK_BOT_LED_IMAGE_DURATION_SECONDS="10"                # Optional; seconds, default: 10
export SLACK_BOT_LED_CONNECT_TIMEOUT_SECONDS="10"               # Optional; seconds, default: 10
export SLACK_BOT_LED_OPERATION_TIMEOUT_SECONDS="30"             # Optional; seconds, default: 30
export SLACK_BOT_EMOJI_LIST_CACHE_TTL_SECONDS="86400"           # Optional; seconds, default: 24h
export SLACK_BOT_EMOJI_IMAGE_CACHE_TTL_SECONDS="86400"          # Optional; seconds, default: 24h
export DEBUG="true"                                             # Optional; enables debug logs
```

### 2. Slack App Configuration

1. Create an app at [Slack API](https://api.slack.com/apps).
2. Enable **Socket Mode**.
3. Under **Event Subscriptions**, subscribe to:
   - `app_mention`
   - `message.channels`
   - `emoji_changed`
4. Under **OAuth & Permissions**, add:
   - `app_mentions:read`
   - `chat:write`
   - `channels:history`
   - `emoji:read` for custom emojis
5. Copy the Bot User OAuth Token and App-Level Token.

## Build and Run

### Running Locally

```bash
# Install dependencies
go mod download

# Run
make run
```

### Building

```bash
# Build bin/slack-bot
make build

# Run directly
./bin/slack-bot
```

### Container Image

```bash
docker build -t slack-bot .
docker run --rm -p 8080:8080 -e SLACK_BOT_TOKEN=xoxb-... -e SLACK_APP_TOKEN=xapp-... slack-bot
```

The image bundles BIZ UDPGothic, the proportional cut of BIZ UDGothic (Morisawa
Inc., SIL Open Font License 1.1), under `/usr/share/fonts/BIZUDGothic/` together
with its `OFL.txt`, and points `SLACK_BOT_FONT_PATH` at it, so only the two Slack
tokens are required. The font archive and its license are both pinned by SHA-256.

### Other Make Targets

```bash
make help    # Show available targets
make test    # Run tests
make image   # Build the container image
make clean   # Remove build artifacts
make fmt     # Format the code
make lint    # Run the linter
```

## Usage

### Responding to App Mentions

Mention the bot (for example, `@your-bot Hello`) to receive a response.

### Displaying Messages on LED

Channel messages are rendered as PPM images and sent to the LED service over gRPC.
Both custom Slack emojis and Unicode emojis are supported.

## Health Endpoint

`GET /healthz` on `SLACK_BOT_HEALTH_ADDR` returns 200 while the process is
running, for container liveness probes. It starts before the Slack connection so
that a probe does not fail during `auth.test` retries. Set the variable to an
empty string to run without a listener.

## Logging

The bot emits JSON logs with `log/slog`:

- **INFO**: Operations
- **DEBUG**: Debug details when `DEBUG=true`
- **ERROR**: Errors

## Development

### Adding a New Handler

To add an event handler:

1. Create a new file in `internal/handlers/`.
2. Implement the handler struct and methods.
3. Initialize and register the handler in `internal/bot/bot.go`.

### Customization

- Message processing and display: `internal/handlers/message.go`
- Image rendering (font and size): `internal/image/text2image.go`
- gRPC client settings: `pkg/led/client/image_client.go`

## Troubleshooting

### Connection Issues

- Verify `SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN`.
- Confirm Socket Mode is enabled.
- Check network connectivity.

### Not Receiving Events

- Confirm the required **Event Subscriptions** and OAuth scopes.

## License

MIT
