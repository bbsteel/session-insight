package model

import "time"

// FormatTime converts a time.Time to UTC and formats it as RFC3339.
// Use this for all API/DB time serialization to avoid timezone bugs.
func FormatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
