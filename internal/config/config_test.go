package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

const validConfig = `
[base]
debug = true

[mattermost]
url = "https://mm.example.com"
token = "mm-token"
channel_id = "shared-channel"

[telegram]
token = "tg-token"
allowed_users = [111, 222]

[miniflux]
webhook_secret = "secret"
feed_ids = [1, 2]
`

func TestLoadValid(t *testing.T) {
	cfg, err := Load(writeConfig(t, validConfig))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Base.Addr != defaultAddr {
		t.Errorf("addr = %q, want default %q", cfg.Base.Addr, defaultAddr)
	}
	if !cfg.Base.Debug {
		t.Error("debug should be true")
	}
	if !cfg.Telegram.Enabled() || !cfg.Miniflux.Enabled() {
		t.Error("both sources should be enabled")
	}
	if _, ok := cfg.Telegram.AllowedIDs[111]; !ok {
		t.Error("AllowedIDs should contain 111")
	}
	if _, ok := cfg.Miniflux.AllowedFeeds[2]; !ok {
		t.Error("AllowedFeeds should contain 2")
	}
	// Empty per-source channels fall back to the shared one.
	if cfg.Telegram.ChannelID != "shared-channel" {
		t.Errorf("telegram channel = %q, want shared-channel", cfg.Telegram.ChannelID)
	}
	if cfg.Miniflux.ChannelID != "shared-channel" {
		t.Errorf("miniflux channel = %q, want shared-channel", cfg.Miniflux.ChannelID)
	}
	// Omitted timeouts fall back to the defaults.
	defaults := []struct {
		name string
		got  Duration
		want time.Duration
	}{
		{"read_timeout", cfg.Base.ReadTimeout, defaultReadTimeout},
		{"read_header_timeout", cfg.Base.ReadHeaderTimeout, defaultReadHeaderTimeout},
		{"write_timeout", cfg.Base.WriteTimeout, defaultWriteTimeout},
		{"idle_timeout", cfg.Base.IdleTimeout, defaultIdleTimeout},
		{"shutdown_timeout", cfg.Base.ShutdownTimeout, defaultShutdownTimeout},
		{"mattermost timeout", cfg.Mattermost.Timeout, defaultMattermostTimeout},
	}
	for _, d := range defaults {
		if d.got.Timed() != d.want {
			t.Errorf("%s = %v, want default %v", d.name, d.got, d.want)
		}
	}
}

func TestLoadTimeouts(t *testing.T) {
	content := `
[base]
read_timeout = "15s"
read_header_timeout = "2s"
write_timeout = "45s"
idle_timeout = "0s"
shutdown_timeout = "20s"

[mattermost]
url = "https://mm.example.com"
token = "mm-token"
channel_id = "c"
timeout = "8s"

[telegram]
token = "tg-token"
allowed_users = [1]
`
	cfg, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tests := []struct {
		name string
		got  Duration
		want time.Duration
	}{
		{"read_timeout", cfg.Base.ReadTimeout, 15 * time.Second},
		{"read_header_timeout", cfg.Base.ReadHeaderTimeout, 2 * time.Second},
		{"write_timeout", cfg.Base.WriteTimeout, 45 * time.Second},
		// An explicit zero is indistinguishable from an omitted key.
		{"idle_timeout", cfg.Base.IdleTimeout, defaultIdleTimeout},
		{"shutdown_timeout", cfg.Base.ShutdownTimeout, 20 * time.Second},
		{"mattermost timeout", cfg.Mattermost.Timeout, 8 * time.Second},
	}
	for _, tt := range tests {
		if tt.got.Timed() != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestLoadDurationErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "invalid duration",
			content: `
[base]
read_timeout = "banana"
`,
			want: "failed to parse duration",
		},
		{
			name: "negative duration",
			content: `
[mattermost]
timeout = "-5s"
`,
			want: "duration cannot be negative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.content))
			if err == nil {
				t.Fatal("expected duration error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadChannelOverride(t *testing.T) {
	content := `
[mattermost]
url = "https://mm.example.com"
token = "mm-token"
channel_id = "shared"

[telegram]
token = "tg-token"
allowed_users = [1]
channel_id = "tg-channel"

[miniflux]
webhook_secret = "secret"
`
	cfg, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.ChannelID != "tg-channel" {
		t.Errorf("telegram channel = %q, want tg-channel", cfg.Telegram.ChannelID)
	}
	if cfg.Miniflux.ChannelID != "shared" {
		t.Errorf("miniflux channel = %q, want shared (fallback)", cfg.Miniflux.ChannelID)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadBadTOML(t *testing.T) {
	_, err := Load(writeConfig(t, "this is = not = valid toml"))
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	content := validConfig + `
[base.extra]
enabled = true
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for unknown TOML field")
	}
	if !strings.Contains(err.Error(), "strict mode") {
		t.Errorf("error = %q, want strict-mode unknown field error", err)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "missing mattermost fields",
			content: `
[telegram]
token = "tg"
allowed_users = [1]
`,
			want: "mattermost: url is required",
		},
		{
			name: "bad url scheme",
			content: `
[mattermost]
url = "ftp://mm.example.com"
token = "t"
channel_id = "c"
[telegram]
token = "tg"
allowed_users = [1]
`,
			want: "url scheme must be http or https",
		},
		{
			name: "url without host",
			content: `
[mattermost]
url = "https:"
token = "t"
channel_id = "c"
[telegram]
token = "tg"
allowed_users = [1]
`,
			want: "url host is required",
		},
		{
			name: "no sources enabled",
			content: `
[mattermost]
url = "https://mm.example.com"
token = "t"
channel_id = "c"
`,
			want: "at least one source",
		},
		{
			name: "telegram without allowed_users",
			content: `
[mattermost]
url = "https://mm.example.com"
token = "t"
channel_id = "c"
[telegram]
token = "tg"
`,
			want: "allowed_users must not be empty",
		},
		{
			name: "negative feed id",
			content: `
[mattermost]
url = "https://mm.example.com"
token = "t"
channel_id = "c"
[miniflux]
webhook_secret = "s"
feed_ids = [-5]
`,
			want: "feed id must be positive",
		},
		{
			name: "write timeout below mattermost timeout",
			content: `
[base]
write_timeout = "5s"
[mattermost]
url = "https://mm.example.com"
token = "t"
channel_id = "c"
[telegram]
token = "tg"
allowed_users = [1]
`,
			want: "write_timeout (5s) must exceed mattermost timeout (10s)",
		},
		{
			name: "write timeout equal to mattermost timeout",
			content: `
[base]
write_timeout = "10s"
[mattermost]
url = "https://mm.example.com"
token = "t"
channel_id = "c"
timeout = "10s"
[telegram]
token = "tg"
allowed_users = [1]
`,
			want: "must exceed mattermost timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tt.content))
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestValidateAccumulatesErrors(t *testing.T) {
	// Missing url, token and channel_id must all surface together.
	content := `
[telegram]
token = "tg"
allowed_users = [1]
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"url is required", "token is required", "channel_id is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing substring %q", err.Error(), want)
		}
	}
}
