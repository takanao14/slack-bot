# Slack Bot (Socket Mode)

This is a Slack bot that uses Socket Mode, built with the slack-go library.

## Features

- Real-time communication using Socket Mode
- Handles App Mention events
- Processes message events (renders text and emojis to PPM for LED display)
- Emoji caching (separate list/image TTLs, updates on `emoji_changed` events)
- gRPC integration for sending images to an LED display service (with retry and timeout controls)
- Robust configuration loading and gRPC connection management
- Structured logging with log/slog
- Graceful shutdown

## Project Structure

```
slack-bot/
├── cmd/slack-bot/     # Entrypoint (main.go)
├── internal/
│   ├── bot/           # Core bot logic (Socket Mode, event loop)
│   ├── config/        # Configuration management (loading from environment variables)
│   ├── handlers/      # Event handlers (Message, AppMention, EmojiChanged)
│   └── image/         # Image processing (Text to PPM rendering, emoji resolution)
├── pkg/
│   └── grpc/client/   # gRPC client implementation
├── go.mod
└── README.md
```

## Setup

### 1. Required Environment Variables

Set the following environment variables:

```bash
export SLACK_BOT_TOKEN="xoxb-your-bot-token"
export SLACK_APP_TOKEN="xapp-your-app-token"
export SLACK_BOT_FONT="/path/to/font.ttf"
export SLACK_BOT_GRPC_ADDR="localhost:50051"                    # Optional (default: localhost:50051)
export SLACK_BOT_IMAGE_DURATION="10"                            # Optional: Display duration in seconds (default: 10)
export SLACK_BOT_GRPC_CONNECT_TIMEOUT_SECONDS="10"              # Optional: gRPC connection timeout (default: 10)
export SLACK_BOT_GRPC_OPERATION_TIMEOUT_SECONDS="30"            # Optional: gRPC operation timeout (default: 30)
export SLACK_BOT_EMOJI_LIST_CACHE_TTL_SECONDS="86400"           # Optional: Emoji list cache TTL in seconds (default: handler fallback 24h)
export SLACK_BOT_EMOJI_IMAGE_CACHE_TTL_SECONDS="86400"          # Optional: Emoji image cache TTL in seconds (default: handler fallback 24h)
export DEBUG="true"                                             # Optional: Enable debug logging
```

### 2. Slack App Configuration

1. Create an app on the [Slack API](https://api.slack.com/apps) website.
2. Enable **Socket Mode**.
3. Under **Event Subscriptions**, subscribe to the following events:
   - `app_mention`
   - `message.channels`
   - `emoji_changed`
4. Under **OAuth & Permissions**, add the required scopes:
   - `app_mentions:read`
   - `chat:write`
   - `channels:history`
   - `emoji:read` (required for custom emojis)
5. Obtain the Bot User OAuth Token and the App-Level Token.

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
# Build (creates bin/slack-bot)
make build

# Run directly
./bin/slack-bot
```

### Other Make Targets

```bash
make help             # Show available targets
make test             # Run tests
make clean            # Remove build artifacts
make fmt              # Format the code
make lint             # Run the linter
make service          # Generate the systemd service file
make install-service  # Install the service file (Linux only)
make enable-service   # Enable and start the service (Linux only)
```

## Running as a System Service (Linux)

You can run this bot as a persistent user service on a Linux system.

### 1. Install and Start the Service

```bash
# Generate and install the service file
make install-service

# Enable and start the service
make enable-service
```

### 2. Keep the Service Running After Logout

By default, user services stop when the user logs out. To keep the service running even when you are not logged in, run the following command:

```bash
loginctl enable-linger $USER
```

Alternatively, you can use the following Make target:

```bash
make enable-linger
```

### 3. View Logs

```bash
journalctl --user -u slack-bot -f
```

## Usage

### Responding to App Mentions

Mention the bot in Slack (e.g., `@your-bot Hello`), and it will respond.

### Displaying Messages on LED

When a message is posted in a channel the bot is in, its text (and emojis) will be converted to a PPM image and sent to the LED display service via gRPC.
It supports both custom Slack emojis and standard Unicode emojis.

## Logging

The bot uses structured logging with log/slog:

- **INFO**: General operational logs
- **DEBUG**: Debug information (only when `DEBUG=true`)
- **ERROR**: Error information

Logs are output in JSON format.

## Development

### Adding a New Handler

To add a new event handler:

1. Create a new file in `internal/handlers/`.
2. Implement the handler struct and methods.
3. Initialize and register the handler in `internal/bot/bot.go`.

### Customization

- Message processing and display: `internal/handlers/message.go`
- Image rendering (font, size, etc.): `internal/image/text2image.go`
- gRPC client settings: `pkg/grpc/client/image_client.go`

## Troubleshooting

### Connection Issues

- Verify that `SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN` are set correctly.
- Ensure that Socket Mode is enabled for your Slack app.
- Check your network connection.

### Not Receiving Events

- Confirm that you have subscribed to the necessary events under **Event Subscriptions** in the Slack API settings.
- Ensure that the bot has the correct scopes.

## License

MIT
