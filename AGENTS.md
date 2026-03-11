# AGENTS.md

## General Principles
- **Comments**: All comments MUST be written in English.
- **DRY (Don't Repeat Yourself)**: Avoid duplicating logic. Refactor shared code into internal packages or helper functions.
- **KISS (Keep It Simple, Stupid)**: Favor readability and simplicity over clever or overly complex code.
- **YAGNI (You Ain't Gonna Need It)**: Do not add functionality until it is actually needed.

## Go Coding Standards
- **Error Handling**:
    - Do not use `panic` for recoverable errors.
    - Return errors from functions and handle them at the appropriate level (usually in `main.go` or top-level handlers).
    - Wrap errors with `fmt.Errorf("...: %w", err)` to provide context while preserving the original error.
- **Context Usage**:
    - Always pass `context.Context` to functions that perform I/O or long-running operations (Slack API calls, gRPC requests, etc.).
    - Respect context cancellation.
- **Logging**:
    - Use `log/slog` for structured logging.
    - Include relevant context in log attributes (e.g., `channel`, `user`, `error`).
- **Resource Management**:
    - Ensure resources like file handles, network connections, and font faces are properly closed using `defer` or explicit `Close()` methods.
- **Project Structure**:
    - `cmd/`: Application entry points.
    - `internal/`: Private library code for the application.
    - `pkg/`: Public library code that can be used by other projects (e.g., gRPC client).

## Slack Bot Specifics
- **Socket Mode**: The bot uses Slack Socket Mode. Do not add HTTP server listeners unless explicitly required.
- **Event Handling**: Handle events asynchronously where appropriate, but ensure shared resources are thread-safe.
- **Image Rendering**: Message text and emojis are rendered into images (PPM format) before being sent to the LED display.

## gRPC Integration
- **Client**: Use the `pkg/grpc/client` for communicating with the LED image service.
- **Timeouts**: Always respect timeouts defined in the configuration.
