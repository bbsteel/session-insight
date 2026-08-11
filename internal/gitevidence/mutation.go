package gitevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	pathpkg "path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bbsteel/session-insight/internal/model"
)

// MutationOperation is the structured file operation recorded by an edit
// tool. It is session evidence only; it must never be treated as a final file
// status or used to synthesize a net diff.
type MutationOperation string

const (
	MutationAdd    MutationOperation = "add"
	MutationEdit   MutationOperation = "edit"
	MutationWrite  MutationOperation = "write"
	MutationDelete MutationOperation = "delete"
	MutationMove   MutationOperation = "move"
)

// MutationResultState is the outcome recorded by the normalized tool-result
// stream. Unknown means no matching terminal result was present in the same
// authoritative source revision.
type MutationResultState string

const (
	MutationSucceeded MutationResultState = "succeeded"
	MutationFailed    MutationResultState = "failed"
	MutationRejected  MutationResultState = "rejected"
	MutationUnknown   MutationResultState = "unknown"
)

type MutationIssueCode string

const (
	MutationIssueInvalidPath MutationIssueCode = "invalid_path"
)

// MutationIssue deliberately omits the rejected path so absolute, traversal,
// control-character, or otherwise unsafe values cannot escape the builder.
type MutationIssue struct {
	Code         MutationIssueCode `json:"code"`
	EventID      string            `json:"event_id,omitempty"`
	ToolCallID   string            `json:"tool_call_id,omitempty"`
	ToolName     string            `json:"tool_name,omitempty"`
	Operation    MutationOperation `json:"operation,omitempty"`
	EventOrdinal int               `json:"event_ordinal"`
}

// MutationAttribution describes the transcript source and optional backing
// Session for one collaboration invocation. Root identity stays on
// MutationSource so child evidence never replaces its attribution root.
type MutationAttribution struct {
	SourceAgentType  string
	SourceSessionID  string
	BackingAgentType string
	BackingSessionID string
	InvocationID     string
}

// MutationSource binds a normalized RenderEvent stream to its authoritative
// source revision. InvocationAttribution may override source/backing identity
// for embedded or separately loaded subagent events.
type MutationSource struct {
	RootAgentType         string
	RootSessionID         string
	SourceRevision        string
	DefaultAttribution    MutationAttribution
	InvocationAttribution map[string]MutationAttribution
}

// FileMutationEvidence is a storage-neutral record of one structured edit
// tool operation. Failed, rejected, unknown, and rolled-back records remain
// evidence, but ResolveMutationEvidenceLink will not attach them to final
// files.
type FileMutationEvidence struct {
	ID               string              `json:"id"`
	RootAgentType    string              `json:"root_agent_type"`
	RootSessionID    string              `json:"root_session_id"`
	SourceAgentType  string              `json:"source_agent_type"`
	SourceSessionID  string              `json:"source_session_id"`
	BackingAgentType string              `json:"backing_agent_type,omitempty"`
	BackingSessionID string              `json:"backing_session_id,omitempty"`
	InvocationID     string              `json:"invocation_id,omitempty"`
	SourceRevision   string              `json:"source_revision"`
	EventID          string              `json:"event_id,omitempty"`
	ResultEventID    string              `json:"result_event_id,omitempty"`
	ToolCallID       string              `json:"tool_call_id,omitempty"`
	TurnIndex        int                 `json:"turn_index"`
	Depth            int                 `json:"depth"`
	RecordedAt       *time.Time          `json:"recorded_at,omitempty"`
	ToolName         string              `json:"tool_name"`
	Operation        MutationOperation   `json:"operation"`
	Path             string              `json:"path"`
	PreviousPath     string              `json:"previous_path,omitempty"`
	Result           MutationResultState `json:"result"`
	RolledBack       bool                `json:"rolled_back"`
	EventOrdinal     int                 `json:"event_ordinal"`
	OperationOrdinal int                 `json:"operation_ordinal"`
}

type MutationBuildResult struct {
	Mutations []FileMutationEvidence `json:"mutations"`
	Issues    []MutationIssue        `json:"issues"`
}

var ErrInvalidMutationSource = errors.New("invalid mutation source")

