package worktreereview

import (
	"fmt"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

// Mapping from ReviewEvent to the Session Insight render stream (design
// 5.3): stages and dimensions become Tool invocation/result pairs, provider
// calls carry token usage, findings are structured result events, and the
// gate decision is the session's final assistant text.
//
// Event IDs and ToolCallIDs derive from the persisted sequence, so replays
// are stable and tool results always pair with their invocation.

func eventID(sequence int) string { return fmt.Sprintf("wr-%d", sequence) }

// stageTurnIndex assigns each pipeline stage its own turn in first-seen
// order; non-stage events share the current turn.
type turnAssigner struct {
	byStage map[string]int
	next    int
}

func newTurnAssigner() *turnAssigner {
	return &turnAssigner{byStage: make(map[string]int), next: 1}
}

func (t *turnAssigner) forStage(stage string) int {
	if index, ok := t.byStage[stage]; ok {
		return index
	}
	index := t.next
	t.byStage[stage] = index
	t.next++
	return index
}

func (t *turnAssigner) current() int {
	if t.next == 1 {
		return 0
	}
	return t.next - 1
}

// eventsToRenderEvents converts one attempt's journal events into the shared
// render stream. Unknown event types become AgentSpecific annotations so the
// replay never silently drops a persisted event.
func eventsToRenderEvents(events []ReviewEvent) []model.RenderEvent {
	turns := newTurnAssigner()
	out := make([]model.RenderEvent, 0, len(events))

	for i := range events {
		event := &events[i]
		base := model.RenderEvent{
			EventID:   eventID(event.Sequence),
			Seq:       event.Sequence,
			Timestamp: event.OccurredAt,
		}

		switch event.EventType {
		case "attempt.created":
			source := payloadString(event, "source")
			repository := payloadString(event, "repository_display")
			text := fmt.Sprintf("Review request: %s of %s (attempt %s)", source, repository, event.AttemptID)
			out = append(out, withTurn(base, 0, "UserPrompt", text))

		case "stage.started":
			stage := payloadString(event, "stage")
			turn := turns.forStage(stage)
			inv := withTurn(base, turn, "ToolInvocation", "")
			inv.ToolName = "stage:" + stage
			inv.ToolCallID = "stage:" + stage
			inv.ToolInput = map[string]any{"stage": stage}
			out = append(out, inv)

		case "stage.completed":
			stage := payloadString(event, "stage")
			result := withTurn(base, turns.forStage(stage), "ToolResult", "")
			result.ToolCallID = "stage:" + stage
			result.ExitCode = 0
			result.DurationMs = elapsedMs(event)
			out = append(out, result)

		case "stage.failed":
			stage := payloadString(event, "stage")
			result := withTurn(base, turns.forStage(stage), "ToolResult", "")
			result.ToolCallID = "stage:" + stage
			result.ExitCode = 1
			result.DurationMs = elapsedMs(event)
			result.ErrorKind = payloadString(event, "error_category")
			result.Stderr = payloadString(event, "safe_detail")
			out = append(out, result)

		case "dimension.started":
			dimension := payloadString(event, "dimension_id")
			inv := withTurn(base, turns.current(), "ToolInvocation", "")
			inv.ToolName = "dimension:" + dimension
			inv.ToolCallID = "dimension:" + dimension
			inv.ToolInput = map[string]any{"dimension_id": dimension}
			out = append(out, inv)

		case "dimension.completed":
			dimension := payloadString(event, "dimension_id")
			result := withTurn(base, turns.current(), "ToolResult", "")
			result.ToolCallID = "dimension:" + dimension
			result.ExitCode = 0
			result.DurationMs = elapsedMs(event)
			out = append(out, result)

		case "dimension.failed":
			dimension := payloadString(event, "dimension_id")
			result := withTurn(base, turns.current(), "ToolResult", "")
			result.ToolCallID = "dimension:" + dimension
			result.ExitCode = 1
			result.ErrorKind = payloadString(event, "error_category")
			result.Stderr = payloadString(event, "safe_detail")
			out = append(out, result)

		case "provider_call.started":
			provider := payloadString(event, "provider")
			modelName := payloadString(event, "model")
			inv := withTurn(base, turns.current(), "ToolInvocation", "")
			inv.ToolName = "provider:" + provider
			inv.ToolCallID = providerCallID(event)
			inv.ToolInput = map[string]any{
				"provider":     provider,
				"model":        modelName,
				"dimension_id": payloadString(event, "dimension_id"),
			}
			out = append(out, inv)

		case "provider_call.completed":
			result := withTurn(base, turns.current(), "ToolResult", "")
			result.ToolCallID = providerCallID(event)
			result.ExitCode = 0
			if latency, ok := payloadFloat(event, "latency_seconds"); ok {
				result.DurationMs = int64(latency * 1000)
			}
			input, inputOK := payloadInt(event, "input_tokens")
			output, outputOK := payloadInt(event, "output_tokens")
			if inputOK || outputOK {
				result.TokenUsage = &model.RenderTokenUsage{}
				if inputOK {
					result.TokenUsage.InputTokens = input
				}
				if outputOK {
					result.TokenUsage.OutputTokens = output
				}
			}
			parts := []string{}
			// Only recorded token counts appear in the summary text; an
			// unrecorded side is absent, never rendered as zero.
			switch {
			case inputOK && outputOK:
				parts = append(parts, fmt.Sprintf("tokens in=%d out=%d", input, output))
			case inputOK:
				parts = append(parts, fmt.Sprintf("tokens in=%d", input))
			case outputOK:
				parts = append(parts, fmt.Sprintf("tokens out=%d", output))
			}
			if cost, ok := payloadFloat(event, "cost_usd"); ok {
				parts = append(parts, fmt.Sprintf("cost=$%.4f", cost))
			}
			if kind := payloadString(event, "usage_kind"); kind != "" {
				parts = append(parts, "usage="+kind)
			}
			result.Stdout = strings.Join(parts, " ")
			out = append(out, result)

			// Child agent session linkage is emitted only when the writer
			// recorded both identifiers; never inferred from timing or the
			// model name (design 5.4).
			childType := payloadString(event, "child_agent_type")
			childID := payloadString(event, "child_session_id")
			if childType != "" && childID != "" {
				out = append(out, withSubtype(base, turns.current(), "child_session",
					fmt.Sprintf("Child agent session: %s %s", childType, childID)))
			}

		case "provider_call.failed":
			result := withTurn(base, turns.current(), "ToolResult", "")
			result.ToolCallID = providerCallID(event)
			result.ExitCode = 1
			result.ErrorKind = payloadString(event, "error_category")
			result.Stderr = payloadString(event, "safe_detail")
			out = append(out, result)

		case "call_plan.ready":
			calls, _ := payloadInt(event, "call_count")
			estimatedInput, hasEstimate := payloadInt(event, "estimated_input_tokens")
			text := fmt.Sprintf("Call plan ready: %d provider call(s) planned", calls)
			if hasEstimate {
				text += fmt.Sprintf(", estimated input %d tokens", estimatedInput)
			}
			out = append(out, withSubtype(base, turns.current(), "call_plan", text))

		case "finding.drafted":
			text := fmt.Sprintf("Draft finding %s (%s, %s) — not verified evidence",
				payloadString(event, "fingerprint"),
				payloadString(event, "severity"),
				payloadString(event, "dimension_id"))
			out = append(out, withSubtype(base, turns.current(), "finding_drafted", text))

		case "finding.verified":
			text := fmt.Sprintf("Verified finding %s [%s] severity=%s dimension=%s",
				payloadString(event, "fingerprint"),
				payloadString(event, "evidence_band"),
				payloadString(event, "severity"),
				payloadString(event, "dimension_id"))
			out = append(out, withSubtype(base, turns.current(), "finding_verified", text))

		case "coverage.recorded":
			summary := payloadString(event, "summary")
			if summary == "" {
				reviewed, _ := payloadInt(event, "reviewed")
				missing, _ := payloadInt(event, "missing")
				summary = fmt.Sprintf("coverage reviewed=%d missing=%d", reviewed, missing)
			}
			out = append(out, withSubtype(base, turns.current(), "coverage", "Coverage: "+summary))

		case "gate.evaluated":
			gate := payloadString(event, "gate_state")
			blocking := payloadStringSlice(event, "blocking_fingerprints")
			text := fmt.Sprintf("Gate: %s", gate)
			if len(blocking) > 0 {
				text += fmt.Sprintf(" — %d blocking finding(s)", len(blocking))
			}
			out = append(out, withTurn(base, turns.current(), "TextChunk", text))

		case "attempt.completed":
			gate := payloadString(event, "gate_state")
			out = append(out, withSubtype(base, turns.current(), "attempt_completed",
				fmt.Sprintf("Review completed — Gate %s", gate)))

		case "attempt.failed":
			out = append(out, withSubtype(base, turns.current(), "attempt_failed",
				"Review failed — "+payloadString(event, "safe_detail")))

		default:
			out = append(out, withSubtype(base, turns.current(), "unmapped_event",
				fmt.Sprintf("%s (unmapped event type)", event.EventType)))
		}
	}
	return out
}

func providerCallID(event *ReviewEvent) string {
	if ordinal, ok := payloadInt(event, "call_ordinal"); ok {
		return fmt.Sprintf("provider-call-%d", ordinal)
	}
	return fmt.Sprintf("provider-call-seq-%d", event.Sequence)
}

func withTurn(base model.RenderEvent, turn int, eventType, text string) model.RenderEvent {
	base.TurnIndex = turn
	base.Type = eventType
	base.Text = text
	return base
}

func withSubtype(base model.RenderEvent, turn int, subtype, text string) model.RenderEvent {
	event := withTurn(base, turn, "AgentSpecific", text)
	event.Subtype = subtype
	return event
}
