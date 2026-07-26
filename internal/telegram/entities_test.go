package telegram

import (
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

// entity is a shorthand for the table below.
func entity(t models.MessageEntityType, offset, length int) models.MessageEntity {
	return models.MessageEntity{Type: t, Offset: offset, Length: length}
}

func TestRenderEntities(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		entities []models.MessageEntity
		want     string
	}{
		{
			name: "no entities escapes the body",
			text: "смотри *звёздочки* и [скобки]",
			want: "смотри \\*звёздочки\\* и \\[скобки\\]",
		},
		{
			name: "nil entities on empty text",
			text: "",
			want: "",
		},
		{
			name:     "bold",
			text:     "very important",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeBold, 5, 9)},
			want:     "very **important**",
		},
		{
			name:     "italic",
			text:     "very important",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeItalic, 5, 9)},
			want:     "very *important*",
		},
		{
			name:     "strikethrough uses the double tilde Mattermost expects",
			text:     "gone forever",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeStrikethrough, 0, 4)},
			want:     "~~gone~~ forever",
		},
		{
			// The whole point of the change: this URL exists only in the entity.
			name: "text link",
			text: "открой сайт",
			entities: []models.MessageEntity{{
				Type: models.MessageEntityTypeTextLink, Offset: 7, Length: 4, URL: "https://example.com/x",
			}},
			want: "открой [сайт](https://example.com/x)",
		},
		{
			name:     "code content is not escaped",
			text:     "run *ls* now",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeCode, 4, 4)},
			want:     "run `*ls*` now",
		},
		{
			name:     "code containing backticks widens the fence",
			text:     "a`b",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeCode, 0, 3)},
			want:     "``a`b``",
		},
		{
			name:     "code starting with a backtick is padded",
			text:     "`x",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeCode, 0, 2)},
			want:     "`` `x ``",
		},
		{
			name: "pre with language",
			text: "код:\nfmt.Println()",
			entities: []models.MessageEntity{{
				Type: models.MessageEntityTypePre, Offset: 5, Length: 13, Language: "go",
			}},
			want: "код:\n```go\nfmt.Println()\n```",
		},
		{
			name:     "pre without language starts on its own line",
			text:     "код: x := 1",
			entities: []models.MessageEntity{entity(models.MessageEntityTypePre, 5, 6)},
			want:     "код: \n```\nx := 1\n```",
		},
		{
			name:     "text after a block gets its own line",
			text:     "AB",
			entities: []models.MessageEntity{entity(models.MessageEntityTypePre, 0, 1)},
			want:     "```\nA\n```\nB",
		},
		{
			name:     "hostile language tag is stripped",
			text:     "x",
			entities: []models.MessageEntity{{Type: models.MessageEntityTypePre, Offset: 0, Length: 1, Language: "go\n```evil"}},
			want:     "```go\nx\n```",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := renderEntities(tt.text, tt.entities); got != tt.want {
				t.Errorf("renderEntities() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderEntitiesStructure covers offset arithmetic, nesting and the
// malformed input that has to be discarded rather than trusted.
func TestRenderEntitiesStructure(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		entities []models.MessageEntity
		want     string
	}{
		{
			// Offsets are UTF-16 code units: the emoji takes two of them, so a
			// rune-indexed implementation would highlight the wrong substring.
			name:     "offsets are counted in UTF-16 code units",
			text:     "🙂 bold tail",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeBold, 3, 4)},
			want:     "🙂 **bold** tail",
		},
		{
			name:     "entity cutting a surrogate pair is dropped",
			text:     "🙂",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeBold, 0, 1)},
			want:     "🙂",
		},
		{
			name: "bold nested in a link",
			text: "click here",
			entities: []models.MessageEntity{
				{Type: models.MessageEntityTypeTextLink, Offset: 0, Length: 10, URL: "https://example.com"},
				entity(models.MessageEntityTypeBold, 0, 5),
			},
			want: "[**click** here](https://example.com)",
		},
		{
			name: "italic nested in bold",
			text: "bold italic",
			entities: []models.MessageEntity{
				entity(models.MessageEntityTypeBold, 0, 11),
				entity(models.MessageEntityTypeItalic, 5, 6),
			},
			want: "**bold *italic***",
		},
		{
			name: "nested link renders as plain label",
			text: "outer inner",
			entities: []models.MessageEntity{
				{Type: models.MessageEntityTypeTextLink, Offset: 0, Length: 11, URL: "https://outer.example"},
				{Type: models.MessageEntityTypeTextLink, Offset: 6, Length: 5, URL: "https://inner.example"},
			},
			want: "[outer inner](https://outer.example)",
		},
		{
			name:     "surrounding spaces move outside the emphasis",
			text:     "hi there",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeBold, 2, 6)},
			want:     "hi **there**",
		},
		{
			name:     "whitespace-only emphasis is left alone",
			text:     "a b",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeBold, 1, 1)},
			want:     "a b",
		},
		{
			name: "unsupported entities render as escaped text",
			text: "under spoil quote @user #tag",
			entities: []models.MessageEntity{
				entity(models.MessageEntityTypeUnderline, 0, 5),
				entity(models.MessageEntityTypeSpoiler, 6, 5),
				entity(models.MessageEntityTypeBlockquote, 12, 5),
				entity(models.MessageEntityTypeMention, 18, 5),
				entity(models.MessageEntityTypeHashtag, 24, 4),
			},
			want: "under spoil quote @user \\#tag",
		},
		{
			name:     "entity starting inside a surrogate pair is dropped",
			text:     "a🙂",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeBold, 2, 1)},
			want:     "a🙂",
		},
		{
			name: "entity nested in code is ignored, content stays verbatim",
			text: "code x",
			entities: []models.MessageEntity{
				entity(models.MessageEntityTypePre, 0, 6),
				entity(models.MessageEntityTypeBold, 0, 4),
			},
			want: "```\ncode x\n```",
		},
		{
			name: "partially overlapping entity is dropped",
			text: "abcdefgh",
			entities: []models.MessageEntity{
				entity(models.MessageEntityTypeBold, 0, 5),
				entity(models.MessageEntityTypeItalic, 3, 5),
			},
			want: "**abcde**fgh",
		},
		{
			name:     "entity beyond the end of the text is dropped",
			text:     "short",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeBold, 100, 4)},
			want:     "short",
		},
		{
			name:     "entity overrunning the text is dropped",
			text:     "short",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeBold, 3, 40)},
			want:     "short",
		},
		{
			name:     "zero length entity is dropped",
			text:     "short",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeBold, 2, 0)},
			want:     "short",
		},
		{
			name:     "negative offset is dropped",
			text:     "short",
			entities: []models.MessageEntity{entity(models.MessageEntityTypeBold, -1, 3)},
			want:     "short",
		},
		{
			name: "line breaks in the body survive",
			text: "first\nsecond",
			entities: []models.MessageEntity{
				entity(models.MessageEntityTypeBold, 6, 6),
			},
			want: "first\n**second**",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := renderEntities(tt.text, tt.entities); got != tt.want {
				t.Errorf("renderEntities() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderEntitiesLinkTargets(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "https is linked",
			url:  "https://example.com/a",
			want: "[label](https://example.com/a)",
		},
		{
			name: "http is linked",
			url:  "http://example.com",
			want: "[label](http://example.com)",
		},
		{
			name: "telegram deep link is kept",
			url:  "tg://user?id=42",
			want: "[label](tg://user?id=42)",
		},
		{
			name: "mailto is kept",
			url:  "mailto:me@example.com",
			want: "[label](mailto:me@example.com)",
		},
		{
			name: "javascript scheme is refused",
			url:  "javascript:alert(1)",
			want: "label",
		},
		{
			name: "data scheme is refused",
			url:  "data:text/html;base64,PHNjcmlwdD4=",
			want: "label",
		},
		{
			name: "schemeless target is refused",
			url:  "example.com/no-scheme",
			want: "label",
		},
		{
			name: "empty target is refused",
			url:  "",
			want: "label",
		},
		{
			name: "unparsable target is refused",
			url:  "https://exa mple.com/%zz",
			want: "label",
		},
		{
			name: "parentheses cannot close the link early",
			url:  "https://example.com/a(b)c",
			want: "[label](https://example.com/a%28b%29c)",
		},
		{
			// url.Parse refuses control characters, so the target is dropped
			// rather than silently repaired into a different URL.
			name: "control characters are refused",
			url:  "https://example.com/a\nb",
			want: "label",
		},
		{
			name: "a space in the path cannot end the link early",
			url:  "https://example.com/a b",
			want: "[label](https://example.com/a%20b)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entities := []models.MessageEntity{{
				Type: models.MessageEntityTypeTextLink, Offset: 0, Length: 5, URL: tt.url,
			}}
			if got := renderEntities("label", entities); got != tt.want {
				t.Errorf("renderEntities() = %q, want %q", got, tt.want)
			}
		})
	}
}

