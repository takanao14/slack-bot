package bot

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"slack-bot/internal/config"

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
