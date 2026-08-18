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
│   └── image/         # Text and emoji rendering
├── pkg/
│   └── grpc/client/   # LED service client
├── go.mod
└── README.md
```

## Setup

### 1. Environment Variables

Set the required variables and any optional overrides:

```bash
export SLACK_BOT_TOKEN="xoxb-your-bot-token"
export SLACK_APP_TOKEN="xapp-your-app-token"
export SLACK_BOT_FONT="/path/to/font.ttf"
export SLACK_BOT_GRPC_ADDR="localhost:50051"                    # Optional; default: localhost:50051
export SLACK_BOT_IMAGE_DURATION="10"                            # Optional; seconds, default: 10
export SLACK_BOT_GRPC_CONNECT_TIMEOUT_SECONDS="10"              # Optional; seconds, default: 10
export SLACK_BOT_GRPC_OPERATION_TIMEOUT_SECONDS="30"            # Optional; seconds, default: 30
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

## Running as a Linux User Service

Run the bot as a persistent Linux user service.

### 1. Install and Start

```bash
make enable-service
```

### 2. Keep the Service Running After Logout

Enable lingering to keep the service running after logout:

```bash
loginctl enable-linger $USER
```

Or use:

```bash
make enable-linger
```

### 3. View Logs

```bash
journalctl --user -u slack-bot -f
```

### Startup Ordering

A user service may start before DNS is ready. Startup is protected by:

- `ExecStartPre`, which waits up to 120 seconds for `slack.com` to resolve.
- `auth.test` retries, which prevent startup without the identity needed to filter
  the bot's own posts.

`Restart=always` retries failed starts. The user service cannot use the system
manager's `network-online.target`; its directives remain commented in the template
for system-service installations.

## Usage

### Responding to App Mentions

Mention the bot (for example, `@your-bot Hello`) to receive a response.

### Displaying Messages on LED

Channel messages are rendered as PPM images and sent to the LED service over gRPC.
Both custom Slack emojis and Unicode emojis are supported.

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
- gRPC client settings: `pkg/grpc/client/image_client.go`

## Troubleshooting

### Connection Issues

- Verify `SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN`.
- Confirm Socket Mode is enabled.
- Check network connectivity.

### Not Receiving Events

- Confirm the required **Event Subscriptions** and OAuth scopes.

## License

MIT
