package changeevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/changehost"
	"github.com/bbsteel/session-insight/internal/model"
)

const (
	CommandGitHubCLI        = "github_cli_pr_create"
	CommandGitLabCLI        = "gitlab_cli_mr_create"
	CommandChangeRequestURL = "change_request_url"
)

// ExtractCreationEvidence records hosted review links that appear in a
// session. A successful gh/glab create still wins as created evidence for that
// URL; every other recognized PR/MR-shaped URL is stored as a local mention.
// Plain repository or wiki URLs are ignored.
func ExtractCreationEvidence(events []model.RenderEvent, sourceRevision string) []model.ChangeRequestCreationEvidence {
	registry := changehost.NewDefaultRegistry()
	invocations := make(map[string]model.RenderEvent)
	for _, event := range events {
		if event.Type == "ToolInvocation" && event.EventID != "" {
			invocations[event.EventID] = event
		}
	}
	result := make([]model.ChangeRequestCreationEvidence, 0)
	seenURL := make(map[string]struct{})

	for _, event := range events {
		if event.Type != "ToolResult" || event.ParentEventID == "" || event.ExitCode != 0 ||
			event.Truncated || event.TimedOut || event.Rejected || event.ErrorKind != "" {
			continue
		}
		invocation, ok := invocations[event.ParentEventID]
		if !ok {
			continue
		}
		kind, provider, ok := creationCommand(toolCommand(invocation.ToolInput))
		if !ok {
			continue
		}
		reference, ok := uniqueReference(registry, event.Stdout, provider)
		if !ok {
			continue
		}
		if _, duplicate := seenURL[reference.NormalizedURL]; duplicate {
			continue
		}
		seenURL[reference.NormalizedURL] = struct{}{}
		result = append(result, newCreationEvidence(sourceRevision, invocation.EventID, reference, kind, invocation, event.Timestamp))
	}

	for index, event := range events {
		if event.Truncated || event.TimedOut || event.Rejected {
			continue
		}
		for _, reference := range scanReferences(registry, eventText(event)) {
			if _, duplicate := seenURL[reference.NormalizedURL]; duplicate {
				continue
			}
			seenURL[reference.NormalizedURL] = struct{}{}
			eventID := event.EventID
			if eventID == "" {
				eventID = "render:" + strconv.Itoa(index)
			}
			toolName := event.ToolName
			if toolName == "" {
				toolName = "message"
			}
			recordedAt := event.Timestamp
			if recordedAt.IsZero() {
				recordedAt = time.Unix(0, int64(index)+1).UTC()
			}
			carrier := model.RenderEvent{
				EventID: eventID, ToolName: toolName, ToolCallID: event.ToolCallID,
				TurnIndex: event.TurnIndex, InvocationID: event.InvocationID,
			}
			result = append(result, newCreationEvidence(
				sourceRevision, eventID, reference, CommandChangeRequestURL, carrier, recordedAt,
			))
		}
	}
	return result
}

func newCreationEvidence(
	sourceRevision, eventID string,
	reference model.ChangeRequestReference,
	kind string,
	carrier model.RenderEvent,
	recordedAt time.Time,
) model.ChangeRequestCreationEvidence {
	key := eventID + "\x00" + reference.NormalizedURL
	digest := sha256.Sum256([]byte(sourceRevision + "\x00" + key))
	return model.ChangeRequestCreationEvidence{
		EvidenceID: "cr-create-" + hex.EncodeToString(digest[:]), Reference: reference,
		CommandKind: kind, ToolName: carrier.ToolName, EventID: eventID,
		ToolCallID: carrier.ToolCallID, TurnIndex: carrier.TurnIndex,
		InvocationID: carrier.InvocationID, RecordedAt: recordedAt,
		SourceRevision: sourceRevision, Assessment: model.ExactGitEvidence(),
	}
}

func toolCommand(input map[string]any) string {
	if input == nil {
		return ""
	}
	if command, ok := input["command"].(string); ok {
		return command
	}
	if command, ok := input["cmd"].(string); ok {
		return command
	}
	return ""
}

func creationCommand(command string) (string, model.ChangeProviderKind, bool) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) < 3 {
		return "", "", false
	}
	switch {
	case fields[0] == "gh" && fields[1] == "pr" && fields[2] == "create":
		return CommandGitHubCLI, model.ChangeProviderGitHub, true
	case fields[0] == "glab" && fields[1] == "mr" && fields[2] == "create":
		return CommandGitLabCLI, model.ChangeProviderGitLab, true
	default:
		return "", "", false
	}
}

func uniqueReference(registry *changehost.Registry, output string, provider model.ChangeProviderKind) (model.ChangeRequestReference, bool) {
	var found *model.ChangeRequestReference
	for _, reference := range scanReferences(registry, output) {
		if reference.Provider != provider {
			continue
		}
		if found != nil && found.NormalizedURL != reference.NormalizedURL {
			return model.ChangeRequestReference{}, false
		}
		copy := reference
		found = &copy
	}
	if found == nil {
		return model.ChangeRequestReference{}, false
	}
	return *found, true
}

func scanReferences(registry *changehost.Registry, text string) []model.ChangeRequestReference {
	if text == "" {
		return nil
	}
	found := make([]model.ChangeRequestReference, 0)
	seen := make(map[string]struct{})
	for _, field := range strings.Fields(text) {
		candidate := strings.Trim(field, "<>[](){}\"'`,.;")
		reference, err := registry.ResolveReference(candidate)
		if err != nil {
			continue
		}
		if _, duplicate := seen[reference.NormalizedURL]; duplicate {
			continue
		}
		seen[reference.NormalizedURL] = struct{}{}
		found = append(found, reference)
	}
	return found
}

func eventText(event model.RenderEvent) string {
	parts := make([]string, 0, 3)
	if event.Text != "" {
		parts = append(parts, event.Text)
	}
	if event.Stdout != "" {
		parts = append(parts, event.Stdout)
	}
	if event.Stderr != "" {
		parts = append(parts, event.Stderr)
	}
	return strings.Join(parts, "\n")
}
