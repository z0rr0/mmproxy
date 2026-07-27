package telegram

import (
	"fmt"
	"strings"

	"github.com/go-telegram/bot/models"
	"github.com/z0rr0/mmproxy/internal/markdown"
)

// FormatForwarded renders a forwarded message into the text posted to
// Mattermost: an attribution line naming the origin, followed by the body with
// its Telegram formatting rebuilt from entities.
func FormatForwarded(origin *models.MessageOrigin, text string, entities []models.MessageEntity) string {
	return fmt.Sprintf("Forwarded from Telegram %s:\n\n%s", originDescription(origin), renderEntities(text, entities))
}

// originDescription produces a human-readable source label for every
// MessageOrigin variant. A default branch guards against future API types.
func originDescription(origin *models.MessageOrigin) string {
	if origin == nil {
		return "unknown source"
	}
	switch origin.Type {
	case models.MessageOriginTypeUser:
		if origin.MessageOriginUser == nil {
			return "unknown user"
		}
		return markdown.EscapeText(userName(origin.MessageOriginUser.SenderUser))
	case models.MessageOriginTypeHiddenUser:
		if origin.MessageOriginHiddenUser == nil {
			return "hidden user"
		}
		return markdown.EscapeText(origin.MessageOriginHiddenUser.SenderUserName)
	case models.MessageOriginTypeChat:
		if origin.MessageOriginChat == nil {
			return "unknown chat"
		}
		return markdown.EscapeText(chatName(origin.MessageOriginChat.SenderChat))
	case models.MessageOriginTypeChannel:
		if origin.MessageOriginChannel == nil {
			return "unknown channel"
		}
		return channelLabel(origin.MessageOriginChannel)
	default:
		return "unknown source"
	}
}

// userName formats a user as "First Last (@username)", omitting empty parts.
func userName(u models.User) string {
	name := strings.TrimSpace(u.FirstName + " " + u.LastName)
	switch {
	case name != "" && u.Username != "":
		return fmt.Sprintf("%s (@%s)", name, u.Username)
	case name != "":
		return name
	case u.Username != "":
		return "@" + u.Username
	default:
		return "unknown user"
	}
}

// chatName prefers the chat title, falling back to its @username.
func chatName(c models.Chat) string {
	if c.Title != "" {
		return c.Title
	}
	if c.Username != "" {
		return "@" + c.Username
	}
	return "unknown chat"
}

// channelLabel names a source channel and, when it has a public username, links
// to the exact forwarded message. The title is escaped, so it cannot break out
// of the link label; the URL goes through the same sanitizer as message links.
func channelLabel(o *models.MessageOriginChannel) string {
	title := markdown.EscapeText(chatName(o.Chat))
	if o.Chat.Username == "" {
		return title
	}
	target, ok := safeURL(fmt.Sprintf("https://t.me/%s/%d", o.Chat.Username, o.MessageID))
	if !ok {
		return title
	}
	return fmt.Sprintf("[%s](%s)", title, target)
}
