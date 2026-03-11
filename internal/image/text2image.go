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
	// DPI is the standard screen dots per inch.
	DPI = 72
	// trailingPadding is the extra space added to the end of the rendered image.
	trailingPadding = 16
)

var emojiTokenRe = regexp.MustCompile(`:([a-zA-Z0-9_+\-]+):`)

// EmojiResolver is a function that resolves an emoji name to an image.
type EmojiResolver func(name string) (image.Image, error)

// Text2Image converts text strings, including emojis, into images.
type Text2Image struct {
	face   font.Face
	height int
	logger *slog.Logger
}

// NewText2Image creates a new Text2Image instance with the given font file and pixel height.
// If pixelHeight is 0, it defaults to 32.
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

// Close closes the font face, releasing its resources.
func (t *Text2Image) Close() error {
	if t.face != nil {
		return t.face.Close()
	}
	return nil
}

// RenderTextWithEmoji renders text with emoji support into a PPM image byte slice.
// Text like "Hello :emoji: World" will render with the emoji image embedded.
func (t *Text2Image) RenderTextWithEmoji(text string, resolve EmojiResolver) ([]byte, error) {
	singleLine := strings.ReplaceAll(text, "\n", " ")
	if strings.TrimSpace(singleLine) == "" {
		singleLine = "(empty message)"
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

	// Create canvas
	canvas := image.NewRGBA(image.Rect(0, 0, totalWidth, imgHeight))
	imagedraw.Draw(canvas, canvas.Bounds(), &image.Uniform{C: color.Black}, image.Point{}, imagedraw.Src)

	// Draw items onto the canvas
	t.drawItems(canvas, items)

	// Encode to PPM format
	return encodePPM(canvas), nil
}

// calculateLayout determines the size and position of each text and emoji segment.
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
					// Scale emoji width based on aspect ratio and target height
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

		// If the item is not a resolved emoji, treat it as text.
		if item.img == nil {
			txt := p.value
			if p.isEmoji {
				// Fallback for unresolved emoji: render the original token
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

// drawItems renders the pre-calculated layout items onto the canvas.
func (t *Text2Image) drawItems(canvas *image.RGBA, items []renderItem) {
	x := 0
	emojiSize := t.height
	for _, item := range items {
		if item.img != nil {
			// Draw emoji image
			dstRect := image.Rect(x, 0, x+item.width, emojiSize)
			draw.ApproxBiLinear.Scale(canvas, dstRect, item.img, item.imgBounds, imagedraw.Over, nil)
		} else if item.text != "" {
			// Draw text segment
			t.drawTextSegment(canvas, item.text, x)
		}
		x += item.width
	}
}

// encodePPM encodes an image to PPM (Portable Pixmap) P6 binary format.
func encodePPM(img *image.RGBA) []byte {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	var buf bytes.Buffer

	// PPM header (P6 format - binary)
	fmt.Fprintf(&buf, "P6\n%d %d\n255\n", width, height)

	// Write pixel data (RGB, no alpha)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			// Convert from 16-bit (0-65535) to 8-bit (0-255)
			buf.WriteByte(byte(r >> 8))
			buf.WriteByte(byte(g >> 8))
			buf.WriteByte(byte(b >> 8))
		}
	}

	return buf.Bytes()
}

// part represents a segment of the input string, either plain text or an emoji token.
type part struct {
	isEmoji bool
	value   string
}

// renderItem holds the information needed to render a single part of the message.
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

		// Add preceding text part if it exists
		if pos < fullStart {
			parts = append(parts, part{isEmoji: false, value: s[pos:fullStart]})
		}
		// Add emoji part
		parts = append(parts, part{isEmoji: true, value: s[nameStart:nameEnd]})
		pos = fullEnd
	}
	// Add trailing text part if it exists
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

// drawTextSegment draws a single string of text onto the destination image.
// It calculates the vertical position to center the text within the image's height.
func (t *Text2Image) drawTextSegment(dst imagedraw.Image, s string, x int) {
	if s == "" {
		return
	}

	// Measure the bounding box of the text to be rendered.
	// The bounds are relative to the baseline (0,0).
	// Min.Y is the ascent (usually negative), Max.Y is the descent.
	bounds, _ := font.BoundString(t.face, s)
	textHeight := (bounds.Max.Y - bounds.Min.Y).Ceil()

	// Calculate the Y position for the baseline to vertically center the text.
	// 1. (t.height - textHeight) / 2 gives the top margin.
	// 2. We subtract bounds.Min.Y (the ascent, which is negative) to move from the top of the text box to the baseline.
	// The +1 is a small adjustment that often helps with visual alignment.
	yOffset := (t.height-textHeight)/2 - bounds.Min.Y.Ceil() + 1

	d := &font.Drawer{
		Dst:  dst,
		Src:  image.NewUniform(color.White),
		Face: t.face,
		Dot:  fixed.P(x, yOffset),
	}
	d.DrawString(s)
}
