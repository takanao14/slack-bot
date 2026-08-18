package bot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"slack-bot/internal/config"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type stubMessageHandler struct {
	appMentionCalls   int
	messageCalls      int
	emojiChangedCalls int
}

func (s *stubMessageHandler) HandleAppMention(_ context.Context, _ *slackevents.AppMentionEvent) {
	s.appMentionCalls++
}

func (s *stubMessageHandler) HandleMessage(_ context.Context, _ *slackevents.MessageEvent) {
	s.messageCalls++
}

func (s *stubMessageHandler) HandleEmojiChanged(_ context.Context, _ *slackevents.EmojiChangedEvent) {
	s.emojiChangedCalls++
}

type recordedEntry struct {
	message string
	level   slog.Level
	keys    map[string]struct{}
}

type recordingHandler struct {
	mu      sync.Mutex
	entries []recordedEntry
}

func (h *recordingHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	keys := map[string]struct{}{}
	r.Attrs(func(a slog.Attr) bool {
		keys[a.Key] = struct{}{}
		return true
	})

	h.mu.Lock()
	h.entries = append(h.entries, recordedEntry{message: r.Message, level: r.Level, keys: keys})
	h.mu.Unlock()
	return nil
}

func (h *recordingHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *recordingHandler) hasEntryWithKey(message string, level slog.Level, key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.entries {
		if e.message == message && e.level == level {
			if _, ok := e.keys[key]; ok {
				return true
			}
		}
	}
	return false
}

func (h *recordingHandler) find(message string) (recordedEntry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.entries {
		if e.message == message {
			return e, true
		}
	}
	return recordedEntry{}, false
}

func newTestBot(t *testing.T) (*Bot, *recordingHandler, *stubMessageHandler, *int) {
	t.Helper()

	handler := &recordingHandler{}
	logger := slog.New(handler)
	cfg := &config.Config{Logger: logger}
	msgHandler := &stubMessageHandler{}
	ackCount := 0

	b := newBot(cfg, nil, nil, nil, nil, msgHandler)
	b.ackRequest = func(_ socketmode.Request) {
		ackCount++
	}

	return b, handler, msgHandler, &ackCount
}

func TestHandleEventUnknownTypeLogsTypeKey(t *testing.T) {
	b, logs, _, _ := newTestBot(t)

	b.handleEvent(context.Background(), socketmode.Event{Type: "unexpected_type"})

	if !logs.hasEntryWithKey("Unexpected event type received", slog.LevelWarn, "type") {
		t.Fatal("expected warn log with type key")
	}
}

func TestHandleEventEventsAPIRoutesToMessageHandler(t *testing.T) {
	b, _, msgHandler, ackCount := newTestBot(t)

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			Type:       slackevents.CallbackEvent,
			InnerEvent: slackevents.EventsAPIInnerEvent{Data: &slackevents.MessageEvent{}},
		},
		Request: &socketmode.Request{},
	}

	b.handleEvent(context.Background(), evt)

	if msgHandler.messageCalls != 1 {
		t.Fatalf("expected MessageEvent handler to be called once, got %d", msgHandler.messageCalls)
	}
	if *ackCount != 1 {
		t.Fatalf("expected ack to be called once, got %d", *ackCount)
	}
}

func TestHandleEventsAPITypeMismatchLogsDataKeyAndSkipsAck(t *testing.T) {
	b, logs, _, ackCount := newTestBot(t)

	b.handleEventsAPI(context.Background(), socketmode.Event{Type: socketmode.EventTypeEventsAPI, Data: "invalid"})

	if *ackCount != 0 {
		t.Fatalf("expected ack not to be called, got %d", *ackCount)
	}
	if !logs.hasEntryWithKey("Failed to parse EventsAPI event", slog.LevelWarn, "data") {
		t.Fatal("expected warn log with data key")
	}
}

