package handlers

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGetCachedEmojiMapReturnsCachedValue(t *testing.T) {
	h := &MessageHandler{
		logger:             testLogger(),
		emojiListCache:     map[string]string{"wave": "https://example.com/wave.png"},
		emojiListFetchedAt: time.Now(),
		emojiListCacheTTL:  time.Hour,
	}

	got, ok := h.getCachedEmojiMap()
	if !ok {
		t.Fatal("expected cached emoji map to be returned")
	}
	if got["wave"] != "https://example.com/wave.png" {
		t.Fatalf("unexpected cached emoji map value: %v", got)
	}
}

func TestGetCachedEmojiMapReturnsFalseWhenExpired(t *testing.T) {
	h := &MessageHandler{
		logger:             testLogger(),
		emojiListCache:     map[string]string{"wave": "https://example.com/wave.png"},
		emojiListFetchedAt: time.Now().Add(-time.Hour - time.Second),
		emojiListCacheTTL:  time.Hour,
	}

	got, ok := h.getCachedEmojiMap()
	if ok {
		t.Fatal("expected expired emoji map cache miss")
	}
	if got != nil {
		t.Fatalf("expected no expired emoji map, got: %v", got)
	}
}

func TestHandleEmojiChangedInvalidatesEmojiListCache(t *testing.T) {
	h := &MessageHandler{
		logger:             testLogger(),
		emojiCache:         make(map[string]emojiCacheEntry),
		emojiListCache:     map[string]string{"wave": "https://example.com/wave.png"},
		emojiListFetchedAt: time.Now(),
		emojiListCacheTTL:  time.Hour,
	}

	h.HandleEmojiChanged(context.Background(), &slackevents.EmojiChangedEvent{Subtype: "add", Name: "party"})

	if got, ok := h.getCachedEmojiMap(); ok || got != nil {
		t.Fatalf("expected emoji list cache to be invalidated, got ok=%v map=%v", ok, got)
	}
}

func TestNewEmojiResolverReturnsFreshCachedEmoji(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	h := &MessageHandler{
		logger:             testLogger(),
		emojiCache:         map[string]emojiCacheEntry{"cached_custom": {img: img, fetchedAt: time.Now()}},
		emojiImageCacheTTL: time.Hour,
		httpClient:         &http.Client{Timeout: time.Second},
	}

	resolver := h.newEmojiResolver(context.Background(), map[string]string{})
	got, err := resolver("cached_custom")
	if err != nil {
		t.Fatalf("expected fresh cached emoji, got error: %v", err)
	}
	if got != img {
		t.Fatal("expected resolver to return cached emoji image")
	}
}

func TestNewEmojiResolverRemovesExpiredCacheEntryImmediately(t *testing.T) {
	h := &MessageHandler{
		logger:             testLogger(),
		emojiImageCacheTTL: time.Hour,
		emojiCache: map[string]emojiCacheEntry{
			"expired_custom": {img: image.NewRGBA(image.Rect(0, 0, 1, 1)), fetchedAt: time.Now().Add(-time.Hour - time.Second)},
		},
		httpClient: &http.Client{Timeout: time.Second},
	}

	resolver := h.newEmojiResolver(context.Background(), map[string]string{})
	_, err := resolver("expired_custom")
	if err == nil {
		t.Fatal("expected resolver miss after expired cache entry removal")
	}

	h.cacheMu.RLock()
	_, ok := h.emojiCache["expired_custom"]
	h.cacheMu.RUnlock()
	if ok {
		t.Fatal("expected expired emoji cache entry to be removed immediately")
	}
}

func TestEmojiListCacheCanRemainValidWhenImageCacheExpires(t *testing.T) {
	listTTL := 2 * time.Hour
	imageTTL := 30 * time.Minute
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))

	h := &MessageHandler{
		logger:             testLogger(),
		emojiListCache:     map[string]string{"wave": "https://example.com/wave.png"},
		emojiListFetchedAt: time.Now().Add(-time.Hour),
		emojiListCacheTTL:  listTTL,
		emojiImageCacheTTL: imageTTL,
		emojiCache: map[string]emojiCacheEntry{
			"expired_custom": {img: img, fetchedAt: time.Now().Add(-time.Hour)},
		},
		httpClient: &http.Client{Timeout: time.Second},
	}

	gotMap, ok := h.getCachedEmojiMap()
	if !ok || gotMap["wave"] == "" {
		t.Fatalf("expected emoji list cache to remain valid, got ok=%v map=%v", ok, gotMap)
	}

	resolver := h.newEmojiResolver(context.Background(), map[string]string{})
	_, err := resolver("expired_custom")
	if err == nil {
		t.Fatal("expected image cache entry to expire independently from emoji list cache")
	}

	h.cacheMu.RLock()
	_, stillCached := h.emojiCache["expired_custom"]
	h.cacheMu.RUnlock()
	if stillCached {
		t.Fatal("expected expired image cache entry to be removed")
	}
}

