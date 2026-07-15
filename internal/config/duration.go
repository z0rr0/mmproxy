package config

import (
	"fmt"
	"time"
)

// Duration wraps time.Duration so TOML values can be written as Go duration
// strings like "10s" or "1m30s". Negative values are rejected.
type Duration time.Duration

// UnmarshalText implements encoding.TextUnmarshaler for TOML string values.
func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("failed to parse duration [%s]: %w", text, err)
	}
	if parsed < 0 {
		return fmt.Errorf("duration cannot be negative [%s]: %v", text, parsed)
	}
	*d = Duration(parsed)
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// Timed returns the value as a standard time.Duration.
func (d Duration) Timed() time.Duration { return time.Duration(d) }

// String implements fmt.Stringer.
func (d Duration) String() string { return time.Duration(d).String() }
