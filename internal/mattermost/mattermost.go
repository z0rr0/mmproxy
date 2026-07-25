// Package mattermost implements the small subset of the Mattermost REST API
// MMProxy needs: a startup health check and creation of channel posts.
package mattermost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// maxMessageRunes is the Mattermost default post limit (MaxPostSize),
	// measured in runes.
	maxMessageRunes = 16383
	// defaultTimeout is the fallback when New receives a non-positive timeout.
	defaultTimeout   = 10 * time.Second
	maxResponseBytes = 64 << 10
)

// Client is a concurrency-safe Mattermost API client.
type Client struct {
	baseURL    *url.URL
	token      string
	timeout    time.Duration
	httpClient *http.Client
}

// New builds a client for baseURL authenticated with a bot or personal access
// token. The base URL must identify the Mattermost site, without /api/v4. The
// timeout bounds each API call; a non-positive value falls back to
// defaultTimeout, since a zero deadline would fail every request.
func New(baseURL, token string, timeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse mattermost URL: %w", err)
	}

	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("mattermost URL must have an http or https scheme and host")
	}

	if timeout <= 0 {
		timeout = defaultTimeout
	}

	return &Client{
		baseURL:    parsed,
		token:      token,
		timeout:    timeout,
		httpClient: &http.Client{},
	}, nil
}

// Ping verifies connectivity and credentials by fetching the current user.
func (c *Client) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req, err := c.newRequest(ctx, http.MethodGet, "/api/v4/users/me", nil)
	if err != nil {
		return fmt.Errorf("mattermost ping: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mattermost ping: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err = responseError(resp); err != nil {
		return fmt.Errorf("mattermost ping: %w", err)
	}
	var me struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err = decodeLimited(resp.Body, &me); err != nil {
		return fmt.Errorf("mattermost ping response: %w", err)
	}
	if me.ID == "" {
		return errors.New("mattermost ping response: user id is empty")
	}
	slog.Info("mattermost connected", "username", me.Username, "user_id", me.ID)
	return nil
}

// Post publishes message to channelID, truncating it to the server's rune
// limit first.
func (c *Client) Post(ctx context.Context, channelID, message string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	body, err := json.Marshal(struct {
		ChannelID string `json:"channel_id"`
		Message   string `json:"message"`
	}{
		ChannelID: channelID,
		Message:   Truncate(message, maxMessageRunes),
	})
	if err != nil {
		return fmt.Errorf("marshal mattermost post: %w", err)
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/api/v4/posts", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mattermost create post: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mattermost create post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err = responseError(resp); err != nil {
		return fmt.Errorf("mattermost create post: %w", err)
	}
	if _, err = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes)); err != nil {
		slog.Error("mattermost io copy", "error", err)
	}
	return nil
}

func (c *Client) newRequest(ctx context.Context, method, apiPath string, body io.Reader) (*http.Request, error) {
	u := *c.baseURL
	u.Path = strings.TrimRight(u.Path, "/") + apiPath
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""

	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return req, nil
}

func responseError(resp *http.Response) error {
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("mattermost returned %s; read error response: %w", resp.Status, err)
	}

	var apiErr struct {
		Message string `json:"message"`
	}

	if json.Unmarshal(body, &apiErr) == nil && apiErr.Message != "" {
		return fmt.Errorf("mattermost returned %s: %s", resp.Status, apiErr.Message)
	}

	return fmt.Errorf("mattermost returned %s", resp.Status)
}

func decodeLimited(r io.Reader, dst any) error {
	limited := &io.LimitedReader{R: r, N: maxResponseBytes + 1}
	if err := json.NewDecoder(limited).Decode(dst); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	if limited.N <= 0 {
		return errors.New("response body is too large")
	}
	return nil
}

// Truncate shortens s to at most limit runes (not bytes), appending an ellipsis
// when it cuts. It is safe for multibyte input.
func Truncate(s string, limit int) string {
	if limit <= 0 {
		return ""
	}

	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}

	if limit <= 1 {
		return string(runes[:limit])
	}

	return string(runes[:limit-1]) + "…"
}