func TestImageCacheCanRemainValidWhenEmojiListCacheExpires(t *testing.T) {
	listTTL := 30 * time.Minute
	imageTTL := 2 * time.Hour
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))

	h := &MessageHandler{
		logger:             testLogger(),
		emojiListCache:     map[string]string{"wave": "https://example.com/wave.png"},
		emojiListFetchedAt: time.Now().Add(-time.Hour),
		emojiListCacheTTL:  listTTL,
		emojiImageCacheTTL: imageTTL,
		emojiCache: map[string]emojiCacheEntry{
			"cached_custom": {img: img, fetchedAt: time.Now().Add(-time.Hour)},
		},
		httpClient: &http.Client{Timeout: time.Second},
	}

	gotMap, ok := h.getCachedEmojiMap()
	if ok || gotMap != nil {
		t.Fatalf("expected emoji list cache to expire, got ok=%v map=%v", ok, gotMap)
	}

	resolver := h.newEmojiResolver(context.Background(), map[string]string{})
	gotImg, err := resolver("cached_custom")
	if err != nil {
		t.Fatalf("expected image cache entry to remain valid, got error: %v", err)
	}
	if gotImg != img {
		t.Fatal("expected resolver to return still-valid cached image")
	}
}

func TestStoreEmojiLockedDropsExpiredEntries(t *testing.T) {
	h := &MessageHandler{logger: testLogger(), emojiCache: map[string]emojiCacheEntry{
		"stale": {img: image.NewRGBA(image.Rect(0, 0, 1, 1)), fetchedAt: time.Now().Add(-2 * time.Hour)},
		"fresh": {img: image.NewRGBA(image.Rect(0, 0, 1, 1)), fetchedAt: time.Now()},
	}}

	h.storeEmojiLocked("new", image.NewRGBA(image.Rect(0, 0, 1, 1)), time.Hour)

	if _, ok := h.emojiCache["stale"]; ok {
		t.Fatal("expected the expired entry to be swept on insert")
	}
	for _, name := range []string{"fresh", "new"} {
		if _, ok := h.emojiCache[name]; !ok {
			t.Fatalf("expected %q to remain cached", name)
		}
	}
}

// Without a cap, an emoji used once stayed resident for the life of the process.
func TestStoreEmojiLockedCapsCacheSize(t *testing.T) {
	h := &MessageHandler{logger: testLogger(), emojiCache: map[string]emojiCacheEntry{}}

	base := time.Now()
	for i := 0; i < maxEmojiCacheEntries+50; i++ {
		h.emojiCache["e"+strconv.Itoa(i)] = emojiCacheEntry{
			img:       image.NewRGBA(image.Rect(0, 0, 1, 1)),
			fetchedAt: base.Add(time.Duration(i) * time.Second),
		}
	}
	h.storeEmojiLocked("newest", image.NewRGBA(image.Rect(0, 0, 1, 1)), time.Hour)

	if len(h.emojiCache) > maxEmojiCacheEntries {
		t.Fatalf("expected at most %d entries, got %d", maxEmojiCacheEntries, len(h.emojiCache))
	}
	if _, ok := h.emojiCache["newest"]; !ok {
		t.Fatal("expected the new entry to be kept")
	}
	if _, ok := h.emojiCache["e0"]; ok {
		t.Fatal("expected the oldest entry to be evicted first")
	}
}

func TestDownloadAndDecodeEmojiRejectsOversizedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, maxEmojiBytes+1))
	}))
	t.Cleanup(srv.Close)

	h := &MessageHandler{logger: testLogger(), httpClient: srv.Client()}
	_, err := h.downloadAndDecodeEmoji(context.Background(), srv.URL, "huge")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected an oversized-body error, got %v", err)
	}
}

