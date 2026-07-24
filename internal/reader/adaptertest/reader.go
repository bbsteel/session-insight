// Package adaptertest is shared conformance-test infrastructure for Agent
// adapters. It is imported only from *_test.go files.
//
// It must not import the parent internal/reader package: reader/registry.go
// imports every concrete adapter, so a cycle would form if adapters imported
// reader through this package under test.
package adaptertest

import "github.com/bbsteel/session-insight/internal/model"

// Reader is the minimal structural surface required by the shared suite.
// Concrete BaseSessionReader implementations satisfy it without importing
// this package.
type Reader interface {
	AgentType() string
	DisplayName() string
	ListSessions() ([]model.Session, error)
	GetSession(id string) (*model.SessionDetail, error)
	RenderANSI(id string, cols int) (string, error)
	GetRenderEvents(id string) ([]model.RenderEvent, error)
}

// LiveRevisionProvider is the structural optional interface for realtime
// content-revision detection (matches production LiveRevisionProvider).
type LiveRevisionProvider interface {
	LiveRevision(id string) (int64, error)
}

// SessionDeleter is the structural optional interface for permanent session
// removal (matches production SessionDeleter).
type SessionDeleter interface {
	DeleteSession(id string) error
}

// SessionProcessFinder is the structural optional interface for exact PID
// lookup used by terminate (matches production SessionProcessFinder).
type SessionProcessFinder interface {
	SessionProcesses(id string) ([]int, error)
}

// SessionLivenessChecker is the structural optional interface for block-only
// liveness without PIDs (matches production SessionLivenessChecker).
type SessionLivenessChecker interface {
	SessionRunning(id string) (bool, error)
}

// SessionLivenessProvider is the structural optional interface for cheap
// list-path liveness (matches production SessionLivenessProvider).
type SessionLivenessProvider interface {
	SessionLive(id string) (bool, error)
}
