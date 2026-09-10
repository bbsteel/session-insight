package worktreereview

import (
	"fmt"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
)

// buildDetail assembles the unified SessionDetail for one attempt: a request
// turn, one turn per pipeline stage with dimensions/provider calls/findings
// as tool details, the gate decision as the final assistant message, and a
// billing aggregate from provider_call.completed events (result.json usage
// fills in only when no call-level token evidence exists).
func buildDetail(session model.Session, view *attemptView) *model.SessionDetail {
	turns := buildTurns(view.events)
	session.TurnCount = len(turns)
	session.MessageCount = len(view.events)
	session.ModelName, session.ModelProvider = modelEvidence(view.events)

	detail := &model.SessionDetail{
		Session:    session,
		Turns:      turns,
		Provenance: buildProvenance(view),
	}
	detail.Billing = buildBilling(view)
	return detail
}

func buildTurns(events []ReviewEvent) []model.TurnVM {
	turns := []model.TurnVM{}
	byStage := map[string]int{}

	newTurn := func(user, assistant string) int {
		turns = append(turns, model.TurnVM{
			TurnIndex:        len(turns),
			UserMessage:      user,
			AssistantMessage: assistant,
		})
		return len(turns) - 1
	}

	stageTurn := func(stage string) int {
		if index, ok := byStage[stage]; ok {
			return index
		}
		index := newTurn(fmt.Sprintf("Pipeline stage: %s", stage), "")
		byStage[stage] = index
		return index
	}

	currentTurn := 0

	for i := range events {
		event := &events[i]
		switch event.EventType {
		case "attempt.created":
			currentTurn = newTurn(
				fmt.Sprintf("Review %s of %s (attempt %s)",
					payloadString(event, "source"),
					payloadString(event, "repository_display"),
					event.AttemptID),
				"",
			)

		case "stage.started":
			currentTurn = stageTurn(payloadString(event, "stage"))

		case "stage.completed":
			stage := payloadString(event, "stage")
			turn := stageTurn(stage)
			currentTurn = turn
			turns[turn].ToolDetails = append(turns[turn].ToolDetails, model.ToolCallVM{
				Name:     "stage:" + stage,
				Duration: elapsedMs(event),
			})

		case "stage.failed":
			stage := payloadString(event, "stage")
			turn := stageTurn(stage)
			currentTurn = turn
			turns[turn].ToolDetails = append(turns[turn].ToolDetails, model.ToolCallVM{
				Name:         "stage:" + stage,
				ExitCode:     1,
				Duration:     elapsedMs(event),
				ErrorKind:    payloadString(event, "error_category"),
				ErrorMessage: payloadString(event, "safe_detail"),
			})
			turns[turn].ErrorCount++

		case "dimension.started":
			// dimensions open on the current (run-dimensions) turn

		case "dimension.completed", "dimension.failed":
			dimension := payloadString(event, "dimension_id")
			detail := model.ToolCallVM{
				Name:     "dimension:" + dimension,
				Duration: elapsedMs(event),
			}
			if event.EventType == "dimension.failed" {
				detail.ExitCode = 1
				detail.ErrorKind = payloadString(event, "error_category")
				detail.ErrorMessage = payloadString(event, "safe_detail")
				turns[currentTurn].ErrorCount++
			}
			turns[currentTurn].ToolDetails = append(turns[currentTurn].ToolDetails, detail)
			turns[currentTurn].ToolCallCount++

		case "provider_call.completed":
			call := model.ToolCallVM{
				Name: "provider:" + payloadString(event, "provider"),
			}
			if latency, ok := payloadFloat(event, "latency_seconds"); ok {
				call.Duration = int64(latency * 1000)
			}
			turns[currentTurn].ToolDetails = append(turns[currentTurn].ToolDetails, call)
			turns[currentTurn].ToolCallCount++
			turns[currentTurn].RequestCount++
			if input, ok := payloadInt(event, "input_tokens"); ok {
				turns[currentTurn].TokenUsage.PromptTokens += input
				turns[currentTurn].TokenUsage.Present.Input = model.PresenceExact
			}
			if output, ok := payloadInt(event, "output_tokens"); ok {
				turns[currentTurn].TokenUsage.CompletionTokens += output
				turns[currentTurn].TokenUsage.Present.Output = model.PresenceExact
			}

		case "provider_call.failed":
			turns[currentTurn].ToolDetails = append(turns[currentTurn].ToolDetails, model.ToolCallVM{
				Name:         "provider:" + payloadString(event, "provider"),
				ExitCode:     1,
				ErrorKind:    payloadString(event, "error_category"),
				ErrorMessage: payloadString(event, "safe_detail"),
			})
			turns[currentTurn].ErrorCount++

		case "finding.verified":
			turns[currentTurn].ToolDetails = append(turns[currentTurn].ToolDetails, model.ToolCallVM{
				Name: fmt.Sprintf("finding:%s", payloadString(event, "severity")),
			})

		case "gate.evaluated":
			gate := payloadString(event, "gate_state")
			blocking := payloadStringSlice(event, "blocking_fingerprints")
			text := fmt.Sprintf("Gate: %s", gate)
			if len(blocking) > 0 {
				text += fmt.Sprintf(" — %d blocking finding(s): %s", len(blocking), strings.Join(blocking, ", "))
			}
			turns[currentTurn].AssistantMessage = text

		case "attempt.completed":
			turns[currentTurn].AssistantMessage = strings.TrimSpace(
				turns[currentTurn].AssistantMessage + "\nReview completed.")
			if gate := payloadString(event, "gate_state"); gate != "" &&
				!strings.Contains(turns[currentTurn].AssistantMessage, gate) {
				turns[currentTurn].AssistantMessage += fmt.Sprintf(" Gate: %s.", gate)
			}

		case "attempt.failed":
			turns[currentTurn].AssistantMessage = strings.TrimSpace(
				turns[currentTurn].AssistantMessage + "\nReview failed: " + payloadString(event, "safe_detail"))
			turns[currentTurn].ErrorCount++
		}
	}
	return turns
}

