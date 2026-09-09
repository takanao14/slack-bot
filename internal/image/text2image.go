package image

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	imagedraw "image/draw"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	// DPI is the font rendering resolution.
	DPI = 72
	// trailingPadding adds space after the rendered content.
	trailingPadding = 16
	// maxTextRunes limits canvas allocation for long Slack messages.
	maxTextRunes = 300
	ellipsis     = "…"
)

var emojiTokenRe = regexp.MustCompile(`:([a-zA-Z0-9_+\-]+):`)

// EmojiResolver resolves an emoji name to an image.
type EmojiResolver func(name string) (image.Image, error)

// Text2Image renders text and emojis as images.
type Text2Image struct {
	face   font.Face
	height int
	logger *slog.Logger
}

// NewText2Image loads a font at pixelHeight. A zero height defaults to 32.
func NewText2Image(fontPath string, pixelHeight int, logger *slog.Logger) (*Text2Image, error) {
	if pixelHeight == 0 {
		pixelHeight = 32
	}

	ttf, err := os.ReadFile(fontPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read font file %q: %w", fontPath, err)
	}

	drawFont, err := opentype.Parse(ttf)
	if err != nil {
		return nil, fmt.Errorf("failed to parse font file %q: %w", fontPath, err)
	}

	face, err := opentype.NewFace(drawFont, &opentype.FaceOptions{
		Size:    float64(pixelHeight),
		DPI:     DPI,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create font face from %q: %w", fontPath, err)
	}

	return &Text2Image{
		face:   face,
		height: pixelHeight,
		logger: logger,
	}, nil
}

// Close releases the font face.
func (t *Text2Image) Close() error {
	if t.face != nil {
		return t.face.Close()
	}
	return nil
}

// RenderTextWithEmoji renders text and :emoji: tokens as a PPM image.
func (t *Text2Image) RenderTextWithEmoji(text string, resolve EmojiResolver) ([]byte, error) {
	singleLine := strings.ReplaceAll(text, "\n", " ")
	if strings.TrimSpace(singleLine) == "" {
		singleLine = "(empty message)"
	}
	// Truncate by rune to avoid splitting UTF-8 characters.
	if runes := []rune(singleLine); len(runes) > maxTextRunes {
		t.logger.Info("Truncating message to bound the rendered image",
			slog.Int("runes", len(runes)),
			slog.Int("limit", maxTextRunes),
		)
		singleLine = string(runes[:maxTextRunes]) + ellipsis
	}

	items, totalWidth := t.calculateLayout(singleLine, resolve)
	totalWidth += trailingPadding

	if totalWidth < 1 {
		totalWidth = 1
	}
	imgHeight := t.height
	if imgHeight < 1 {
		imgHeight = 1
	}

	canvas := image.NewRGBA(image.Rect(0, 0, totalWidth, imgHeight))
	imagedraw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.Black}, image.Point{}, imagedraw.Src)

	t.drawItems(canvas, items)

	return encodePPM(canvas), nil
}

// calculateLayout returns render items and their total width.
func (t *Text2Image) calculateLayout(text string, resolve EmojiResolver) ([]renderItem, int) {
	parts := splitEmojiParts(text)
	emojiSize := t.height

	var items []renderItem
	totalWidth := 0
	for _, p := range parts {
		item := renderItem{part: p}
		if p.isEmoji && resolve != nil {
			if src, err := resolve(p.value); err == nil && src != nil {
				srcBounds := src.Bounds()
				if srcBounds.Dy() > 0 {
					// Preserve the aspect ratio at the target height.
					w := srcBounds.Dx() * emojiSize / srcBounds.Dy()
					if w < 1 {
						w = 1
					}
					item.width = w
					item.img = src
					item.imgBounds = srcBounds
				}
			}
		}

		if item.img == nil {
			txt := p.value
			if p.isEmoji {
				// Preserve unresolved emoji tokens as text.
				txt = ":" + p.value + ":"
			}
			item.width = t.measureTextWidth(txt)
			item.text = txt
		}

		items = append(items, item)
		totalWidth += item.width
	}
	return items, totalWidth
}

// drawItems renders laid-out items onto the canvas.
func (t *Text2Image) drawItems(canvas *image.RGBA, items []renderItem) {
	x := 0
	emojiSize := t.height
	for _, item := range items {
		if item.img != nil {
			dstRect := image.Rect(x, 0, x+item.width, emojiSize)
			draw.ApproxBiLinear.Scale(canvas, dstRect, item.img, item.imgBounds, imagedraw.Over, nil)
		} else if item.text != "" {
			t.drawTextSegment(canvas, item.text, x)
		}
		x += item.width
	}
}

// encodePPM encodes an RGBA image as binary PPM (P6).
func encodePPM(img *image.RGBA) []byte {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	var buf bytes.Buffer
	// A wide strip reallocates a dozen times without this.
	buf.Grow(width*height*3 + 32)

	fmt.Fprintf(&buf, "P6\n%d %d\n255\n", width, height)

	// Pix already stores premultiplied 8-bit RGBA values.
	for y := 0; y < height; y++ {
		start := img.PixOffset(bounds.Min.X, bounds.Min.Y+y)
		row := img.Pix[start : start+width*4]
		for i := 0; i < len(row); i += 4 {
			buf.Write(row[i : i+3])
		}
	}

	return buf.Bytes()
}

type part struct {
	isEmoji bool
	value   string
}

type renderItem struct {
	part      part
	width     int
	text      string
	img       image.Image
	imgBounds image.Rectangle
}

// splitEmojiParts splits a string into alternating text and emoji name parts.
func splitEmojiParts(s string) []part {
	matches := emojiTokenRe.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return []part{{isEmoji: false, value: s}}
	}

	var parts []part
	pos := 0
	for _, m := range matches {
		fullStart, fullEnd := m[0], m[1]
		nameStart, nameEnd := m[2], m[3]

		if pos < fullStart {
			parts = append(parts, part{isEmoji: false, value: s[pos:fullStart]})
		}
		parts = append(parts, part{isEmoji: true, value: s[nameStart:nameEnd]})
		pos = fullEnd
	}
	if pos < len(s) {
		parts = append(parts, part{isEmoji: false, value: s[pos:]})
	}
	return parts
}

// measureTextWidth calculates the pixel width of a string.
func (t *Text2Image) measureTextWidth(s string) int {
	if s == "" {
		return 0
	}
	d := &font.Drawer{Face: t.face}
	// MeasureString returns width in 26.6 fixed-point format, so we Ceil it.
	w := d.MeasureString(s).Ceil()
	if w < 0 {
		return 0
	}
	return w
}

// drawTextSegment vertically centers and draws text.
func (t *Text2Image) drawTextSegment(dst imagedraw.Image, s string, x int) {
	if s == "" {
		return
	}

	// Bounds are relative to the baseline; Min.Y is typically negative.
	bounds, _ := font.BoundString(t.face, s)
	textHeight := (bounds.Max.Y - bounds.Min.Y).Ceil()

	// Center the bounds, then convert their top edge to the baseline.
	yOffset := (t.height-textHeight)/2 - bounds.Min.Y.Ceil() + 1

	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(color.White),
		Face: t.face,
		Dot:  fixed.P(x, yOffset),
	}
	d.DrawString(s)
}
