package reader

import "session-insight/internal/model"

type BaseSessionReader interface {
	AgentType() string
	ListSessions() ([]model.Session, error)
}
