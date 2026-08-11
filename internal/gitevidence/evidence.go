package gitevidence

import (
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// MutationPositionSet identifies one rendered position layout. Its source
// revision is deliberately independent from PositionsRevision.
type MutationPositionSet struct {
	SourceRevision    string
	PositionsRevision int64
	TimestampWindow   time.Duration
	Anchors           []MutationPositionAnchor
}

// MutationPositionAnchor is the storage-neutral subset of a rendered tool
// position needed to reconnect a mutation to replay evidence.
type MutationPositionAnchor struct {
	Path             string
	RootAgentType    string
	RootSessionID    string
	SourceAgentType  string
	SourceSessionID  string
	BackingAgentType string
	BackingSessionID string
	InvocationID     string
	EventID          string
	ToolCallID       string
	TurnIndex        *int
	RecordedAt       *time.Time
}

type MutationLinkStatus string

const (
	MutationLinkExact       MutationLinkStatus = "exact"
	MutationLinkEstimated   MutationLinkStatus = "estimated"
	MutationLinkStale       MutationLinkStatus = "stale"
	MutationLinkUnavailable MutationLinkStatus = "unavailable"
)

type MutationLinkReason string

const (
	MutationLinkReasonPositionsUnavailable   MutationLinkReason = "positions_revision_unavailable"
	MutationLinkReasonSourceRevisionMismatch MutationLinkReason = "source_revision_mismatch"
	MutationLinkReasonMutationNotFinal       MutationLinkReason = "mutation_not_final"
	MutationLinkReasonInvalidPath            MutationLinkReason = "invalid_path"
	MutationLinkReasonNoMatch                MutationLinkReason = "no_match"
	MutationLinkReasonAmbiguous              MutationLinkReason = "ambiguous_match"
)

type MutationLinkResolution struct {
	Status MutationLinkStatus     `json:"status"`
	Reason MutationLinkReason     `json:"reason,omitempty"`
	Link   *model.GitEvidenceLink `json:"link,omitempty"`
}

const defaultMutationTimestampWindow = 2 * time.Second

// ResolveMutationEvidenceLink resolves one mutation against the current
// rendered position set. Only succeeded, active-path mutations are eligible
// for a final file. Exact IDs win, then complete attribution plus turn/time;
// a unique path-only match is explicitly estimated. Stable event/call IDs
// remain the anchor when the matching terminal position is inside a fold.
func ResolveMutationEvidenceLink(mutation FileMutationEvidence, finalPath string, positions MutationPositionSet) MutationLinkResolution {
	if positions.PositionsRevision <= 0 {
		return unavailableMutationLink(MutationLinkReasonPositionsUnavailable)
	}
	if positions.SourceRevision == "" || positions.SourceRevision != mutation.SourceRevision {
		return MutationLinkResolution{Status: MutationLinkStale, Reason: MutationLinkReasonSourceRevisionMismatch}
	}
	if mutation.Result != MutationSucceeded || mutation.RolledBack {
		return unavailableMutationLink(MutationLinkReasonMutationNotFinal)
	}
	normalizedFinalPath, err := SanitizeRepositoryRelativePath(finalPath)
	if err != nil {
		return unavailableMutationLink(MutationLinkReasonInvalidPath)
	}
	if normalizedFinalPath != mutation.Path {
		return unavailableMutationLink(MutationLinkReasonNoMatch)
	}

	candidates := make([]MutationPositionAnchor, 0, len(positions.Anchors))
	for _, anchor := range positions.Anchors {
		anchorPath, err := SanitizeRepositoryRelativePath(anchor.Path)
		if err == nil && anchorPath == normalizedFinalPath {
			candidates = append(candidates, anchor)
		}
	}

	exactIDs := filterMutationAnchors(candidates, func(anchor MutationPositionAnchor) bool {
		return stableMutationIDMatch(mutation, anchor)
	})
	if resolution, decided := uniqueMutationLink(mutation, positions.PositionsRevision, exactIDs, model.ExactGitEvidence()); decided {
		return resolution
	}

	window := positions.TimestampWindow
	if window <= 0 {
		window = defaultMutationTimestampWindow
	}
	attributed := filterMutationAnchors(candidates, func(anchor MutationPositionAnchor) bool {
		return mutationAttributionMatch(mutation, anchor) && mutationTurnTimeMatch(mutation, anchor, window)
	})
	if resolution, decided := uniqueMutationLink(mutation, positions.PositionsRevision, attributed, model.ExactGitEvidence()); decided {
		return resolution
	}

	if len(candidates) == 1 {
		assessment := model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonSourceMissing)
		return resolvedMutationLink(mutation, positions.PositionsRevision, candidates[0], assessment)
	}
	if len(candidates) > 1 {
		return unavailableMutationLink(MutationLinkReasonAmbiguous)
	}
	return unavailableMutationLink(MutationLinkReasonNoMatch)
}

