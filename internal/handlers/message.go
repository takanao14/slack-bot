package handlers

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	slackbotimage "slack-bot/internal/image"
	grpcclient "slack-bot/pkg/grpc/client"

	"github.com/enescakir/emoji"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type MessageHandler struct {
	api                *slack.Client
	logger             *slog.Logger
	botUserID          string
	text2img           *slackbotimage.Text2Image
	grpcClient         *grpcclient.ImageClient
	imageDuration      int32
	emojiCache         map[string]emojiCacheEntry
	emojiListCache     map[string]string
	emojiListFetchedAt time.Time
	emojiListCacheTTL  time.Duration
	emojiImageCacheTTL time.Duration
	cacheMu            sync.RWMutex
	httpClient         *http.Client
}

type emojiCacheEntry struct {
	img       image.Image
	fetchedAt time.Time
}

const (
	defaultEmojiListCacheTTL  = 24 * time.Hour
	defaultEmojiImageCacheTTL = 24 * time.Hour
)

func NewMessageHandler(
	api *slack.Client,
	logger *slog.Logger,
	botUserID string,
	text2img *slackbotimage.Text2Image,
	grpcClient *grpcclient.ImageClient,
	imageDuration int32,
	emojiListCacheTTL time.Duration,
	emojiImageCacheTTL time.Duration,
) *MessageHandler {
	return &MessageHandler{
		api:                api,
		logger:             logger,
		botUserID:          botUserID,
		text2img:           text2img,
		grpcClient:         grpcClient,
		imageDuration:      imageDuration,
		emojiCache:         make(map[string]emojiCacheEntry),
		emojiListCache:     nil,
		emojiListFetchedAt: time.Time{},
		emojiListCacheTTL:  emojiListCacheTTL,
		emojiImageCacheTTL: emojiImageCacheTTL,
		httpClient:         &http.Client{Timeout: 10 * time.Second},
	}
}

func (h *MessageHandler) HandleAppMention(ctx context.Context, ev *slackevents.AppMentionEvent) {
	if h.botUserID != "" && ev.User == h.botUserID {
		return
	}

	h.logger.Info("App mention received",
		slog.String("channel", ev.Channel),
		slog.String("user", ev.User),
	)

	_, _, err := h.api.PostMessageContext(
		ctx,
		ev.Channel,
		slack.MsgOptionText("Hello! Thank you for the mention.", false),
	)
	if err != nil {
		h.logger.Error("Failed to post message",
			slog.Any("error", err),
			slog.String("channel", ev.Channel),
		)
	}
}

func (h *MessageHandler) HandleMessage(ctx context.Context, ev *slackevents.MessageEvent) {
	if h.botUserID != "" && ev.User == h.botUserID {
		return
	}

	messageText := ev.Text
	if ev.SubType == "bot_message" && ev.Message != nil && len(ev.Message.Attachments) > 0 {
		if extracted := extractTextFromAttachments(ev.Message.Attachments); extracted != "" {
			messageText = extracted
		}
	}

	h.logger.Debug("Message event received",
		slog.String("channel", ev.Channel),
		slog.String("user", ev.User),
		slog.String("text", messageText),
	)

	if err := h.processMessageImage(ctx, messageText); err != nil {
		h.logger.Error("Failed to process message image",
			slog.Any("error", err),
			slog.String("channel", ev.Channel),
		)
		return
	}

	h.logger.Info("Message image processed",
		slog.String("channel", ev.Channel),
		slog.String("user", ev.User),
	)

	_, _, postErr := h.api.PostMessageContext(
		ctx,
		ev.Channel,
		slack.MsgOptionText("Ack", false),
	)
	if postErr != nil {
		h.logger.Error("Failed to post ack message",
			slog.Any("error", postErr),
			slog.String("channel", ev.Channel),
		)
	}
}

