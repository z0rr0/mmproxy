package config

import (
	"strings"
	"testing"
	"time"
)

func TestDurationUnmarshalText(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Duration
		wantErr string
	}{
		{name: "seconds", input: "10s", want: Duration(10 * time.Second)},
		{name: "composite", input: "1m30s", want: Duration(90 * time.Second)},
		{name: "milliseconds", input: "500ms", want: Duration(500 * time.Millisecond)},
		{name: "zero", input: "0s", want: 0},
		{name: "not a duration", input: "banana", wantErr: "failed to parse duration"},
		{name: "missing unit", input: "10", wantErr: "failed to parse duration"},
		{name: "empty", input: "", wantErr: "failed to parse duration"},
		{name: "negative", input: "-5s", wantErr: "duration cannot be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Duration
			err := d.UnmarshalText([]byte(tt.input))
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error for %q", tt.input)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d != tt.want {
				t.Errorf("duration = %v, want %v", d, tt.want)
			}
		})
	}
}

func TestDurationTextRoundTrip(t *testing.T) {
	src := Duration(90 * time.Second)
	text, err := src.MarshalText()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(text) != "1m30s" {
		t.Errorf("text = %q, want %q", text, "1m30s")
	}

	var dst Duration
	if err = dst.UnmarshalText(text); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dst != src {
		t.Errorf("round trip = %v, want %v", dst, src)
	}
}

func TestDurationTimedAndString(t *testing.T) {
	d := Duration(10 * time.Second)
	if d.Timed() != 10*time.Second {
		t.Errorf("Timed() = %v, want %v", d.Timed(), 10*time.Second)
	}
	if d.String() != "10s" {
		t.Errorf("String() = %q, want %q", d.String(), "10s")
	}
}