func TestHandleEventsAPICallbackRoutesAllSupportedEvents(t *testing.T) {
	tests := []struct {
		name         string
		innerData    any
		wantApp      int
		wantMessage  int
		wantEmoji    int
		wantAckCalls int
	}{
		{name: "app mention", innerData: &slackevents.AppMentionEvent{}, wantApp: 1, wantAckCalls: 1},
		{name: "message", innerData: &slackevents.MessageEvent{}, wantMessage: 1, wantAckCalls: 1},
		{name: "emoji changed", innerData: &slackevents.EmojiChangedEvent{}, wantEmoji: 1, wantAckCalls: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, _, msgHandler, ackCount := newTestBot(t)

			b.handleEventsAPI(context.Background(), socketmode.Event{
				Type: socketmode.EventTypeEventsAPI,
				Data: slackevents.EventsAPIEvent{
					Type:       slackevents.CallbackEvent,
					InnerEvent: slackevents.EventsAPIInnerEvent{Data: tt.innerData},
				},
				Request: &socketmode.Request{},
			})

			if msgHandler.appMentionCalls != tt.wantApp {
				t.Fatalf("expected app mention calls %d, got %d", tt.wantApp, msgHandler.appMentionCalls)
			}
			if msgHandler.messageCalls != tt.wantMessage {
				t.Fatalf("expected message calls %d, got %d", tt.wantMessage, msgHandler.messageCalls)
			}
			if msgHandler.emojiChangedCalls != tt.wantEmoji {
				t.Fatalf("expected emoji changed calls %d, got %d", tt.wantEmoji, msgHandler.emojiChangedCalls)
			}
			if *ackCount != tt.wantAckCalls {
				t.Fatalf("expected ack calls %d, got %d", tt.wantAckCalls, *ackCount)
			}
		})
	}
}

func TestHandleEventsAPIUnhandledTypeAcksAndLogsTypeKey(t *testing.T) {
	b, logs, msgHandler, ackCount := newTestBot(t)

	b.handleEventsAPI(context.Background(), socketmode.Event{
		Type:    socketmode.EventTypeEventsAPI,
		Data:    slackevents.EventsAPIEvent{Type: "url_verification"},
		Request: &socketmode.Request{},
	})

	if *ackCount != 1 {
		t.Fatalf("expected ack to be called once, got %d", *ackCount)
	}
	if msgHandler.appMentionCalls+msgHandler.messageCalls+msgHandler.emojiChangedCalls != 0 {
		t.Fatal("expected no message handler delegation for unhandled event type")
	}
	if !logs.hasEntryWithKey("Unhandled EventsAPI event type", slog.LevelDebug, "type") {
		t.Fatal("expected debug log with type key")
	}
}

// Socket Mode errors should use specific logs without dumping events.
func TestHandleEventLogsSocketModeErrorsWithoutDumpingTheEvent(t *testing.T) {
	cause := errors.New("websocket: close 1000 (normal)")

	tests := []struct {
		name      string
		event     socketmode.Event
		wantMsg   string
		wantLevel slog.Level
		wantErr   bool
	}{
		{
			name:      "incoming error",
			event:     socketmode.Event{Type: socketmode.EventTypeIncomingError, Data: &slack.IncomingEventError{ErrorObj: cause}},
			wantMsg:   "Socket Mode connection error, reconnecting",
			wantLevel: slog.LevelWarn,
			wantErr:   true,
		},
		{
			name: "write failed",
			event: socketmode.Event{
				Type: socketmode.EventTypeErrorWriteFailed,
				Data: &socketmode.ErrorWriteFailed{Cause: cause, Response: &socketmode.Response{EnvelopeID: "d88ad92d"}},
			},
			wantMsg:   "Failed to send Socket Mode response, Slack will redeliver",
			wantLevel: slog.LevelError,
			wantErr:   true,
		},
		{
			name:      "bad message",
			event:     socketmode.Event{Type: socketmode.EventTypeErrorBadMessage, Data: &socketmode.ErrorBadMessage{Cause: cause}},
			wantMsg:   "Received an unparsable Socket Mode message",
			wantLevel: slog.LevelWarn,
			wantErr:   true,
		},
		{
			name:      "invalid auth",
			event:     socketmode.Event{Type: socketmode.EventTypeInvalidAuth, Data: &slack.InvalidAuthEvent{}},
			wantMsg:   "Slack rejected the app-level token, check SLACK_APP_TOKEN",
			wantLevel: slog.LevelError,
		},
		{
			name: "connection error",
			event: socketmode.Event{
				Type: socketmode.EventTypeConnectionError,
				Data: &slack.ConnectionErrorEvent{Attempt: 2, Backoff: time.Second, ErrorObj: cause},
			},
			wantMsg:   "Connection failed, retrying",
			wantLevel: slog.LevelError,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, logs, _, _ := newTestBot(t)

			b.handleEvent(context.Background(), tt.event)

			entry, ok := logs.find(tt.wantMsg)
			if !ok {
				t.Fatalf("expected a log entry %q", tt.wantMsg)
			}
			if entry.level != tt.wantLevel {
				t.Fatalf("expected level %v, got %v", tt.wantLevel, entry.level)
			}
			if _, dumped := entry.keys["event"]; dumped {
				t.Fatal("expected no full event dump")
			}
			if _, hasErr := entry.keys["error"]; hasErr != tt.wantErr {
				t.Fatalf("expected error key presence %v, got %v", tt.wantErr, hasErr)
			}
			if _, ok := logs.find("Unexpected event type received"); ok {
				t.Fatal("expected the event not to fall through to the default branch")
			}
		})
	}
}

