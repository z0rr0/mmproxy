package telegram

import (
	"fmt"
	"strings"

	"github.com/go-telegram/bot/models"
)

// FormatForwarded renders a forwarded message into the text posted to
// Mattermost: an attribution line naming the origin, followed by the body.
func FormatForwarded(origin *models.MessageOrigin, text string) string {
	return fmt.Sprintf("Forwarded from %s:\n\n%s", originDescription(origin), text)
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
		return userName(origin.MessageOriginUser.SenderUser)
	case models.MessageOriginTypeHiddenUser:
		if origin.MessageOriginHiddenUser == nil {
			return "hidden user"
		}
		return origin.MessageOriginHiddenUser.SenderUserName
	case models.MessageOriginTypeChat:
		if origin.MessageOriginChat == nil {
			return "unknown chat"
		}
		return chatName(origin.MessageOriginChat.SenderChat)
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
// to the exact forwarded message.
func channelLabel(o *models.MessageOriginChannel) string {
	title := chatName(o.Chat)
	if o.Chat.Username != "" {
		return fmt.Sprintf("%s (https://t.me/%s/%d)", title, o.Chat.Username, o.MessageID)
	}
	return title
}
