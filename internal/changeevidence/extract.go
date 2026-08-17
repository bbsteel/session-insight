package changeevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/bbsteel/session-insight/internal/changehost"
	"github.com/bbsteel/session-insight/internal/model"
)

const (
	CommandGitHubCLI = "github_cli_pr_create"
	CommandGitLabCLI = "gitlab_cli_mr_create"
)

// ExtractCreationEvidence pairs successful, non-truncated creation commands
// with their exact tool results. Plain URL mentions are deliberately ignored.
func ExtractCreationEvidence(events []model.RenderEvent, sourceRevision string) []model.ChangeRequestCreationEvidence {
	registry := changehost.NewDefaultRegistry()
	invocations := make(map[string]model.RenderEvent)
	for _, event := range events {
		if event.Type == "ToolInvocation" && event.EventID != "" {
			invocations[event.EventID] = event
		}
	}
	result := make([]model.ChangeRequestCreationEvidence, 0)
	seen := make(map[string]struct{})
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
		key := invocation.EventID + "\x00" + reference.NormalizedURL
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		digest := sha256.Sum256([]byte(sourceRevision + "\x00" + key))
		result = append(result, model.ChangeRequestCreationEvidence{
			EvidenceID: "cr-create-" + hex.EncodeToString(digest[:]), Reference: reference,
			CommandKind: kind, ToolName: invocation.ToolName, EventID: invocation.EventID,
			ToolCallID: invocation.ToolCallID, TurnIndex: invocation.TurnIndex,
			InvocationID: invocation.InvocationID, RecordedAt: event.Timestamp,
			SourceRevision: sourceRevision, Assessment: model.ExactGitEvidence(),
		})
	}
	return result
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
	for _, statement := range unquotedStatements(command) {
		fields := strings.Fields(statement)
		if len(fields) < 3 {
			continue
		}
		switch {
		case fields[0] == "gh" && fields[1] == "pr" && fields[2] == "create":
			return CommandGitHubCLI, model.ChangeProviderGitHub, true
		case fields[0] == "glab" && fields[1] == "mr" && fields[2] == "create":
			return CommandGitLabCLI, model.ChangeProviderGitLab, true
		}
	}
	return "", "", false
}

// unquotedStatements splits a tool command on unquoted &&, ;, and newlines.
// Quoted bodies, including heredoc text inside double quotes, stay one statement
// so echo/prose mentions are not promoted and a trailing gh/glab create is found.
func unquotedStatements(command string) []string {
	var (
		out    []string
		buf    strings.Builder
		quote  rune
		escape bool
	)
	flush := func() {
		statement := strings.TrimSpace(buf.String())
		buf.Reset()
		if statement != "" {
			out = append(out, statement)
		}
	}
	for i, r := range command {
		if escape {
			buf.WriteRune(r)
			escape = false
			continue
		}
		if quote == 0 && r == '\\' {
			buf.WriteRune(r)
			escape = true
			continue
		}
		if quote == 0 && (r == '\'' || r == '"') {
			quote = r
			buf.WriteRune(r)
			continue
		}
		if quote != 0 {
			buf.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\n' || r == ';' {
			flush()
			continue
		}
		if r == '&' && i+1 < len(command) && command[i+1] == '&' {
			flush()
			continue
		}
		if r == '&' && i > 0 && command[i-1] == '&' {
			continue
		}
		buf.WriteRune(r)
	}
	flush()
	return out
}

func uniqueReference(registry *changehost.Registry, output string, provider model.ChangeProviderKind) (model.ChangeRequestReference, bool) {
	var found *model.ChangeRequestReference
	for _, field := range strings.Fields(output) {
		candidate := strings.Trim(field, "<>[](){}\"'`,.;")
		reference, err := registry.ResolveReference(candidate)
		if err != nil || reference.Provider != provider {
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
