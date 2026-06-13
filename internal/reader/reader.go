package reader

import "session-insight/internal/model"

type BaseSessionReader interface {
	AgentType() string
	DisplayName() string
	ListSessions() ([]model.Session, error)
	GetSession(id string) (*model.SessionDetail, error)
}
