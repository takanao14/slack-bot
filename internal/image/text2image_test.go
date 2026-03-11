package image

import (
	"reflect"
	"testing"
)

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