// modelEvidence reports provider/model only from recorded provider_call
// events — never inferred from names or timing.
func modelEvidence(events []ReviewEvent) (modelName, provider string) {
	for i := range events {
		if events[i].EventType != "provider_call.completed" && events[i].EventType != "provider_call.started" {
			continue
		}
		if m := payloadString(&events[i], "model"); m != "" {
			modelName = m
		}
		if p := payloadString(&events[i], "provider"); p != "" {
			provider = p
		}
		if modelName != "" && provider != "" {
			return modelName, provider
		}
	}
	return "", ""
}

// buildBilling aggregates recorded provider-call usage. Precision is exact
// only when every recorded call carried cost; any unknown cost downgrades to
// estimated, and sessions without any token evidence get no billing block.
func buildBilling(view *attemptView) *model.SessionBilling {
	var totals model.TokenUsage
	var amount float64
	calls := 0
	costUnknown := false

	for i := range view.events {
		event := &view.events[i]
		if event.EventType != "provider_call.completed" {
			continue
		}
		calls++
		if input, ok := payloadInt(event, "input_tokens"); ok {
			totals.PromptTokens += input
			totals.Present.Input = model.PresenceExact
		}
		if output, ok := payloadInt(event, "output_tokens"); ok {
			totals.CompletionTokens += output
			totals.Present.Output = model.PresenceExact
		}
		if cost, ok := payloadFloat(event, "cost_usd"); ok {
			amount += cost
		} else {
			costUnknown = true
		}
	}

	if calls == 0 {
		return nil
	}
	precision := model.PrecisionExact
	if costUnknown {
		precision = model.PrecisionEstimated
	}
	return &model.SessionBilling{
		Precision:     precision,
		BillingUnit:   "usd",
		BillingAmount: amount,
		Totals:        totals,
	}
}

// buildProvenance attaches the independent record-completeness contract on
// the same read path as the body.
func buildProvenance(view *attemptView) *model.SessionProvenance {
	warnings := []model.ParseWarning{}
	if view.skippedMalformed > 0 {
		warnings = append(warnings, model.ParseWarning{
			Code:                "malformed_lines_skipped",
			Severity:            "warning",
			AffectsCompleteness: true,
			Count:               view.skippedMalformed,
			SourceRole:          model.SourceRoleEvents,
			Impacts:             []string{"replay"},
		})
	}
	if view.skippedVersion > 0 {
		warnings = append(warnings, model.ParseWarning{
			Code:                "unsupported_event_version_skipped",
			Severity:            "info",
			AffectsCompleteness: true,
			Count:               view.skippedVersion,
			SourceRole:          model.SourceRoleEvents,
			Impacts:             []string{"replay"},
		})
	}
	built := provenance.Build(provenance.Input{
		CapturedAt:        time.Now().UTC(),
		AdapterRevision:   adapterRevision,
		Sources:           view.sources,
		Warnings:          warnings,
		HasReplayableBody: len(view.events) > 0,
	})
	return &built
}
