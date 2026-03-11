package handlers

import (
	"context"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/http"
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

func TestNewMessageHandlerStoresConfiguredCacheTTLs(t *testing.T) {
	h := NewMessageHandler(nil, testLogger(), "", nil, nil, 10, 15*time.Minute, 45*time.Minute)

	if h.emojiListCacheTTL != 15*time.Minute {
		t.Fatalf("expected emoji list cache TTL to be stored, got %v", h.emojiListCacheTTL)
	}
	if h.emojiImageCacheTTL != 45*time.Minute {
		t.Fatalf("expected emoji image cache TTL to be stored, got %v", h.emojiImageCacheTTL)
	}
}

func TestMessageHandlerTTLGettersFallbackToDefaults(t *testing.T) {
	h := &MessageHandler{}

	if got := h.getEmojiListCacheTTL(); got != defaultEmojiListCacheTTL {
		t.Fatalf("expected default emoji list cache TTL %v, got %v", defaultEmojiListCacheTTL, got)
	}
	if got := h.getEmojiImageCacheTTL(); got != defaultEmojiImageCacheTTL {
		t.Fatalf("expected default emoji image cache TTL %v, got %v", defaultEmojiImageCacheTTL, got)
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
