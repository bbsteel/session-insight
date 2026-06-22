package reader

import "session-insight/internal/model"

type BaseSessionReader interface {
	AgentType() string
	DisplayName() string
	ListSessions() ([]model.Session, error)
	GetSession(id string) (*model.SessionDetail, error)

	// RenderANSI returns the session's RenderEvent stream formatted as ANSI
	// terminal text (see internal/render). Readers without a RenderEvent
	// adapter yet (Codex/Copilot, as of Phase 2) should return a non-nil
	// error rather than panic or silently produce empty output, so the API
	// layer can tell "session not found" apart from "rendering not
	// supported for this agent type" and report it clearly instead of
	// falling through to the next reader.
	// cols is the terminal column count from the frontend (0 = use default).
	RenderANSI(id string, cols int) (string, error)

	// GetRenderEvents returns the raw render event stream for a session.
	// Used by the server to extract structured data (e.g. Edit calls).
	GetRenderEvents(id string) ([]model.RenderEvent, error)
}
