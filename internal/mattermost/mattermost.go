// Package mattermost wraps the official Mattermost model.Client4 with the small
// surface MMProxy needs: a startup health check and a single-channel post,
// with message truncation to the server's rune limit.
package mattermost

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

const (
	// maxMessageRunes is the Mattermost default post limit (MaxPostSize),
	// measured in runes.
	maxMessageRunes = 16383
	requestTimeout  = 10 * time.Second
)

// Client is a thin wrapper over the Mattermost REST client. It is safe for
// concurrent use: model.Client4 wraps an http.Client and the token is set once
// before any goroutine starts.
type Client struct {
	api *model.Client4
}

// New builds a client for baseURL authenticated with token (a bot access
// token).
func New(baseURL, token string) *Client {
	api := model.NewAPIv4Client(baseURL)
	api.SetToken(token)
	return &Client{api: api}
}

// Ping verifies connectivity and credentials by fetching the current user.
// GetMe requires authentication, so it validates both the URL and the token.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	me, _, err := c.api.GetMe(ctx, "")
	if err != nil {
		return fmt.Errorf("mattermost ping: %w", err)
	}
	slog.Info("mattermost connected", "username", me.Username, "user_id", me.Id)
	return nil
}

// Post publishes message to the given channel, truncating it to the server's
// rune limit first.
func (c *Client) Post(ctx context.Context, channelID, message string) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	post := &model.Post{
		ChannelId: channelID,
		Message:   Truncate(message, maxMessageRunes),
	}
	if _, _, err := c.api.CreatePost(ctx, post); err != nil {
		return fmt.Errorf("mattermost create post: %w", err)
	}
	return nil
}

// Truncate shortens s to at most limit runes (not bytes), appending an ellipsis
// when it cuts. It is safe for multibyte input.
func Truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}
