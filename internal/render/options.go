package render

import "strings"

// Options are per-request render options driven by user settings, as opposed
// to Profile, which is the per-agent terminal layout. They change the ANSI
// line layout, so anything caching render output or line positions must key
// on Mask() as well.
type Options struct {
	// Timestamp prefixes (HH:MM:SS, server-local time) per message kind.
	TimestampUser      bool
	TimestampAssistant bool
	TimestampTool      bool
}

// ParseTimestampKinds parses the "ts" query value / stored setting: a
// comma-separated subset of "user", "assistant", "tool". Unknown entries are
// ignored so a stale or hand-edited setting can't break rendering.
func ParseTimestampKinds(s string) Options {
	var o Options
	for _, k := range strings.Split(s, ",") {
		switch strings.TrimSpace(k) {
		case "user":
			o.TimestampUser = true
		case "assistant":
			o.TimestampAssistant = true
		case "tool":
			o.TimestampTool = true
		}
	}
	return o
}

// KindsString returns the canonical serialized form for storage.
func (o Options) KindsString() string {
	var kinds []string
	if o.TimestampUser {
		kinds = append(kinds, "user")
	}
	if o.TimestampAssistant {
		kinds = append(kinds, "assistant")
	}
	if o.TimestampTool {
		kinds = append(kinds, "tool")
	}
	return strings.Join(kinds, ",")
}

// Mask returns a canonical small integer identifying this option set for
// cache keying.
func (o Options) Mask() int64 {
	var m int64
	if o.TimestampUser {
		m |= 1
	}
	if o.TimestampAssistant {
		m |= 2
	}
	if o.TimestampTool {
		m |= 4
	}
	return m
}