// A small file can still expand into a bitmap far larger than the display needs.
func TestDownloadAndDecodeEmojiRejectsOversizedDimensions(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2000, 2000))); err != nil {
		t.Fatalf("failed to build fixture: %v", err)
	}
	if buf.Len() > maxEmojiBytes {
		t.Fatalf("fixture must stay under the byte limit to exercise the pixel check, got %d", buf.Len())
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	t.Cleanup(srv.Close)

	h := &MessageHandler{logger: testLogger(), httpClient: srv.Client()}
	_, err := h.downloadAndDecodeEmoji(context.Background(), srv.URL, "bomb")
	if err == nil || !strings.Contains(err.Error(), "pixel limit") {
		t.Fatalf("expected a pixel-limit error, got %v", err)
	}
}

func TestDownloadAndDecodeEmojiAcceptsNormalImage(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatalf("failed to build fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	t.Cleanup(srv.Close)

	h := &MessageHandler{logger: testLogger(), httpClient: srv.Client()}
	img, err := h.downloadAndDecodeEmoji(context.Background(), srv.URL, "ok")
	if err != nil {
		t.Fatalf("expected the download to succeed, got %v", err)
	}
	if got := img.Bounds().Dx(); got != 64 {
		t.Fatalf("expected a 64px wide image, got %d", got)
	}
}

func TestIsOwnPost(t *testing.T) {
	identity := BotIdentity{UserID: "U08R6PTE4LA", BotID: "B08R6PTDHQA"}

	tests := []struct {
		name     string
		identity BotIdentity
		user     string
		botID    string
		want     bool
	}{
		{name: "own message carries user and bot id", identity: identity, user: "U08R6PTE4LA", botID: "B08R6PTDHQA", want: true},
		{name: "own bot_message carries bot id only", identity: identity, user: "", botID: "B08R6PTDHQA", want: true},
		{name: "human message", identity: identity, user: "U5E3582NN", want: false},
		{name: "another bot", identity: identity, user: "UOTHERBOT", botID: "BOTHERBOT", want: false},
		{name: "unknown identity does not match a human", identity: BotIdentity{}, user: "U5E3582NN", want: false},
		{name: "unknown identity does not match an empty user", identity: BotIdentity{}, user: "", botID: "", want: false},
		{name: "user id only identity still matches own message", identity: BotIdentity{UserID: "U08R6PTE4LA"}, user: "U08R6PTE4LA", botID: "B08R6PTDHQA", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &MessageHandler{identity: tt.identity}
			if got := h.isOwnPost(tt.user, tt.botID); got != tt.want {
				t.Fatalf("expected isOwnPost to be %v, got %v", tt.want, got)
			}
		})
	}
}

// Missing dependencies expose any regression in the self-post guard.
func TestHandleMessageIgnoresOwnPosts(t *testing.T) {
	tests := []struct {
		name  string
		event *slackevents.MessageEvent
	}{
		{
			name:  "ack echoed back with user and bot id",
			event: &slackevents.MessageEvent{Channel: "C5H95KWNP", User: "U08R6PTE4LA", BotID: "B08R6PTDHQA", Text: "Ack"},
		},
		{
			name:  "ack echoed back as bot_message without user",
			event: &slackevents.MessageEvent{Channel: "C5H95KWNP", BotID: "B08R6PTDHQA", SubType: "bot_message", Text: "Ack"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("expected own post to be ignored before processing, got panic: %v", r)
				}
			}()

			h := NewMessageHandler(
				nil,
				testLogger(),
				BotIdentity{UserID: "U08R6PTE4LA", BotID: "B08R6PTDHQA"},
				nil,
				nil,
				10,
				0,
				0,
				time.Hour,
				time.Hour,
			)

			h.HandleMessage(context.Background(), tt.event)
		})
	}
}

// A nil client exposes any reply reintroduced here: Slack delivers a channel
// mention as both app_mention and message, so replying would answer twice.
func TestHandleAppMentionDoesNotReply(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("expected no Slack API call, got panic: %v", r)
		}
	}()

	h := NewMessageHandler(nil, testLogger(), BotIdentity{UserID: "U08R6PTE4LA"}, nil, nil, 10, 0, 0, time.Hour, time.Hour)
	h.HandleAppMention(context.Background(), &slackevents.AppMentionEvent{
		Channel: "C5H95KWNP",
		User:    "U5E3582NN",
		Text:    "<@U08R6PTE4LA> hello",
	})
}