func TestHandleEventConnectionErrorWithoutPayloadStillLogs(t *testing.T) {
	b, logs, _, _ := newTestBot(t)

	b.handleEvent(context.Background(), socketmode.Event{Type: socketmode.EventTypeConnectionError})

	entry, ok := logs.find("Connection failed, retrying")
	if !ok {
		t.Fatal("expected connection failure to be logged without a payload")
	}
	if _, hasErr := entry.keys["error"]; hasErr {
		t.Fatal("expected no error key when the payload is absent")
	}
}

func TestEventErrorFallsBackToNilForUnknownPayloads(t *testing.T) {
	if got := eventError("not an error"); got != nil {
		t.Fatalf("expected nil for an unrecognized payload, got %v", got)
	}
	if got := eventError(nil); got != nil {
		t.Fatalf("expected nil for a nil payload, got %v", got)
	}

	cause := errors.New("boom")
	if got := eventError(cause); !errors.Is(got, cause) {
		t.Fatalf("expected a plain error payload to pass through, got %v", got)
	}
}

// newAuthTestClient fails the first failures auth.test calls.
func newAuthTestClient(t *testing.T, failures int) (*slack.Client, *int) {
	t.Helper()

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls <= failures {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"user_id":"U08R6PTE4LA","bot_id":"B08R6PTDHQA"}`)
	}))
	t.Cleanup(server.Close)

	return slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/")), &calls
}

func TestResolveBotIdentityRetriesUntilSuccess(t *testing.T) {
	withFastAuthTestBackoff(t)

	api, calls := newAuthTestClient(t, 2)

	identity, err := resolveBotIdentity(context.Background(), api, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("expected auth test to succeed after retries, got %v", err)
	}
	if identity.UserID != "U08R6PTE4LA" || identity.BotID != "B08R6PTDHQA" {
		t.Fatalf("expected identity to be populated, got %+v", identity)
	}
	if *calls != 3 {
		t.Fatalf("expected 3 auth test calls, got %d", *calls)
	}
}

// Exhausted retries must fail startup to prevent self-reply loops.
func TestResolveBotIdentityFailsAfterMaxAttempts(t *testing.T) {
	withFastAuthTestBackoff(t)

	api, calls := newAuthTestClient(t, authTestMaxAttempts)

	if _, err := resolveBotIdentity(context.Background(), api, slog.New(slog.NewTextHandler(io.Discard, nil))); err == nil {
		t.Fatal("expected an error once attempts are exhausted")
	}
	if *calls != authTestMaxAttempts {
		t.Fatalf("expected %d auth test calls, got %d", authTestMaxAttempts, *calls)
	}
}

func TestResolveBotIdentityStopsOnCanceledContext(t *testing.T) {
	withFastAuthTestBackoff(t)

	api, _ := newAuthTestClient(t, authTestMaxAttempts)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := resolveBotIdentity(ctx, api, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// withFastAuthTestBackoff shortens retry delays for tests.
func withFastAuthTestBackoff(t *testing.T) {
	t.Helper()

	initial, max := authTestInitialBackoff, authTestMaxBackoff
	authTestInitialBackoff, authTestMaxBackoff = time.Millisecond, time.Millisecond
	t.Cleanup(func() {
		authTestInitialBackoff, authTestMaxBackoff = initial, max
	})
}

func TestNewBotConstructsWithProvidedDependencies(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{Logger: logger}
	msgHandler := &stubMessageHandler{}

	b := newBot(cfg, nil, nil, nil, nil, msgHandler)
	if b == nil {
		t.Fatal("expected bot instance")
	}
	if b.config != cfg {
		t.Fatal("expected config to be assigned")
	}
	if b.messageHandler != msgHandler {
		t.Fatal("expected message handler to be assigned")
	}
	if b.grpcClient != nil {
		t.Fatal("expected nil grpcClient")
	}
	if b.text2img != nil {
		t.Fatal("expected nil text2img")
	}
}
