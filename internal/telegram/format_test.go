package telegram

import (
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestOriginDescription(t *testing.T) {
	tests := []struct {
		name   string
		origin *models.MessageOrigin
		want   string
	}{
		{
			name: "user with full name and username",
			origin: &models.MessageOrigin{
				Type:              models.MessageOriginTypeUser,
				MessageOriginUser: &models.MessageOriginUser{SenderUser: models.User{FirstName: "Иван", LastName: "Петров", Username: "ivan"}},
			},
			want: "Иван Петров \\(@ivan\\)",
		},
		{
			name: "user first name only",
			origin: &models.MessageOrigin{
				Type:              models.MessageOriginTypeUser,
				MessageOriginUser: &models.MessageOriginUser{SenderUser: models.User{FirstName: "Anna"}},
			},
			want: "Anna",
		},
		{
			name: "user username only",
			origin: &models.MessageOrigin{
				Type:              models.MessageOriginTypeUser,
				MessageOriginUser: &models.MessageOriginUser{SenderUser: models.User{Username: "solo"}},
			},
			want: "@solo",
		},
		{
			name: "hidden user",
			origin: &models.MessageOrigin{
				Type:                    models.MessageOriginTypeHiddenUser,
				MessageOriginHiddenUser: &models.MessageOriginHiddenUser{SenderUserName: "Secret Person"},
			},
			want: "Secret Person",
		},
		{
			name: "chat with title",
			origin: &models.MessageOrigin{
				Type:              models.MessageOriginTypeChat,
				MessageOriginChat: &models.MessageOriginChat{SenderChat: models.Chat{Title: "My Group"}},
			},
			want: "My Group",
		},
		{
			name: "channel with username links to message",
			origin: &models.MessageOrigin{
				Type:                 models.MessageOriginTypeChannel,
				MessageOriginChannel: &models.MessageOriginChannel{Chat: models.Chat{Title: "News", Username: "newschan"}, MessageID: 42},
			},
			want: "[News](https://t.me/newschan/42)",
		},
		{
			name: "channel without username",
			origin: &models.MessageOrigin{
				Type:                 models.MessageOriginTypeChannel,
				MessageOriginChannel: &models.MessageOriginChannel{Chat: models.Chat{Title: "Private"}},
			},
			want: "Private",
		},
		{
			name:   "unknown type",
			origin: &models.MessageOrigin{Type: models.MessageOriginType("future_type")},
			want:   "unknown source",
		},
		{
			name:   "nil origin",
			origin: nil,
			want:   "unknown source",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := originDescription(tt.origin); got != tt.want {
				t.Errorf("originDescription() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatForwarded(t *testing.T) {
	origin := &models.MessageOrigin{
		Type:              models.MessageOriginTypeUser,
		MessageOriginUser: &models.MessageOriginUser{SenderUser: models.User{FirstName: "Bob"}},
	}
	got := FormatForwarded(origin, "hello world", nil)
	want := "Forwarded from Telegram Bob:\n\nhello world"
	if got != want {
		t.Errorf("FormatForwarded() = %q, want %q", got, want)
	}
	if !strings.Contains(got, "hello world") {
		t.Error("formatted text must contain the body")
	}
}

// TestFormatForwardedEscapesAttributionAndBody covers both untrusted halves of
// the post: the attribution is flattened and escaped, and the body — which used
// to be passed through verbatim — is escaped too, so only markup rebuilt from
// entities reaches Mattermost as markup.
func TestFormatForwardedEscapesAttributionAndBody(t *testing.T) {
	origin := &models.MessageOrigin{
		Type: models.MessageOriginTypeChannel,
		MessageOriginChannel: &models.MessageOriginChannel{
			Chat:      models.Chat{Title: "[News] #1\nInjected", Username: "newschan"},
			MessageID: 42,
		},
	}
	got := FormatForwarded(origin, "**not really bold**", nil)
	want := "Forwarded from Telegram [\\[News\\] \\#1 Injected](https://t.me/newschan/42):" +
		"\n\n\\*\\*not really bold\\*\\*"
	if got != want {
		t.Fatalf("FormatForwarded() = %q, want %q", got, want)
	}
}

// TestFormatForwardedRendersEntities checks the attribution and the entity
// rendering meet in one post.
func TestFormatForwardedRendersEntities(t *testing.T) {
	origin := &models.MessageOrigin{
		Type:              models.MessageOriginTypeUser,
		MessageOriginUser: &models.MessageOriginUser{SenderUser: models.User{FirstName: "Bob"}},
	}
	entities := []models.MessageEntity{
		{Type: models.MessageEntityTypeBold, Offset: 0, Length: 6},
		{Type: models.MessageEntityTypeTextLink, Offset: 7, Length: 4, URL: "https://example.com"},
	}
	got := FormatForwarded(origin, "Срочно тута", entities)
	want := "Forwarded from Telegram Bob:\n\n**Срочно** [тута](https://example.com)"
	if got != want {
		t.Fatalf("FormatForwarded() = %q, want %q", got, want)
	}
}
