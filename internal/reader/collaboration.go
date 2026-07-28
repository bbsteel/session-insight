package reader

import (
	"context"

	"github.com/bbsteel/session-insight/internal/collaboration"
	"github.com/bbsteel/session-insight/internal/model"
)

// CollaborationReader is an optional reader capability: normalize the
// multi-Agent collaboration structure of one root Session into the shared
// contract graph.
//
// Adapters own source discovery, stable identity, root/child linkage,
// source anchors, status normalization, evidence precision, backing Session
// references, and replay-event association. Shared code owns graph
// validation (collaboration.Validate), persistence, aggregation, and API
// serialization. Readers without this interface simply declare their
// subtasks capability without collaboration payloads; the UI must not
// fabricate a graph for them.
//
// Implementations must satisfy the contract invariants checked by
// collaboration.Validate and the shared conformance skeleton
// (internal/reader/adaptertest): deterministic native-ID-based invocation
// identity stable across repeated parses, exactly one deterministic root
// invocation, per-fact evidence precision with reason codes, and missing
// evidence left absent rather than synthesized.
type CollaborationReader interface {
	ReadCollaboration(ctx context.Context, root model.Session) (collaboration.CollaborationGraph, error)
}
