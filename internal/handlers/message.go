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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	slackbotimage "slack-bot/internal/image"
	ledclient "slack-bot/pkg/led/client"

	"github.com/enescakir/emoji"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// BotIdentity contains auth.test IDs used to detect the bot's posts.
type BotIdentity struct {
	UserID string
	BotID  string
}

type MessageHandler struct {
	api                *slack.Client
	logger             *slog.Logger
	identity           BotIdentity
	text2img           *slackbotimage.Text2Image
	ledClient          *ledclient.ImageClient
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
	// maxEmojiCacheEntries bounds the decoded-image cache. Expired entries are
	// otherwise only dropped when the same emoji is looked up again, so one used
	// once would stay resident for the life of the process.
	maxEmojiCacheEntries = 256
	// maxEmojiBytes and maxEmojiPixels bound a download. The URLs come from the
	// workspace emoji list rather than the message, but a decoder fed an
	// unbounded stream, or a small file that expands to a huge bitmap, can still
	// exhaust the memory budget of the container.
	maxEmojiBytes  = 1 << 20
	maxEmojiPixels = 1 << 20
)

func NewMessageHandler(
	api *slack.Client,
	logger *slog.Logger,
	identity BotIdentity,
	text2img *slackbotimage.Text2Image,
	ledClient *ledclient.ImageClient,
	imageDuration int32,
	emojiListCacheTTL time.Duration,
	emojiImageCacheTTL time.Duration,
) *MessageHandler {
	return &MessageHandler{
		api:                api,
		logger:             logger,
		identity:           identity,
		text2img:           text2img,
		ledClient:          ledClient,
		imageDuration:      imageDuration,
		emojiCache:         make(map[string]emojiCacheEntry),
		emojiListCache:     nil,
		emojiListFetchedAt: time.Time{},
		emojiListCacheTTL:  emojiListCacheTTL,
		emojiImageCacheTTL: emojiImageCacheTTL,
		httpClient:         &http.Client{Timeout: 10 * time.Second},
	}
}

// slackEntityRe matches the <...> spans Slack wraps mentions, channels, links
// and special commands in.
var slackEntityRe = regexp.MustCompile(`<([^<>]*)>`)

// slackEscapes reverses the three characters Slack escapes in message text.
var slackEscapes = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")

// decodeSlackText turns Slack's wire format into what the author saw when they
// typed it. Entity spans are replaced before the escapes are reversed, so an
// escaped "<" in the original message is never mistaken for markup.
func decodeSlackText(s string) string {
	decoded := slackEntityRe.ReplaceAllStringFunc(s, func(match string) string {
		return decodeSlackEntity(match[1 : len(match)-1])
	})
	return slackEscapes.Replace(decoded)
}

// decodeSlackEntity renders one <...> span. Slack supplies a label after a pipe
// for most spans; without one only the raw ID is available.
func decodeSlackEntity(body string) string {
	label := ""
	if i := strings.Index(body, "|"); i >= 0 {
		label, body = body[i+1:], body[:i]
	}

	switch {
	case strings.HasPrefix(body, "@"):
		return "@" + fallback(label, body[1:])
	case strings.HasPrefix(body, "#"):
		return "#" + fallback(label, body[1:])
	case strings.HasPrefix(body, "!subteam^"):
		// The label already carries its own "@".
		return fallback(label, "@group")
	case strings.HasPrefix(body, "!"):
		// here, channel, everyone, and the date spans, whose label is the
		// preformatted fallback text.
		if label != "" {
			return label
		}
		return "@" + body[1:]
	default:
		return fallback(label, body)
	}
}

func fallback(label, raw string) string {
	if label != "" {
		return label
	}
	return raw
}

// handledSubTypes lists the message subtypes that carry new text to display.
// Anything else either announces no new content (joins, topic changes) or keeps
// the author and text under ev.Message, where the self-post guard cannot reach
// them: message_changed arrives with an empty User and Text, so every edit would
// otherwise slip past isOwnPost and render as "(empty message)".
var handledSubTypes = map[string]struct{}{
	"":                 {},
	"bot_message":      {},
	"file_share":       {},
	"me_message":       {},
	"thread_broadcast": {},
}

func isHandledSubType(subType string) bool {
	_, ok := handledSubTypes[subType]
	return ok
}

// isOwnPost detects messages that would cause a self-reply loop.
func (h *MessageHandler) isOwnPost(user, botID string) bool {
	if h.identity.UserID != "" && user == h.identity.UserID {
		return true
	}
	// bot_message events may only carry bot_id.
	return h.identity.BotID != "" && botID == h.identity.BotID
}

