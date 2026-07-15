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
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	defaultAddr              = ":8080"
	defaultReadTimeout       = 10 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
	defaultMattermostTimeout = 10 * time.Second
)

// Config is the root configuration structure parsed from TOML.
type Config struct {
	Base       Base       `toml:"base"`
	Mattermost Mattermost `toml:"mattermost"`
	Telegram   Telegram   `toml:"telegram"`
	Miniflux   Miniflux   `toml:"miniflux"`
}

// Base holds general HTTP server settings. Every timeout defaults to a sane
// value when omitted or set to zero.
type Base struct {
	Addr  string `toml:"addr"`
	Debug bool   `toml:"debug"`
	// ReadTimeout limits reading an entire request, including its body.
	ReadTimeout Duration `toml:"read_timeout"`
	// ReadHeaderTimeout limits reading the request headers.
	ReadHeaderTimeout Duration `toml:"read_header_timeout"`
	// WriteTimeout limits writing a response. It must exceed Mattermost.Timeout
	// because the Miniflux webhook posts to Mattermost synchronously.
	WriteTimeout Duration `toml:"write_timeout"`
	// IdleTimeout limits how long keep-alive connections stay open.
	IdleTimeout Duration `toml:"idle_timeout"`
	// ShutdownTimeout is the graceful shutdown deadline shared by the HTTP
	// server drain and the Telegram source.
	ShutdownTimeout Duration `toml:"shutdown_timeout"`
}

// Mattermost holds the target Mattermost connection and default channel.
type Mattermost struct {
	URL       string `toml:"url"`
	Token     string `toml:"token"`
	ChannelID string `toml:"channel_id"`
	// Timeout is the per-request deadline for Mattermost API calls.
	Timeout Duration `toml:"timeout"`
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

// applyDefaults fills in unset fields. A zero duration means "not set", so an
// explicit "0s" also resolves to the default: there is no way to disable a
// timeout.
func (c *Config) applyDefaults() {
	if c.Base.Addr == "" {
		c.Base.Addr = defaultAddr
	}
	if c.Base.ReadTimeout == 0 {
		c.Base.ReadTimeout = Duration(defaultReadTimeout)
	}
	if c.Base.ReadHeaderTimeout == 0 {
		c.Base.ReadHeaderTimeout = Duration(defaultReadHeaderTimeout)
	}
	if c.Base.WriteTimeout == 0 {
		c.Base.WriteTimeout = Duration(defaultWriteTimeout)
	}
	if c.Base.IdleTimeout == 0 {
		c.Base.IdleTimeout = Duration(defaultIdleTimeout)
	}
	if c.Base.ShutdownTimeout == 0 {
		c.Base.ShutdownTimeout = Duration(defaultShutdownTimeout)
	}
	if c.Mattermost.Timeout == 0 {
		c.Mattermost.Timeout = Duration(defaultMattermostTimeout)
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
	var errs []error
	if !c.Telegram.Enabled() && !c.Miniflux.Enabled() {
		errs = append(errs, errors.New("at least one source (telegram or miniflux) must be enabled"))
	}
	// The Miniflux webhook posts to Mattermost synchronously, so a response cut
	// short by write_timeout would abandon a request that is still in flight.
	if c.Base.WriteTimeout <= c.Mattermost.Timeout {
		errs = append(errs, fmt.Errorf("base: write_timeout (%s) must exceed mattermost timeout (%s)",
			c.Base.WriteTimeout, c.Mattermost.Timeout))
	}
	return errors.Join(errs...)
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
