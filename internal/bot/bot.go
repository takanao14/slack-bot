package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"slack-bot/internal/config"
	"slack-bot/internal/handlers"
	"slack-bot/internal/image"
	"slack-bot/internal/metrics"
	ledclient "slack-bot/pkg/led/client"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// messageEventHandler handles Slack message events.
type messageEventHandler interface {
	HandleAppMention(ctx context.Context, ev *slackevents.AppMentionEvent)
	HandleMessage(ctx context.Context, ev *slackevents.MessageEvent)
	HandleEmojiChanged(ctx context.Context, ev *slackevents.EmojiChangedEvent)
}

// Bot represents the Slack bot application.
type Bot struct {
	api       *slack.Client
	client    *socketmode.Client
	config    *config.Config
	ledClient *ledclient.ImageClient
	text2img  *image.Text2Image

	messageHandler messageEventHandler
	runSocketMode  func(ctx context.Context) error
	ackRequest     func(req socketmode.Request)
	events         <-chan socketmode.Event

	// Prevents Shutdown from closing resources used by the event loop.
	eventLoop sync.WaitGroup
}

// newBot constructs a Bot from its dependencies.
func newBot(
	cfg *config.Config,
	api *slack.Client,
	client *socketmode.Client,
	ledClient *ledclient.ImageClient,
	text2img *image.Text2Image,
	messageHandler messageEventHandler,
) *Bot {
	b := &Bot{
		api:            api,
		client:         client,
		config:         cfg,
		ledClient:      ledClient,
		text2img:       text2img,
		messageHandler: messageHandler,
	}
	// Socket Mode hooks remain nil in tests without a client.
	if client != nil {
		b.runSocketMode = client.RunContext
		b.ackRequest = func(req socketmode.Request) {
			client.Ack(req)
		}
		b.events = client.Events
	}
	return b
}

const authTestMaxAttempts = 5

// Retry delays are variables so tests can shorten them.
var (
	authTestInitialBackoff = time.Second
	authTestMaxBackoff     = 15 * time.Second
)

// resolveBotIdentity retries auth.test to prevent startup without self-filtering.
func resolveBotIdentity(ctx context.Context, api *slack.Client, logger *slog.Logger) (handlers.BotIdentity, error) {
	backoff := authTestInitialBackoff
	var lastErr error

	for attempt := 1; attempt <= authTestMaxAttempts; attempt++ {
		resp, err := api.AuthTestContext(ctx)
		switch {
		case err != nil:
			lastErr = err
		case resp.UserID == "":
			return handlers.BotIdentity{}, errors.New("auth test returned an empty user id")
		default:
			logger.Info("Resolved bot identity",
				slog.String("bot_user_id", resp.UserID),
				slog.String("bot_id", resp.BotID),
			)
			return handlers.BotIdentity{UserID: resp.UserID, BotID: resp.BotID}, nil
		}

		logger.Warn("Failed to resolve bot identity via auth test",
			slog.Any("error", lastErr),
			slog.Int("attempt", attempt),
			slog.Int("max_attempts", authTestMaxAttempts),
		)

		if attempt == authTestMaxAttempts {
			break
		}

		select {
		case <-ctx.Done():
			return handlers.BotIdentity{}, ctx.Err()
		case <-time.After(backoff):
		}

		if backoff *= 2; backoff > authTestMaxBackoff {
			backoff = authTestMaxBackoff
		}
	}

	return handlers.BotIdentity{}, fmt.Errorf("auth test failed after %d attempts: %w", authTestMaxAttempts, lastErr)
}

// New initializes a Bot and its dependencies.
func New(ctx context.Context, cfg *config.Config) (*Bot, error) {
	api := slack.New(
		cfg.BotToken,
		slack.OptionAppLevelToken(cfg.AppToken),
		slack.OptionDebug(cfg.Debug),
	)

	client := socketmode.New(
		api,
		socketmode.OptionDebug(cfg.Debug),
	)

	identity, err := resolveBotIdentity(ctx, api, cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve bot identity: %w", err)
	}

	text2img, err := image.NewText2Image(cfg.FontPath, 32, cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Text2Image: %w", err)
	}

	ledClient, err := ledclient.NewImageClient(cfg.LEDAddr, cfg.LEDConnectTimeout, cfg.LEDOperationTimeout, cfg.Logger)
	if err != nil {
		if closeErr := text2img.Close(); closeErr != nil {
			cfg.Logger.Warn("Failed to close Text2Image during gRPC client initialization cleanup",
				slog.Any("error", closeErr),
			)
		}
		return nil, fmt.Errorf("failed to initialize gRPC client: %w", err)
	}
	cfg.Logger.Info("gRPC client initialized",
		slog.String("led_addr", cfg.LEDAddr),
		slog.Duration("led_connect_timeout", cfg.LEDConnectTimeout),
	)

	messageHandler := handlers.NewMessageHandler(
		api,
		cfg.Logger,
		identity,
		text2img,
		ledClient,
		cfg.LEDImageDurationSeconds,
		cfg.LEDScrollCycles,
		cfg.LEDMinDisplaySeconds,
		cfg.EmojiListCacheTTL,
		cfg.EmojiImageCacheTTL,
	)

	return newBot(cfg, api, client, ledClient, text2img, messageHandler), nil
}

// Run starts the bot's event processing loop.
func (b *Bot) Run(ctx context.Context) error {
	b.eventLoop.Add(1)
	go func() {
		defer b.eventLoop.Done()
		b.handleEvents(ctx)
	}()

	b.config.Logger.Info("Starting Slack bot with Socket Mode")
	if b.runSocketMode != nil {
		return b.runSocketMode(ctx)
	}
	return nil
}