func (h *MessageHandler) HandleEmojiChanged(ctx context.Context, ev *slackevents.EmojiChangedEvent) {
	_ = ctx
	h.logger.Info("Emoji changed event received", slog.String("subtype", ev.Subtype))

	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()

	switch ev.Subtype {
	case "add":
		h.invalidateEmojiListCacheLocked()
	case "remove":
		for _, name := range ev.Names {
			delete(h.emojiCache, name)
		}
		h.invalidateEmojiListCacheLocked()
	case "rename":
		delete(h.emojiCache, ev.OldName)
		h.invalidateEmojiListCacheLocked()
	}
}

func (h *MessageHandler) processMessageImage(ctx context.Context, text string) error {
	emojiMap := h.getEmojiMap(ctx)

	resolver := func(name string) (image.Image, error) {
		return h.resolveEmojiImage(ctx, emojiMap, name)
	}

	imageData, err := h.text2img.RenderTextWithEmoji(text, resolver)
	if err != nil {
		return err
	}

	if h.grpcClient != nil {
		width, height, parseErr := parsePPMSize(imageData)
		if parseErr != nil {
			h.logger.Warn("Failed to parse PPM size", slog.Any("error", parseErr))
		}

		_, sendErr := h.grpcClient.SendImage(
			ctx,
			imageData,
			"image/x-portable-pixmap",
			h.imageDuration,
		)
		if sendErr != nil {
			h.logger.Error("Failed to send image via gRPC", slog.Any("error", sendErr))
			return sendErr
		}

		attrs := []any{slog.Int("size_bytes", len(imageData))}
		if parseErr == nil {
			attrs = append(attrs, slog.Int("width", width), slog.Int("height", height))
		}
		h.logger.Info("Image sent via gRPC", attrs...)
	}

	return nil
}

func (h *MessageHandler) resolveEmojiImage(ctx context.Context, emojiMap map[string]string, name string) (image.Image, error) {
	imageCacheTTL := h.getEmojiImageCacheTTL()

	h.cacheMu.RLock()
	entry, found := h.emojiCache[name]
	h.cacheMu.RUnlock()

	if found {
		if time.Since(entry.fetchedAt) < imageCacheTTL {
			h.logger.Debug("Emoji found in cache", slog.String("name", name))
			return entry.img, nil
		}
		h.logger.Debug("Emoji cache expired", slog.String("name", name))
	}

	url, ok := resolveEmojiURL(emojiMap, name, make(map[string]struct{}))
	if !ok {
		h.logger.Warn("Emoji URL resolution failed", slog.String("name", name))
		return nil, fmt.Errorf("emoji not found: %s", name)
	}

	img, err := h.downloadAndDecodeEmoji(ctx, url, name)
	if err != nil {
		return nil, err
	}

	h.cacheMu.Lock()
	h.emojiCache[name] = emojiCacheEntry{
		img:       img,
		fetchedAt: time.Now(),
	}
	h.cacheMu.Unlock()

	return img, nil
}

func (h *MessageHandler) downloadAndDecodeEmoji(ctx context.Context, url, name string) (image.Image, error) {
	h.logger.Debug("Downloading emoji", slog.String("name", name), slog.String("url", url))

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status: %d", resp.StatusCode)
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}
	return img, nil
}

func (h *MessageHandler) getEmojiMap(ctx context.Context) map[string]string {
	h.cacheMu.RLock()
	cachedMap := h.emojiListCache
	fetchedAt := h.emojiListFetchedAt
	h.cacheMu.RUnlock()

	if cachedMap != nil && time.Since(fetchedAt) < h.getEmojiListCacheTTL() {
		return cachedMap
	}

	if h.api == nil {
		return nil
	}

	h.logger.Info("Fetching emoji map from Slack")
	emojiMap, err := h.api.GetEmojiContext(ctx)
	if err != nil {
		h.logger.Warn("Failed to fetch emoji map", slog.Any("error", err))
		return nil
	}

	h.cacheMu.Lock()
	h.emojiListCache = emojiMap
	h.emojiListFetchedAt = time.Now()
	h.cacheMu.Unlock()

	return emojiMap
}