func TestDecodeSlackText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain text is untouched", input: "hello world", want: "hello world"},
		{name: "labelled user mention", input: "hi <@U5E3582NN|takanao>", want: "hi @takanao"},
		{name: "bare user mention falls back to the id", input: "hi <@U5E3582NN>", want: "hi @U5E3582NN"},
		{name: "labelled channel", input: "see <#C5H95KWNP|general>", want: "see #general"},
		{name: "bare channel falls back to the id", input: "see <#C5H95KWNP>", want: "see #C5H95KWNP"},
		{name: "bare link", input: "<https://example.com>", want: "https://example.com"},
		{name: "labelled link uses the label", input: "<https://example.com|Example>", want: "Example"},
		{name: "mailto", input: "<mailto:a@example.com|a@example.com>", want: "a@example.com"},
		{name: "here", input: "<!here> deploy done", want: "@here deploy done"},
		{name: "channel command", input: "<!channel>", want: "@channel"},
		{name: "user group uses its label", input: "<!subteam^S123|@sre>", want: "@sre"},
		{name: "user group without a label", input: "<!subteam^S123>", want: "@group"},
		{name: "date span uses the fallback label", input: "at <!date^1697000000^{date_short}|Oct 11, 2023>", want: "at Oct 11, 2023"},
		{name: "escapes are restored", input: "a &amp; b &lt; c &gt; d", want: "a & b < c > d"},
		{name: "escaped angle brackets are not parsed as markup", input: "&lt;@U5E3582NN&gt;", want: "<@U5E3582NN>"},
		{name: "mixed", input: "<@U1|ken> pushed &amp; deployed <https://ci/1|build 1> <!here>", want: "@ken pushed & deployed build 1 @here"},
		{name: "emoji tokens survive", input: "done <@U1|ken> :tada:", want: "done @ken :tada:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeSlackText(tt.input); got != tt.want {
				t.Fatalf("decodeSlackText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsHandledSubType(t *testing.T) {
	tests := []struct {
		subType string
		want    bool
	}{
		{subType: "", want: true},
		{subType: "bot_message", want: true},
		{subType: "file_share", want: true},
		{subType: "me_message", want: true},
		{subType: "thread_broadcast", want: true},
		{subType: "message_changed", want: false},
		{subType: "message_deleted", want: false},
		{subType: "message_replied", want: false},
		{subType: "channel_join", want: false},
		{subType: "channel_leave", want: false},
		{subType: "channel_topic", want: false},
		{subType: "tombstone", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.subType, func(t *testing.T) {
			if got := isHandledSubType(tt.subType); got != tt.want {
				t.Fatalf("expected isHandledSubType(%q) to be %v, got %v", tt.subType, tt.want, got)
			}
		})
	}
}

// Missing dependencies expose any regression in the subtype guard. A
// message_changed event carries no User, so isOwnPost alone would let the bot's
// own edits through.
func TestHandleMessageIgnoresUnhandledSubTypes(t *testing.T) {
	events := []*slackevents.MessageEvent{
		{Channel: "C5H95KWNP", SubType: "message_changed"},
		{Channel: "C5H95KWNP", SubType: "message_deleted", DeletedTimeStamp: "1697000000.000100"},
		{Channel: "C5H95KWNP", SubType: "channel_join", User: "U5E3582NN", Text: "<@U5E3582NN> has joined the channel"},
		{Channel: "C5H95KWNP", SubType: "channel_topic", User: "U5E3582NN", Text: "set the channel topic"},
	}

	for _, ev := range events {
		t.Run(ev.SubType, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("expected subtype to be ignored before processing, got panic: %v", r)
				}
			}()

			h := NewMessageHandler(
				nil,
				testLogger(),
				BotIdentity{UserID: "U08R6PTE4LA", BotID: "B08R6PTDHQA"},
				nil,
				nil,
				10,
				0,
				0,
				time.Hour,
				time.Hour,
			)

			h.HandleMessage(context.Background(), ev)
		})
	}
}

func TestNewMessageHandlerStoresConfiguredCacheTTLs(t *testing.T) {
	h := NewMessageHandler(nil, testLogger(), BotIdentity{}, nil, nil, 10, 0, 0, 15*time.Minute, 45*time.Minute)

	if h.emojiListCacheTTL != 15*time.Minute {
		t.Fatalf("expected emoji list cache TTL to be stored, got %v", h.emojiListCacheTTL)
	}
	if h.emojiImageCacheTTL != 45*time.Minute {
		t.Fatalf("expected emoji image cache TTL to be stored, got %v", h.emojiImageCacheTTL)
	}
}

