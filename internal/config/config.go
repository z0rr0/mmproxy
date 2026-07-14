// Package config loads and validates the MMProxy service configuration from a
// TOML file. It exposes derived helpers (allowlist maps, per-source channel
// normalization) so that consumers do not have to reason about fallbacks.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

const defaultAddr = ":8080"

// Config is the root configuration structure parsed from TOML.
type Config struct {
	Base       Base       `toml:"base"`
	Mattermost Mattermost `toml:"mattermost"`
	Telegram   Telegram   `toml:"telegram"`
	Miniflux   Miniflux   `toml:"miniflux"`
}

// Base holds general HTTP server settings.
type Base struct {
	Addr  string `toml:"addr"`
	Debug bool   `toml:"debug"`
}

// Mattermost holds the target Mattermost connection and default channel.
type Mattermost struct {
	URL       string `toml:"url"`
	Token     string `toml:"token"`
	ChannelID string `toml:"channel_id"`
}

// Telegram holds the Telegram bot source configuration. An empty Token disables
// the source. AllowedIDs is derived from AllowedUsers during validation.
type Telegram struct {
	Token        string             `toml:"token"`
	AllowedUsers []int64            `toml:"allowed_users"`
	ChannelID    string             `toml:"channel_id"`
	AllowedIDs   map[int64]struct{} `toml:"-"`
}

// Miniflux holds the Miniflux webhook source configuration. An empty
// WebhookSecret disables the source. AllowedFeeds is derived from FeedIDs.
type Miniflux struct {
	WebhookSecret string             `toml:"webhook_secret"`
	FeedIDs       []int64            `toml:"feed_ids"`
	ChannelID     string             `toml:"channel_id"`
	AllowedFeeds  map[int64]struct{} `toml:"-"`
}

// Enabled reports whether the Telegram source is active.
func (t *Telegram) Enabled() bool { return t.Token != "" }

// Enabled reports whether the Miniflux source is active.
func (m *Miniflux) Enabled() bool { return m.WebhookSecret != "" }

// Load reads, parses and validates the configuration file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	cfg := new(Config)
	if err = toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	cfg.applyDefaults()
	if err = cfg.validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Base.Addr == "" {
		c.Base.Addr = defaultAddr
	}
}

// validate checks every section, accumulating all problems via errors.Join, and
// then normalizes derived fields (allowlist maps, per-source channels).
func (c *Config) validate() error {
	err := errors.Join(
		c.Mattermost.validate(),
		c.Telegram.validate(),
		c.Miniflux.validate(),
		c.crossValidate(),
	)
	if err != nil {
		return err
	}
	c.normalize()
	return nil
}

func (m *Mattermost) validate() error {
	var errs []error
	if m.URL == "" {
		errs = append(errs, errors.New("mattermost: url is required"))
	} else if u, err := url.Parse(m.URL); err != nil {
		errs = append(errs, fmt.Errorf("mattermost: invalid url: %w", err))
	} else if u.Scheme != "http" && u.Scheme != "https" {
		errs = append(errs, fmt.Errorf("mattermost: url scheme must be http or https, got %q", u.Scheme))
	} else if u.Host == "" {
		errs = append(errs, errors.New("mattermost: url host is required"))
	}
	if m.Token == "" {
		errs = append(errs, errors.New("mattermost: token is required"))
	}
	if m.ChannelID == "" {
		errs = append(errs, errors.New("mattermost: channel_id is required"))
	}
	return errors.Join(errs...)
}

func (t *Telegram) validate() error {
	if !t.Enabled() {
		return nil
	}
	var errs []error
	if len(t.AllowedUsers) == 0 {
		errs = append(errs, errors.New("telegram: allowed_users must not be empty when token is set"))
	}
	for _, id := range t.AllowedUsers {
		if id <= 0 {
			errs = append(errs, fmt.Errorf("telegram: allowed user id must be positive, got %d", id))
		}
	}
	return errors.Join(errs...)
}

func (m *Miniflux) validate() error {
	if !m.Enabled() {
		return nil
	}
	var errs []error
	for _, id := range m.FeedIDs {
		if id <= 0 {
			errs = append(errs, fmt.Errorf("miniflux: feed id must be positive, got %d", id))
		}
	}
	return errors.Join(errs...)
}

func (c *Config) crossValidate() error {
	if !c.Telegram.Enabled() && !c.Miniflux.Enabled() {
		return errors.New("at least one source (telegram or miniflux) must be enabled")
	}
	return nil
}

// normalize builds derived allowlist maps and resolves empty per-source channels
// to the shared Mattermost channel. Runs only after validation succeeds.
func (c *Config) normalize() {
	c.Telegram.AllowedIDs = sliceToSet(c.Telegram.AllowedUsers)
	c.Miniflux.AllowedFeeds = sliceToSet(c.Miniflux.FeedIDs)

	if c.Telegram.ChannelID == "" {
		c.Telegram.ChannelID = c.Mattermost.ChannelID
	}
	if c.Miniflux.ChannelID == "" {
		c.Miniflux.ChannelID = c.Mattermost.ChannelID
	}
}

func sliceToSet(ids []int64) map[int64]struct{} {
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}
