package model

import (
	"testing"
	"time"
)

func TestIsSessionLive(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		updatedAt time.Time
		want      bool
	}{
		{"just now", now, true},
		{"within window", now.Add(-(LiveWindow - time.Minute)), true},
		{"just past window", now.Add(-(LiveWindow + time.Second)), false},
		{"long ago", now.Add(-24 * time.Hour), false},
		{"zero value", time.Time{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsSessionLive(c.updatedAt); got != c.want {
				t.Errorf("IsSessionLive(%v) = %v, want %v", c.updatedAt, got, c.want)
			}
		})
	}
}