func TestEmojiCacheConcurrentAccess(t *testing.T) {
	// This test ensures that concurrent access to the cache map does not cause race conditions.
	// Run with 'go test -race' to verify.
	h := &MessageHandler{
		logger:             testLogger(),
		emojiCache:         make(map[string]emojiCacheEntry),
		emojiListCache:     map[string]string{"wave": "https://example.com/wave.png"},
		emojiListFetchedAt: time.Now(),
		emojiListCacheTTL:  time.Hour,
	}

	var wg sync.WaitGroup
	concurrency := 10

	// Simulate concurrent readers
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.getCachedEmojiMap()
		}()
	}

	// Simulate concurrent writers (invalidation)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := fmt.Sprintf("emoji_%d", id)
			h.HandleEmojiChanged(context.Background(), &slackevents.EmojiChangedEvent{Subtype: "add", Name: name})
		}(i)
	}

	wg.Wait()
}

func TestParsePPMSize(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantW   int
		wantH   int
		wantErr bool
	}{
		{name: "valid", data: []byte("P6\n16 32\n255\nxxx"), wantW: 16, wantH: 32},
		{name: "invalid magic", data: []byte("P3\n16 32\n255\nxxx"), wantErr: true},
		{name: "invalid dims", data: []byte("P6\n16\n255\nxxx"), wantErr: true},
		{name: "invalid max", data: []byte("P6\n16 32\n100\nxxx"), wantErr: true},
		{name: "invalid header", data: []byte("P6\n"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, h, err := parsePPMSize(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if w != tt.wantW || h != tt.wantH {
				t.Fatalf("unexpected size: got %dx%d, want %dx%d", w, h, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestResolveEmojiURL(t *testing.T) {
	tests := []struct {
		name     string
		emojiMap map[string]string
		query    string
		wantOK   bool
		contains string
	}{
		{
			name:     "direct url",
			emojiMap: map[string]string{"wave": "https://example.com/wave.png"},
			query:    "wave",
			wantOK:   true,
			contains: "https://example.com/wave.png",
		},
		{
			name:     "alias chain",
			emojiMap: map[string]string{"a": "alias:b", "b": "https://example.com/b.png"},
			query:    "a",
			wantOK:   true,
			contains: "https://example.com/b.png",
		},
		{
			name:     "alias cycle",
			emojiMap: map[string]string{"a": "alias:b", "b": "alias:a"},
			query:    "a",
			wantOK:   false,
		},
		{
			name:     "unicode from map",
			emojiMap: map[string]string{"smile": "😀"},
			query:    "smile",
			wantOK:   true,
			contains: "cdnjs.cloudflare.com/ajax/libs/twemoji",
		},
		{
			name:     "fallback via emoji package",
			emojiMap: map[string]string{},
			query:    "wave",
			wantOK:   true,
			contains: "cdnjs.cloudflare.com/ajax/libs/twemoji",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolveEmojiURL(tt.emojiMap, tt.query, map[string]struct{}{})
			if ok != tt.wantOK {
				t.Fatalf("unexpected ok: got %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK && !strings.Contains(got, tt.contains) {
				t.Fatalf("unexpected url: got %q, want to contain %q", got, tt.contains)
			}
		})
	}
}

func TestEmojiToCodepoint(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: ""},
		{name: "single", input: "😀", want: "1f600"},
		{name: "variation selector is removed", input: "✌️", want: "270c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := emojiToCodepoint(tt.input)
			if got != tt.want {
				t.Fatalf("unexpected codepoint: got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractTextFromAttachments(t *testing.T) {
	attachments := []slack.Attachment{
		{
			Fallback: "fallback text",
			Pretext:  "pretext",
			Title:    "title",
			Text:     "body",
			Fields: []slack.AttachmentField{
				{Value: "field1"},
				{Value: "   "},
				{Value: "field2"},
			},
		},
		{
			Fallback: "second",
		},
	}

	got := extractTextFromAttachments(attachments)
	wantParts := []string{"fallback text", "pretext", "title", "body", "field1", "field2", "second"}
	for _, p := range wantParts {
		if !strings.Contains(got, p) {
			t.Fatalf("expected extracted text to contain %q, got %q", p, got)
		}
	}
}