// BuildFileMutationEvidence normalizes only declared structured edit tools.
// It intentionally does not inspect command strings, stdout, or stderr for
// shell-shaped edits.
func BuildFileMutationEvidence(events []model.RenderEvent, source MutationSource) (MutationBuildResult, error) {
	if err := validateMutationSource(source); err != nil {
		return MutationBuildResult{}, err
	}
	rolledBack := rollbackMembership(events)
	resultsByParent, resultsByCall := indexMutationResults(events, source)
	result := MutationBuildResult{
		Mutations: []FileMutationEvidence{},
		Issues:    []MutationIssue{},
	}

	for eventOrdinal, event := range events {
		if event.Type != "ToolInvocation" {
			continue
		}
		specs := mutationSpecs(event)
		if len(specs) == 0 {
			continue
		}
		attribution := source.attribution(event)
		eventID := sanitizedOpaque(event.EventID)
		toolCallID := sanitizedOpaque(event.ToolCallID)
		terminal, terminalFound := matchingMutationResult(
			eventOrdinal, eventID, toolCallID, attribution.InvocationID,
			resultsByParent, resultsByCall,
		)
		state := MutationUnknown
		resultEventID := ""
		if terminalFound {
			state = mutationResultState(terminal.event)
			resultEventID = sanitizedOpaque(terminal.event.EventID)
		}

		for operationOrdinal, spec := range specs {
			path, err := SanitizeRepositoryRelativePath(spec.path)
			if err != nil {
				result.Issues = append(result.Issues, MutationIssue{
					Code: MutationIssueInvalidPath, EventID: eventID,
					ToolCallID: toolCallID, ToolName: event.ToolName,
					Operation: spec.operation, EventOrdinal: eventOrdinal,
				})
				continue
			}
			previousPath := ""
			if spec.previousPath != "" {
				previousPath, err = SanitizeRepositoryRelativePath(spec.previousPath)
				if err != nil {
					result.Issues = append(result.Issues, MutationIssue{
						Code: MutationIssueInvalidPath, EventID: eventID,
						ToolCallID: toolCallID, ToolName: event.ToolName,
						Operation: spec.operation, EventOrdinal: eventOrdinal,
					})
					continue
				}
			}
			recordedAt := timePointer(event.Timestamp)
			mutation := FileMutationEvidence{
				RootAgentType: source.RootAgentType, RootSessionID: source.RootSessionID,
				SourceAgentType: attribution.SourceAgentType, SourceSessionID: attribution.SourceSessionID,
				BackingAgentType: attribution.BackingAgentType, BackingSessionID: attribution.BackingSessionID,
				InvocationID: attribution.InvocationID, SourceRevision: source.SourceRevision,
				EventID: eventID, ResultEventID: resultEventID, ToolCallID: toolCallID,
				TurnIndex: event.TurnIndex, Depth: event.Depth, RecordedAt: recordedAt, ToolName: event.ToolName,
				Operation: spec.operation, Path: path, PreviousPath: previousPath,
				Result: state, RolledBack: rolledBack[eventOrdinal],
				EventOrdinal: eventOrdinal, OperationOrdinal: operationOrdinal,
			}
			mutation.ID = stableMutationID(mutation)
			result.Mutations = append(result.Mutations, mutation)
		}
	}
	return result, nil
}