// HandleAppMention only records the mention. Slack delivers a channel mention as
// both app_mention and message, and HandleMessage already renders the text and
// acknowledges it, so replying here too answered one mention twice.
func (h *MessageHandler) HandleAppMention(ctx context.Context, ev *slackevents.AppMentionEvent) {
	_ = ctx
	if h.isOwnPost(ev.User, ev.BotID) {
		return
	}

	h.logger.Debug("App mention received",
		slog.String("channel", ev.Channel),
		slog.String("user", ev.User),
	)
}

func (h *MessageHandler) HandleMessage(ctx context.Context, ev *slackevents.MessageEvent) {
	if !isHandledSubType(ev.SubType) {
		h.logger.Debug("Ignoring message subtype",
			slog.String("subtype", ev.SubType),
			slog.String("channel", ev.Channel),
		)
		return
	}

	if h.isOwnPost(ev.User, ev.BotID) {
		return
	}

	messageText := ev.Text
	if ev.SubType == "bot_message" && ev.Message != nil && len(ev.Message.Attachments) > 0 {
		if extracted := extractTextFromAttachments(ev.Message.Attachments); extracted != "" {
			messageText = extracted
		}
	}

	messageText = decodeSlackText(messageText)

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
	resolver := h.newEmojiResolver(ctx, emojiMap)

	imageData, err := h.text2img.RenderTextWithEmoji(text, resolver)
	if err != nil {
		return err
	}

	if h.ledClient != nil {
		width, height, parseErr := parsePPMSize(imageData)
		if parseErr != nil {
			h.logger.Warn("Failed to parse PPM size", slog.Any("error", parseErr))
		}

		_, sendErr := h.ledClient.SendImage(
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

func (h *MessageHandler) newEmojiResolver(ctx context.Context, emojiMap map[string]string) func(string) (image.Image, error) {
	return func(name string) (image.Image, error) {
		return h.resolveEmojiImage(ctx, emojiMap, name)
	}
}

func (h *MessageHandler) resolveEmojiImage(ctx context.Context, emojiMap map[string]string, name string) (image.Image, error) {
	imageCacheTTL := h.emojiImageCacheTTL

	h.cacheMu.RLock()
	entry, found := h.emojiCache[name]
	h.cacheMu.RUnlock()

	if found {
		if time.Since(entry.fetchedAt) < imageCacheTTL {
			h.logger.Debug("Emoji found in cache", slog.String("name", name))
			return entry.img, nil
		}
		h.cacheMu.Lock()
		delete(h.emojiCache, name)
		h.cacheMu.Unlock()
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
	h.storeEmojiLocked(name, img, imageCacheTTL)
	h.cacheMu.Unlock()

	return img, nil
}

// storeEmojiLocked inserts img, first dropping entries past their TTL and then
// the oldest ones if the cache is still full.
func (h *MessageHandler) storeEmojiLocked(name string, img image.Image, ttl time.Duration) {
	now := time.Now()
	for key, entry := range h.emojiCache {
		if now.Sub(entry.fetchedAt) >= ttl {
			delete(h.emojiCache, key)
		}
	}

	for len(h.emojiCache) >= maxEmojiCacheEntries {
		oldestKey := ""
		var oldest time.Time
		for key, entry := range h.emojiCache {
			if oldestKey == "" || entry.fetchedAt.Before(oldest) {
				oldestKey, oldest = key, entry.fetchedAt
			}
		}
		delete(h.emojiCache, oldestKey)
	}

	h.emojiCache[name] = emojiCacheEntry{img: img, fetchedAt: now}
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

	// Read one byte past the limit so an oversized body is reported as such
	// rather than as a truncated image.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxEmojiBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read failed: %w", err)
	}
	if len(data) > maxEmojiBytes {
		return nil, fmt.Errorf("emoji exceeds %d bytes", maxEmojiBytes)
	}

	// Check the dimensions before decoding: a small file can still expand into a
	// bitmap far larger than the display needs.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode config failed: %w", err)
	}
	if cfg.Width*cfg.Height > maxEmojiPixels {
		return nil, fmt.Errorf("emoji is %dx%d, over the %d pixel limit", cfg.Width, cfg.Height, maxEmojiPixels)
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}
	return img, nil
}

func (h *MessageHandler) getCachedEmojiMap() (map[string]string, bool) {
	h.cacheMu.RLock()
	cachedMap := h.emojiListCache
	fetchedAt := h.emojiListFetchedAt
	h.cacheMu.RUnlock()

	if cachedMap != nil && time.Since(fetchedAt) < h.emojiListCacheTTL {
		return cachedMap, true
	}
	return nil, false
}

func (h *MessageHandler) getEmojiMap(ctx context.Context) map[string]string {
	if cachedMap, ok := h.getCachedEmojiMap(); ok {
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