func (h *MessageHandler) getEmojiListCacheTTL() time.Duration {
	if h.emojiListCacheTTL > 0 {
		return h.emojiListCacheTTL
	}
	return defaultEmojiListCacheTTL
}

func (h *MessageHandler) getEmojiImageCacheTTL() time.Duration {
	if h.emojiImageCacheTTL > 0 {
		return h.emojiImageCacheTTL
	}
	return defaultEmojiImageCacheTTL
}

func (h *MessageHandler) invalidateEmojiListCacheLocked() {
	h.emojiListCache = nil
	h.emojiListFetchedAt = time.Time{}
}

func resolveEmojiURL(emojiMap map[string]string, name string, seen map[string]struct{}) (string, bool) {
	if _, ok := seen[name]; ok {
		return "", false
	}
	seen[name] = struct{}{}

	raw, ok := emojiMap[name]
	if !ok || raw == "" {
		unicodeVal := emoji.Parse(":" + name + ":")
		if unicodeVal != ":"+name+":" {
			return getTwemojiURL(unicodeVal), true
		}
		return "", false
	}

	if strings.HasPrefix(raw, "alias:") {
		return resolveEmojiURL(emojiMap, strings.TrimPrefix(raw, "alias:"), seen)
	}

	if strings.HasPrefix(raw, "http") {
		return raw, true
	}

	return getTwemojiURL(raw), true
}

func getTwemojiURL(emojiStr string) string {
	codepoint := emojiToCodepoint(emojiStr)
	if codepoint == "" {
		return ""
	}
	return fmt.Sprintf("https://cdnjs.cloudflare.com/ajax/libs/twemoji/14.0.2/72x72/%s.png", codepoint)
}

func emojiToCodepoint(emojiStr string) string {
	var parts []string
	for _, r := range emojiStr {
		if r == 0xFE0F {
			continue
		}
		parts = append(parts, fmt.Sprintf("%x", r))
	}
	return strings.Join(parts, "-")
}

func parsePPMSize(data []byte) (int, int, error) {
	r := bufio.NewReader(bytes.NewReader(data))

	magic, err := readToken(r)
	if err != nil {
		return 0, 0, fmt.Errorf("reading magic: %w", err)
	}
	if magic != "P6" {
		return 0, 0, fmt.Errorf("invalid magic: %s", magic)
	}

	wStr, err := readToken(r)
	if err != nil {
		return 0, 0, fmt.Errorf("reading width: %w", err)
	}
	w, err := strconv.Atoi(wStr)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid width: %w", err)
	}

	hStr, err := readToken(r)
	if err != nil {
		return 0, 0, fmt.Errorf("reading height: %w", err)
	}
	h, err := strconv.Atoi(hStr)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid height: %w", err)
	}

	maxValStr, err := readToken(r)
	if err != nil {
		return 0, 0, fmt.Errorf("reading max val: %w", err)
	}
	if maxValStr != "255" {
		return 0, 0, fmt.Errorf("unsupported max value: %s", maxValStr)
	}

	return w, h, nil
}

func readToken(r *bufio.Reader) (string, error) {
	var buf bytes.Buffer
	inComment := false
	for {
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF && buf.Len() > 0 {
				return buf.String(), nil
			}
			return "", err
		}

		if inComment {
			if b == '\n' {
				inComment = false
			}
			continue
		}

		if b == '#' {
			inComment = true
			if buf.Len() > 0 {
				return buf.String(), nil
			}
			continue
		}

		if isSpace(b) {
			if buf.Len() > 0 {
				return buf.String(), nil
			}
			continue
		}

		buf.WriteByte(b)
	}
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

func extractTextFromAttachments(attachments []slack.Attachment) string {
	var parts []string
	for _, att := range attachments {
		for _, s := range []string{att.Fallback, att.Pretext, att.Title, att.Text} {
			if s := strings.TrimSpace(s); s != "" {
				parts = append(parts, s)
			}
		}
		for _, f := range att.Fields {
			if s := strings.TrimSpace(f.Value); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, " ")
}
