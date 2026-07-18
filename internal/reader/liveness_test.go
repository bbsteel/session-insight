package reader

import (
	"errors"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

type livenessProvider struct {
	live bool
	err  error
}

func (p livenessProvider) SessionLive(string) (bool, error) { return p.live, p.err }

func TestIsSessionLiveUsesAgentCapabilities(t *testing.T) {
	recent := model.Session{ID: "s1", UpdatedAt: time.Now()}
	stale := model.Session{ID: "s1", UpdatedAt: time.Now().Add(-2 * model.LiveWindow)}
	tests := []struct {
		name   string
		source any
		sess   model.Session
		want   bool
	}{
		{name: "stale upper bound", source: livenessProvider{live: true}, sess: stale, want: false},
		{name: "reader without capability falls back to timestamp", source: struct{}{}, sess: recent, want: true},
		{name: "agent registry owns session", source: livenessProvider{live: true}, sess: recent, want: true},
		{name: "agent registry vetoes recent completed session", source: livenessProvider{}, sess: recent, want: false},
		{name: "capability errors preserve fallback", source: livenessProvider{err: errors.New("read")}, sess: recent, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSessionLive(tt.source, tt.sess); got != tt.want {
				t.Fatalf("IsSessionLive()=%v, want %v", got, tt.want)
			}
		})
	}
}
