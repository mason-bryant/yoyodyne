package config

import (
	"encoding/json"
	"fmt"
	"time"
)

// Duration is a configured span of time. It exists rather than a plain
// time.Duration because a configured duration is read by people in two places:
// the project file they write it in and the effective configuration `config
// show` reports back. Go's own encodings would make those disagree — YAML would
// round-trip "6h" but JSON would report 21600000000000 — and a pause an
// operator has to reason about must not be reported as a nanosecond count.
type Duration time.Duration

// Duration returns the configured span for use with the standard library.
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

func (d Duration) String() string {
	return time.Duration(d).String()
}

func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}

func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var text string
	if err := unmarshal(&text); err != nil {
		return fmt.Errorf("duration must be a string such as \"6h\" or \"15m\": %w", err)
	}
	return d.parse(text)
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("duration must be a string such as \"6h\" or \"15m\": %w", err)
	}
	return d.parse(text)
}

func (d *Duration) parse(text string) error {
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", text, err)
	}
	*d = Duration(parsed)
	return nil
}