func uniqueMutationLink(mutation FileMutationEvidence, positionsRevision int64, anchors []MutationPositionAnchor, assessment model.GitEvidenceAssessment) (MutationLinkResolution, bool) {
	switch len(anchors) {
	case 0:
		return MutationLinkResolution{}, false
	case 1:
		return resolvedMutationLink(mutation, positionsRevision, anchors[0], assessment), true
	default:
		return unavailableMutationLink(MutationLinkReasonAmbiguous), true
	}
}

func resolvedMutationLink(mutation FileMutationEvidence, positionsRevision int64, anchor MutationPositionAnchor, assessment model.GitEvidenceAssessment) MutationLinkResolution {
	eventID := sanitizedOpaque(anchor.EventID)
	toolCallID := sanitizedOpaque(anchor.ToolCallID)
	turn := mutation.TurnIndex
	recordedAt := mutation.RecordedAt
	link := &model.GitEvidenceLink{
		RootAgentType: mutation.RootAgentType, RootSessionID: mutation.RootSessionID,
		SourceAgentType: mutation.SourceAgentType, SourceSessionID: mutation.SourceSessionID,
		BackingAgentType: mutation.BackingAgentType, BackingSessionID: mutation.BackingSessionID,
		InvocationID: mutation.InvocationID, SourceRevision: mutation.SourceRevision,
		PositionsRevision: positionsRevision, EventID: eventID, ToolCallID: toolCallID,
		TurnIndex: &turn, RecordedAt: recordedAt, Assessment: assessment,
	}
	status := MutationLinkExact
	if assessment.State == model.GitEvidenceEstimated {
		status = MutationLinkEstimated
	}
	return MutationLinkResolution{Status: status, Link: link}
}

func unavailableMutationLink(reason MutationLinkReason) MutationLinkResolution {
	return MutationLinkResolution{Status: MutationLinkUnavailable, Reason: reason}
}

func filterMutationAnchors(anchors []MutationPositionAnchor, keep func(MutationPositionAnchor) bool) []MutationPositionAnchor {
	filtered := make([]MutationPositionAnchor, 0, len(anchors))
	for _, anchor := range anchors {
		if keep(anchor) {
			filtered = append(filtered, anchor)
		}
	}
	return filtered
}

func stableMutationIDMatch(mutation FileMutationEvidence, anchor MutationPositionAnchor) bool {
	hasID := false
	if mutation.EventID != "" && sanitizedOpaque(anchor.EventID) != "" {
		hasID = true
		if mutation.EventID != anchor.EventID {
			return false
		}
	}
	if mutation.ToolCallID != "" && sanitizedOpaque(anchor.ToolCallID) != "" {
		hasID = true
		if mutation.ToolCallID != anchor.ToolCallID {
			return false
		}
	}
	return hasID
}

func mutationAttributionMatch(mutation FileMutationEvidence, anchor MutationPositionAnchor) bool {
	return anchor.RootAgentType != "" && anchor.RootAgentType == mutation.RootAgentType &&
		anchor.RootSessionID == mutation.RootSessionID &&
		anchor.SourceAgentType == mutation.SourceAgentType &&
		anchor.SourceSessionID == mutation.SourceSessionID &&
		anchor.BackingAgentType == mutation.BackingAgentType &&
		anchor.BackingSessionID == mutation.BackingSessionID &&
		anchor.InvocationID == mutation.InvocationID
}

func mutationTurnTimeMatch(mutation FileMutationEvidence, anchor MutationPositionAnchor, window time.Duration) bool {
	if anchor.TurnIndex == nil || *anchor.TurnIndex != mutation.TurnIndex {
		return false
	}
	if mutation.RecordedAt == nil || anchor.RecordedAt == nil {
		return true
	}
	delta := mutation.RecordedAt.Sub(*anchor.RecordedAt)
	if delta < 0 {
		delta = -delta
	}
	return delta <= window
}
