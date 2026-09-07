package image

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/goregular"
)

func newTestText2Image(t *testing.T) *Text2Image {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.ttf")
	if err := os.WriteFile(path, goregular.TTF, 0o600); err != nil {
		t.Fatalf("failed to write test font: %v", err)
	}

	ti, err := NewText2Image(path, 32, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("failed to create Text2Image: %v", err)
	}
	t.Cleanup(func() { _ = ti.Close() })
	return ti
}

// ppmSize reads the P6 header the renderer writes.
func ppmSize(t *testing.T, data []byte) (int, int) {
	t.Helper()

	var magic string
	var width, height, maxVal int
	if _, err := fmt.Sscan(string(data[:min(64, len(data))]), &magic, &width, &height, &maxVal); err != nil {
		t.Fatalf("failed to parse PPM header: %v", err)
	}
	if magic != "P6" {
		t.Fatalf("expected P6 magic, got %q", magic)
	}
	return width, height
}

func TestSplitEmojiParts(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []part
	}{
		{
			name:  "No emoji",
			input: "Hello World",
			want: []part{
				{isEmoji: false, value: "Hello World"},
			},
		},
		{
			name:  "Single emoji only",
			input: ":wave:",
			want: []part{
				{isEmoji: true, value: "wave"},
			},
		},
		{
			name:  "Text with emoji in middle",
			input: "Hello :wave: World",
			want: []part{
				{isEmoji: false, value: "Hello "},
				{isEmoji: true, value: "wave"},
				{isEmoji: false, value: " World"},
			},
		},
		{
			name:  "Multiple emojis",
			input: ":wave::smile:",
			want: []part{
				{isEmoji: true, value: "wave"},
				{isEmoji: true, value: "smile"},
			},
		},
		{
			name:  "Text with mixed content",
			input: "A :b: C :d:",
			want: []part{
				{isEmoji: false, value: "A "},
				{isEmoji: true, value: "b"},
				{isEmoji: false, value: " C "},
				{isEmoji: true, value: "d"},
			},
		},
		{
			name:  "Invalid emoji format ignored",
			input: "Hello : invalid : World",
			want: []part{
				{isEmoji: false, value: "Hello : invalid : World"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitEmojiParts(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitEmojiParts() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A Slack message can reach about 40,000 characters. Without a bound the canvas
// alone would be tens of megabytes and the PPM would exceed the gRPC server's
// 4 MB default receive limit.
func TestRenderTextWithEmojiBoundsLongMessages(t *testing.T) {
	ti := newTestText2Image(t)

	data, err := ti.RenderTextWithEmoji(strings.Repeat("あ", 40000), nil)
	if err != nil {
		t.Fatalf("expected render to succeed, got %v", err)
	}

	width, height := ppmSize(t, data)
	if height != 32 {
		t.Fatalf("expected height 32, got %d", height)
	}
	// maxTextRunes glyphs at most as wide as the height, plus the ellipsis.
	if maxWidth := (maxTextRunes+1)*32 + trailingPadding; width > maxWidth {
		t.Fatalf("expected width below %d, got %d", maxWidth, width)
	}
	if len(data) > 1<<20 {
		t.Fatalf("expected PPM below 1 MiB, got %d bytes", len(data))
	}
}

func TestRenderTextWithEmojiKeepsShortMessagesIntact(t *testing.T) {
	ti := newTestText2Image(t)

	short, err := ti.RenderTextWithEmoji("hello", nil)
	if err != nil {
		t.Fatalf("expected render to succeed, got %v", err)
	}
	longer, err := ti.RenderTextWithEmoji("hello world, this is a longer message", nil)
	if err != nil {
		t.Fatalf("expected render to succeed, got %v", err)
	}

	shortWidth, _ := ppmSize(t, short)
	longerWidth, _ := ppmSize(t, longer)
	if shortWidth >= longerWidth {
		t.Fatalf("expected the longer message to be wider, got %d and %d", shortWidth, longerWidth)
	}
}

// Truncation counts runes, so multi-byte text is never split mid-character.
func TestRenderTextWithEmojiTruncatesOnRuneBoundaries(t *testing.T) {
	ti := newTestText2Image(t)

	data, err := ti.RenderTextWithEmoji(strings.Repeat("日本語", 500), nil)
	if err != nil {
		t.Fatalf("expected render to succeed, got %v", err)
	}
	if _, height := ppmSize(t, data); height != 32 {
		t.Fatalf("expected height 32, got %d", height)
	}
}

func TestRenderTextWithEmojiRendersPlaceholderForEmptyInput(t *testing.T) {
	ti := newTestText2Image(t)

	for _, input := range []string{"", "   ", "\n\n"} {
		data, err := ti.RenderTextWithEmoji(input, nil)
		if err != nil {
			t.Fatalf("expected render to succeed for %q, got %v", input, err)
		}
		if width, _ := ppmSize(t, data); width <= trailingPadding {
			t.Fatalf("expected placeholder text to be rendered for %q, got width %d", input, width)
		}
	}
}

// Pins the wire format, which the LED service and parsePPMSize both depend on.
func TestEncodePPM(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{G: 255, A: 255})

	got := encodePPM(img)
	want := append([]byte("P6\n2 1\n255\n"), 255, 0, 0, 0, 255, 0)

	if !bytes.Equal(got, want) {
		t.Fatalf("encodePPM() = %v, want %v", got, want)
	}
}

// A non-zero origin must not shift the pixel rows.
func TestEncodePPMHonoursBoundsOrigin(t *testing.T) {
	img := image.NewRGBA(image.Rect(3, 5, 5, 6))
	img.Set(3, 5, color.RGBA{B: 255, A: 255})
	img.Set(4, 5, color.RGBA{R: 255, G: 255, B: 255, A: 255})

	got := encodePPM(img)
	want := append([]byte("P6\n2 1\n255\n"), 0, 0, 255, 255, 255, 255)

	if !bytes.Equal(got, want) {
		t.Fatalf("encodePPM() = %v, want %v", got, want)
	}
}