// FuzzRenderEntities checks that no combination of body and entity bounds can
// panic: offsets arrive from a forwarded message and index into the text.
func FuzzRenderEntities(f *testing.F) {
	f.Add("hello world", 0, 5, "https://example.com")
	f.Add("🙂 emoji tail", 1, 3, "javascript:alert(1)")
	f.Add("", -1, 0, "")
	f.Add("многобайтный текст", 4, 100, "tg://user?id=1")
	f.Add("a`b```c", 0, 7, "https://e.com/a(b)")

	types := []models.MessageEntityType{
		models.MessageEntityTypeBold,
		models.MessageEntityTypeItalic,
		models.MessageEntityTypeStrikethrough,
		models.MessageEntityTypeCode,
		models.MessageEntityTypePre,
		models.MessageEntityTypeTextLink,
	}
	f.Fuzz(func(t *testing.T, text string, offset, length int, rawURL string) {
		for _, entityType := range types {
			got := renderEntities(text, []models.MessageEntity{{
				Type: entityType, Offset: offset, Length: length, URL: rawURL, Language: rawURL,
			}})
			if text == "" && got != "" {
				t.Errorf("renderEntities(%q) = %q, want empty", text, got)
			}
		}
	})
}

// TestRenderEntitiesNoMarkdownInjection pins the security property the escaping
// buys: markup typed into the source message stays visible text and cannot
// restructure the post.
func TestRenderEntitiesNoMarkdownInjection(t *testing.T) {
	text := "#### fake heading\n[fake](https://evil.example) **loud**"
	got := renderEntities(text, nil)

	for _, unescaped := range []string{"#### ", "[fake]", "**loud**"} {
		if strings.Contains(got, unescaped) {
			t.Errorf("renderEntities() = %q, must not contain raw %q", got, unescaped)
		}
	}
	if !strings.Contains(got, "fake heading") {
		t.Errorf("renderEntities() = %q, must keep the visible text", got)
	}
}