// handleEvents processes incoming Slack events from the socketmode client.
func (b *Bot) handleEvents(ctx context.Context) {
	events := b.events
	if events == nil {
		b.config.Logger.Debug("Socketmode client events channel is nil, skipping event handler.")
		return
	}

	for {
		select {
		case <-ctx.Done():
			b.config.Logger.Info("Shutting down event handler (context canceled)")
			return
		case evt, ok := <-events:
			if !ok {
				b.config.Logger.Info("Shutting down event handler (channel closed)")
				return
			}
			b.handleEvent(ctx, evt)
		}
	}
}

// handleEvent dispatches a single socketmode event to the appropriate handler.
func (b *Bot) handleEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeConnecting:
		metrics.SetSocketConnected(false)
		b.config.Logger.Info("Connecting to Slack with Socket Mode...")

	case socketmode.EventTypeConnectionError:
		attrs := []any{}
		if ev, ok := evt.Data.(*slack.ConnectionErrorEvent); ok {
			attrs = append(attrs,
				slog.Any("error", ev.ErrorObj),
				slog.Int("attempt", ev.Attempt),
				slog.Duration("backoff", ev.Backoff),
			)
		}
		metrics.SetSocketConnected(false)
		b.config.Logger.Error("Connection failed, retrying", attrs...)

	case socketmode.EventTypeConnected:
		metrics.SetSocketConnected(true)
		b.config.Logger.Info("Connected to Slack with Socket Mode")

	case socketmode.EventTypeHello:
		b.config.Logger.Debug("Received Slack 'hello' event")

	case socketmode.EventTypeEventsAPI:
		b.handleEventsAPI(ctx, evt)

	// Socket Mode reconnects after incoming errors.
	case socketmode.EventTypeIncomingError:
		metrics.SetSocketConnected(false)
		b.config.Logger.Warn("Socket Mode connection error, reconnecting",
			slog.Any("error", eventError(evt.Data)),
		)

	// Slack redelivers events when acknowledgements fail.
	case socketmode.EventTypeErrorWriteFailed:
		attrs := []any{slog.Any("error", eventError(evt.Data))}
		if ev, ok := evt.Data.(*socketmode.ErrorWriteFailed); ok && ev.Response != nil {
			attrs = append(attrs, slog.String("envelope_id", ev.Response.EnvelopeID))
		}
		b.config.Logger.Error("Failed to send Socket Mode response, Slack will redeliver", attrs...)

	// Malformed messages do not close the connection.
	case socketmode.EventTypeErrorBadMessage:
		b.config.Logger.Warn("Received an unparsable Socket Mode message",
			slog.Any("error", eventError(evt.Data)),
		)

	// Invalid auth stops socketmode.
	case socketmode.EventTypeInvalidAuth:
		metrics.SetSocketConnected(false)
		b.config.Logger.Error("Slack rejected the app-level token, check SLACK_APP_TOKEN")

	default:
		b.config.Logger.Warn("Unexpected event type received",
			slog.String("type", string(evt.Type)),
			slog.Any("event", evt),
		)
	}
}

// eventError extracts errors from Socket Mode event payloads.
func eventError(data any) error {
	switch ev := data.(type) {
	case *slack.IncomingEventError:
		return ev.ErrorObj
	case *socketmode.ErrorWriteFailed:
		return ev.Cause
	case *socketmode.ErrorBadMessage:
		return ev.Cause
	case error:
		return ev
	default:
		return nil
	}
}

// handleEventsAPI processes Slack Events API events.
func (b *Bot) handleEventsAPI(ctx context.Context, evt socketmode.Event) {
	// Acknowledge before parsing to prevent repeated delivery of invalid events.
	if evt.Request != nil && b.ackRequest != nil {
		b.ackRequest(*evt.Request)
	}

	eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		b.config.Logger.Warn("Failed to parse EventsAPI event",
			slog.Any("data", evt.Data),
		)
		return
	}

	switch eventsAPIEvent.Type {
	case slackevents.CallbackEvent:
		innerEvent := eventsAPIEvent.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			b.messageHandler.HandleAppMention(ctx, ev)
		case *slackevents.MessageEvent:
			b.messageHandler.HandleMessage(ctx, ev)
		case *slackevents.EmojiChangedEvent:
			b.messageHandler.HandleEmojiChanged(ctx, ev)
		default:
			b.config.Logger.Debug("Unhandled inner event type for CallbackEvent",
				slog.String("type", innerEvent.Type),
				slog.Any("event", innerEvent.Data),
			)
		}
	default:
		b.config.Logger.Debug("Unhandled EventsAPI event type",
			slog.String("type", eventsAPIEvent.Type),
			slog.Any("event", eventsAPIEvent),
		)
	}
}

// Shutdown waits for handlers and closes the bot's resources.
func (b *Bot) Shutdown() error {
	b.config.Logger.Info("Shutting down bot")

	// A handler may still be rendering or sending an image.
	b.eventLoop.Wait()

	var errs error

	if b.text2img != nil {
		if err := b.text2img.Close(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to close Text2Image: %w", err))
			b.config.Logger.Error("Failed to close Text2Image", slog.Any("error", err))
		}
	}

	if b.ledClient != nil {
		if err := b.ledClient.Close(); err != nil {
			errs = errors.Join(errs, fmt.Errorf("failed to close gRPC client: %w", err))
			b.config.Logger.Error("Failed to close gRPC client", slog.Any("error", err))
		}
	}

	return errs
}