// SanitizeRepositoryRelativePath returns a slash-normalized, lexical Git path.
// Absolute paths, traversal, NUL/control characters, invalid UTF-8, and
// overlong inputs are rejected without consulting the filesystem.
func SanitizeRepositoryRelativePath(raw string) (string, error) {
	if raw == "" || len(raw) > 4096 || !utf8.ValidString(raw) {
		return "", ErrInvalidMutationPath
	}
	for _, r := range raw {
		if r == 0 || r < 0x20 || r == 0x7f {
			return "", ErrInvalidMutationPath
		}
	}
	path := strings.ReplaceAll(raw, `\`, "/")
	if strings.HasPrefix(path, "/") || hasWindowsVolumePrefix(path) {
		return "", ErrInvalidMutationPath
	}
	path = pathpkg.Clean(path)
	if path == "." || path == ".." || strings.HasPrefix(path, "../") || strings.HasPrefix(path, "/") {
		return "", ErrInvalidMutationPath
	}
	return path, nil
}

var ErrInvalidMutationPath = errors.New("invalid repository-relative mutation path")

type mutationSpec struct {
	operation    MutationOperation
	path         string
	previousPath string
}

func mutationSpecs(event model.RenderEvent) []mutationSpec {
	switch event.ToolName {
	case "apply_patch":
		return applyPatchMutationSpecs(event.ToolInput)
	case "Edit", "str_replace_editor", "edit", "edit_file", "search_replace":
		return normalizedPathMutationSpec(event.ToolInput, MutationEdit)
	case "Write", "write", "write_file":
		return normalizedPathMutationSpec(event.ToolInput, MutationWrite)
	default:
		return nil
	}
}

func normalizedPathMutationSpec(input map[string]any, operation MutationOperation) []mutationSpec {
	path, _ := input["file_path"].(string)
	if path == "" {
		return nil
	}
	return []mutationSpec{{operation: operation, path: path}}
}

func applyPatchMutationSpecs(input map[string]any) []mutationSpec {
	var patch string
	for _, key := range []string{"args", "input", "patch"} {
		if value, ok := input[key].(string); ok && value != "" {
			patch = value
			break
		}
	}
	if patch == "" {
		return nil
	}

	var complete []mutationSpec
	var block []mutationSpec
	var current *mutationSpec
	inPatch := false
	flush := func() {
		if current != nil {
			block = append(block, *current)
			current = nil
		}
	}
	for _, rawLine := range strings.Split(patch, "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		switch {
		case line == "*** Begin Patch":
			inPatch = true
			block = nil
			current = nil
		case line == "*** End Patch" && inPatch:
			flush()
			complete = append(complete, block...)
			block = nil
			inPatch = false
		case !inPatch:
		case strings.HasPrefix(line, "*** Update File: "):
			flush()
			current = &mutationSpec{operation: MutationEdit, path: strings.TrimPrefix(line, "*** Update File: ")}
		case strings.HasPrefix(line, "*** Add File: "):
			flush()
			current = &mutationSpec{operation: MutationAdd, path: strings.TrimPrefix(line, "*** Add File: ")}
		case strings.HasPrefix(line, "*** Delete File: "):
			flush()
			current = &mutationSpec{operation: MutationDelete, path: strings.TrimPrefix(line, "*** Delete File: ")}
		case strings.HasPrefix(line, "*** Move to: ") && current != nil:
			current.previousPath = current.path
			current.path = strings.TrimPrefix(line, "*** Move to: ")
			current.operation = MutationMove
		}
	}
	return complete
}

type indexedMutationResult struct {
	index int
	event model.RenderEvent
}

type mutationResultKey struct {
	id           string
	invocationID string
}

func indexMutationResults(events []model.RenderEvent, source MutationSource) (map[mutationResultKey][]indexedMutationResult, map[mutationResultKey][]indexedMutationResult) {
	byParent := make(map[mutationResultKey][]indexedMutationResult)
	byCall := make(map[mutationResultKey][]indexedMutationResult)
	for index, event := range events {
		if event.Type != "ToolResult" {
			continue
		}
		invocationID := source.attribution(event).InvocationID
		result := indexedMutationResult{index: index, event: event}
		if parentID := sanitizedOpaque(event.ParentEventID); parentID != "" {
			key := mutationResultKey{id: parentID, invocationID: invocationID}
			byParent[key] = append(byParent[key], result)
		}
		if callID := sanitizedOpaque(event.ToolCallID); callID != "" {
			key := mutationResultKey{id: callID, invocationID: invocationID}
			byCall[key] = append(byCall[key], result)
		}
	}
	return byParent, byCall
}

func matchingMutationResult(eventIndex int, eventID, callID, invocationID string, byParent, byCall map[mutationResultKey][]indexedMutationResult) (indexedMutationResult, bool) {
	if eventID != "" {
		if result, ok := lastResultAfter(byParent[mutationResultKey{id: eventID, invocationID: invocationID}], eventIndex); ok {
			return result, true
		}
	}
	if callID != "" {
		return lastResultAfter(byCall[mutationResultKey{id: callID, invocationID: invocationID}], eventIndex)
	}
	return indexedMutationResult{}, false
}

func lastResultAfter(results []indexedMutationResult, eventIndex int) (indexedMutationResult, bool) {
	for index := len(results) - 1; index >= 0; index-- {
		if results[index].index > eventIndex {
			return results[index], true
		}
	}
	return indexedMutationResult{}, false
}

func mutationResultState(event model.RenderEvent) MutationResultState {
	switch {
	case event.Rejected:
		return MutationRejected
	case event.ExitCode != 0 || event.ErrorKind != "" || event.TimedOut:
		return MutationFailed
	default:
		return MutationSucceeded
	}
}

func rollbackMembership(events []model.RenderEvent) []bool {
	membership := make([]bool, len(events))
	depth := 0
	for index, event := range events {
		if event.Type == "RollbackStart" {
			depth++
			continue
		}
		rolledBack, _ := event.Metadata["rolled_back"].(bool)
		membership[index] = depth > 0 || event.TurnIndex < 0 || rolledBack
		if event.Type == "RollbackEnd" && depth > 0 {
			depth--
		}
	}
	return membership
}

func (source MutationSource) attribution(event model.RenderEvent) MutationAttribution {
	attribution := source.DefaultAttribution
	invocationID := sanitizedOpaque(event.InvocationID)
	if invocationID == "" {
		invocationID = attribution.InvocationID
	}
	if override, ok := source.InvocationAttribution[invocationID]; ok {
		attribution = override
	}
	attribution.InvocationID = invocationID
	if event.AgentType != "" && attribution.SourceAgentType == "" {
		attribution.SourceAgentType = sanitizedOpaque(event.AgentType)
	}
	return attribution
}

func validateMutationSource(source MutationSource) error {
	if source.RootAgentType == "" || source.RootSessionID == "" || source.SourceRevision == "" ||
		sanitizedOpaque(source.RootAgentType) != source.RootAgentType ||
		sanitizedOpaque(source.RootSessionID) != source.RootSessionID ||
		sanitizedOpaque(source.SourceRevision) != source.SourceRevision {
		return fmt.Errorf("%w: root identity and source revision are required", ErrInvalidMutationSource)
	}
	if err := validateMutationAttribution(source.DefaultAttribution); err != nil {
		return err
	}
	for invocationID, attribution := range source.InvocationAttribution {
		if sanitizedOpaque(invocationID) != invocationID || invocationID == "" {
			return fmt.Errorf("%w: invalid invocation attribution key", ErrInvalidMutationSource)
		}
		if err := validateMutationAttribution(attribution); err != nil {
			return err
		}
	}
	return nil
}

func validateMutationAttribution(attribution MutationAttribution) error {
	if attribution.SourceAgentType == "" || attribution.SourceSessionID == "" ||
		sanitizedOpaque(attribution.SourceAgentType) != attribution.SourceAgentType ||
		sanitizedOpaque(attribution.SourceSessionID) != attribution.SourceSessionID {
		return fmt.Errorf("%w: source identity is required", ErrInvalidMutationSource)
	}
	if (attribution.BackingAgentType == "") != (attribution.BackingSessionID == "") {
		return fmt.Errorf("%w: backing identity must be complete", ErrInvalidMutationSource)
	}
	for _, value := range []string{attribution.BackingAgentType, attribution.BackingSessionID, attribution.InvocationID} {
		if value != "" && sanitizedOpaque(value) != value {
			return fmt.Errorf("%w: invalid attribution identity", ErrInvalidMutationSource)
		}
	}
	return nil
}

func sanitizedOpaque(value string) string {
	if value == "" || len(value) > 512 || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return ""
	}
	for _, r := range value {
		if r == 0 || r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return value
}

func hasWindowsVolumePrefix(path string) bool {
	return len(path) >= 2 && path[1] == ':' &&
		((path[0] >= 'a' && path[0] <= 'z') || (path[0] >= 'A' && path[0] <= 'Z'))
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}

func stableMutationID(mutation FileMutationEvidence) string {
	hash := sha256.New()
	for _, value := range []string{
		mutation.RootAgentType, mutation.RootSessionID,
		mutation.SourceAgentType, mutation.SourceSessionID,
		mutation.BackingAgentType, mutation.BackingSessionID,
		mutation.InvocationID, mutation.SourceRevision,
		mutation.EventID, mutation.ToolCallID,
		fmt.Sprintf("%d", mutation.EventOrdinal),
		fmt.Sprintf("%d", mutation.OperationOrdinal),
		string(mutation.Operation), mutation.Path, mutation.PreviousPath,
	} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return "mutation:v1:" + hex.EncodeToString(hash.Sum(nil))
}
