package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"session-insight/internal/model"
	"session-insight/internal/render"
)

// RenderANSI implements reader.BaseSessionReader. It resolves the JSONL
// file for session id, parses it (including any spliced-in subagent
// transcripts), and formats the result as an ANSI terminal text block via
// the Phase 2 renderer.
//
// This is a thin convenience wrapper so the HTTP layer doesn't need to know
// how to find a Claude session's file on disk — that stays encapsulated
// here, same as GetSession already does for the JSON API.
func (r *ClaudeReader) RenderANSI(id string) (string, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return "", fmt.Errorf("claude session not found: %s", id)
	}
	events, _, err := ParseClaudeRenderEventsWithSubagents(path)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events), nil
}

// ---- embedded event from user content ----

type userEmbedded struct {
	command   string
	commandID string
	stdout    string
	stderr    string
}

// ---- public API ----

// ParseClaudeRenderEvents parses one Claude Code JSONL transcript into a flat
// []model.RenderEvent stream.
//
// baseDepth and parentEventID exist to support subagent ("sidechain")
// transcripts. In real Claude Code data, a subagent's conversation is NOT
// inlined into the main session file with isSidechain:true markers — it
// lives in a completely separate file at
// "<session-dir>/subagents/agent-<id>.jsonl", with a sibling
// "agent-<id>.meta.json" that carries the "toolUseId" linking it back to the
// "Agent" tool_use block in the main file. (Verified against real session
// data; see integration notes below.)
//
// To render a subagent transcript as a nested branch, the caller (in
// claude.go's GetSession orchestration — not yet wired up, see report) is
// expected to:
//  1. Parse the main file with baseDepth=0, parentEventID="".
//  2. For each "Agent"-named ToolInvocation event found, look up the
//     matching subagent file via its meta.json's toolUseId == ToolCallID.
//  3. Parse that subagent file with baseDepth=1 and parentEventID set to the
//     EventID of the matching ToolInvocation event.
//
// Within a single call, every event in the file is assumed to be at the same
// depth (the whole file is either "main" or "one subagent's transcript") —
// there is no per-line depth switching, because real data doesn't interleave
// the two within one file.
func ParseClaudeRenderEvents(path string, baseDepth int, parentEventID string) ([]model.RenderEvent, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	// Namespaces fallback IDs (used when a JSONL line has no uuid) by the
	// file they came from. Without this, two separate calls to this
	// function — e.g. once for the main session file and once per spliced
	// subagent file in ParseClaudeRenderEventsWithSubagents — would each
	// restart eventCtr at 0 and could emit identical fallback EventIDs
	// (e.g. "evt-0000-boundary" from both), corrupting EventID/ParentEventID
	// resolution once merged into one stream.
	fileTag := strings.TrimSuffix(filepath.Base(path), ".jsonl")

	var (
		events     []model.RenderEvent
		foundModel string
		eventCtr   int
		turnIndex  int

		streamType  string // "", "thinking", "text"
		streamIdx   int
		streamID    string
		streamSeq   int
		streamFirst string

		// Two separate FIFO queues: Agent (subagent/Task) calls can run for
		// a long time, so their ToolResult routinely arrives in the JSONL
		// file AFTER results from other, faster tool calls issued later.
		// Mixing them into one FIFO mismatches results to the wrong
		// invocation as soon as anything completes out of order — which
		// real session data confirms happens routinely. evt.ToolUseResult
		// carries a non-empty AgentID exactly when it's wrapping a subagent
		// completion, so that's used to pick which queue to pop.
		pendingToolIDs      []string
		pendingAgentToolIDs []string
	)

	emit := func(evt model.RenderEvent) string {
		if evt.EventID == "" {
			evt.EventID = fmt.Sprintf("evt-%s-%04d", fileTag, eventCtr)
			eventCtr++
		}
		if evt.AgentType == "" {
			evt.AgentType = "claude"
		}
		evt.Depth = baseDepth
		events = append(events, evt)
		return evt.EventID
	}

	// flushStream ends whatever stream (thinking or text) is currently open.
	// Must be called whenever a stream boundary is crossed: a new turn
	// starts, or one assistant-message's content blocks have all been
	// processed. Without the latter, two separate assistant JSONL lines in
	// the same turn (e.g. text -> tool_use -> text after the tool result)
	// would incorrectly be stitched into one continuous StreamID.
	flushStream := func() {
		if streamType == "thinking" && streamSeq > 0 {
			emit(model.RenderEvent{
				EventID:       fmt.Sprintf("%s-end", streamID),
				ParentEventID: streamFirst,
				StreamID:      streamID,
				Type:          "ThinkingEnd",
				TurnIndex:     turnIndex - 1,
			})
		}
		streamType = ""
		streamSeq = 0
		streamID = ""
		streamFirst = ""
	}

	makeTokenUsage := func(u *claudeUsage) *model.RenderTokenUsage {
		if u == nil {
			return nil
		}
		return &model.RenderTokenUsage{
			InputTokens:         u.InputTokens,
			OutputTokens:        u.OutputTokens,
			CacheReadTokens:     u.CacheReadInputTokens,
			CacheCreationTokens: u.CacheCreationInputTokens,
		}
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var evt claudeEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}
		if evt.IsMeta {
			continue
		}
		// Defensive: real data never interleaves sidechain lines into a
		// main-file parse (baseDepth==0), but guard against it rather than
		// silently mis-attributing depth if some other Claude Code version
		// ever does.
		if evt.IsSidechain && baseDepth == 0 {
			continue
		}

		ts := parseTS(evt.Timestamp)
		evtUUID := evt.UUID
		if evtUUID == "" {
			evtUUID = fmt.Sprintf("evt-%s-%04d", fileTag, eventCtr)
			eventCtr++
		}

		switch {
		// ---- user message: new turn ----
		case evt.Type == "user" && evt.ToolUseResult == nil && evt.Message != nil:
			flushStream()

			turnIndex++

			boundaryID := emit(model.RenderEvent{
				EventID:       fmt.Sprintf("%s-boundary", evtUUID),
				ParentEventID: parentEventID,
				Type:          "TurnBoundary",
				Timestamp:     ts,
				TurnIndex:     turnIndex - 1,
				Model:         foundModel,
				Metadata:      makeMetadata(evt),
			})

			cleanText, embedded := parseUserContent(evt.Message.contentString())

			promptID := emit(model.RenderEvent{
				EventID:       fmt.Sprintf("%s-prompt", evtUUID),
				ParentEventID: boundaryID,
				Type:          "UserPrompt",
				Timestamp:     ts,
				TurnIndex:     turnIndex - 1,
				Text:          cleanText,
			})

			for i, emb := range embedded {
				invID := fmt.Sprintf("%s-bash-%d", evtUUID, i)
				emit(model.RenderEvent{
					EventID:       invID,
					ParentEventID: promptID,
					Type:          "ToolInvocation",
					Timestamp:     ts,
					TurnIndex:     turnIndex - 1,
					ToolName:      "Bash",
					ToolCallID:    emb.commandID,
					ToolInput:     map[string]any{"command": emb.command},
				})

				if emb.stdout != "" {
					emit(model.RenderEvent{
						EventID:       fmt.Sprintf("%s-bash-%d-result", evtUUID, i),
						ParentEventID: invID,
						Type:          "ToolResult",
						Timestamp:     ts,
						TurnIndex:     turnIndex - 1,
						Stdout:        emb.stdout,
						ToolCallID:    emb.commandID,
					})
				}
				if emb.stderr != "" {
					emit(model.RenderEvent{
						EventID:       fmt.Sprintf("%s-bash-%d-stderr", evtUUID, i),
						ParentEventID: invID,
						Type:          "ToolResult",
						Timestamp:     ts,
						TurnIndex:     turnIndex - 1,
						Stderr:        emb.stderr,
						ToolCallID:    emb.commandID,
						ExitCode:      1,
					})
				}
			}

		// ---- tool result ----
		// NOTE (flagged, not fixed): matched FIFO against pendingToolIDs /
		// pendingAgentToolIDs. claudeToolResult does not carry the real
		// tool_use_id from the JSONL, so we cannot do exact matching for the
		// general case. FIFO is correct as long as tool results return in
		// the same order their tool_use blocks were issued.
		//
		// Agent (subagent/Task) calls get their own queue (see
		// pendingAgentToolIDs above) specifically because they violate this
		// assumption routinely in real data: a subagent can run for minutes
		// while other, faster Bash/Read/Edit calls from later turns
		// complete and have their results logged first. Mixing them into
		// one FIFO caused a confirmed mismatch (Agent completion attributed
		// to an unrelated Edit/Read invocation) — see
		// adapter_subagent_test.go. Splitting the queue by call type fixes
		// the cases observed in real data, but still assumes "no two
		// concurrent Agent calls complete out of order relative to each
		// other" and "no two concurrent non-Agent calls complete out of
		// order relative to each other" — true for every sample inspected,
		// not proven in general.
		case evt.Type == "user" && evt.ToolUseResult != nil:
			flushStream()

			toolCallID := ""
			if evt.ToolUseResult.AgentID != "" {
				if len(pendingAgentToolIDs) > 0 {
					toolCallID = pendingAgentToolIDs[0]
					pendingAgentToolIDs = pendingAgentToolIDs[1:]
				}
			} else if len(pendingToolIDs) > 0 {
				toolCallID = pendingToolIDs[0]
				pendingToolIDs = pendingToolIDs[1:]
			}

			exitCode := 0
			if evt.ToolUseResult.IsError || evt.ToolUseResult.Stderr != "" {
				exitCode = 1
			}

			var payload map[string]any
			if evt.ToolUseResult.AgentID != "" {
				// Marks this ToolResult as the wrapper for a subagent run.
				// ParseClaudeRenderEventsWithSubagents looks for this to
				// find and splice in "subagents/agent-<id>.jsonl".
				payload = map[string]any{"agent_id": evt.ToolUseResult.AgentID}
			}

			emit(model.RenderEvent{
				EventID:       fmt.Sprintf("%s-toolresult", evtUUID),
				ParentEventID: toolCallID, // ToolCallID == the ToolInvocation's EventID (both set from block.ID)
				Type:          "ToolResult",
				Timestamp:     ts,
				TurnIndex:     turnIndex - 1,
				Stdout:        evt.ToolUseResult.Stdout,
				Stderr:        evt.ToolUseResult.Stderr,
				ExitCode:      exitCode,
				ToolCallID:    toolCallID,
				Payload:       payload,
			})

		// ---- assistant message ----
		case evt.Type == "assistant" && evt.Message != nil:
			msg := evt.Message

			if foundModel == "" && msg.Model != "" {
				foundModel = msg.Model
			}

			// Attach the message-level TokenUsage to exactly one event per
			// assistant message (the first block emitted), not to every
			// block. The draft attached the full message usage to every
			// thinking/text/tool_use block from the same message, which
			// would multiply-count input/output tokens if the Token
			// Analysis panel (module 9 of the plan) sums TokenUsage across
			// events in a turn.
			tokenUsage := makeTokenUsage(msg.Usage)
			usageAttached := false
			nextUsage := func() *model.RenderTokenUsage {
				if usageAttached || tokenUsage == nil {
					return nil
				}
				usageAttached = true
				return tokenUsage
			}

			blocks := msg.contentBlocks()
			for bi, block := range blocks {
				switch block.Type {
				case "thinking":
					if streamType != "thinking" {
						streamType = "thinking"
						streamIdx++
						streamID = fmt.Sprintf("%s-stream-thinking-%d", evtUUID, streamIdx)
						streamSeq = 1

						eid := emit(model.RenderEvent{
							EventID:    fmt.Sprintf("%s-think-start", streamID),
							StreamID:   streamID,
							Seq:        streamSeq,
							Type:       "ThinkingStart",
							Timestamp:  ts,
							TurnIndex:  turnIndex - 1,
							Text:       block.Thinking,
							TokenUsage: nextUsage(),
						})
						streamFirst = eid
					} else {
						streamSeq++
						emit(model.RenderEvent{
							EventID:       fmt.Sprintf("%s-think-%d", streamID, streamSeq),
							ParentEventID: streamFirst,
							StreamID:      streamID,
							Seq:           streamSeq,
							Type:          "ThinkingChunk",
							Timestamp:     ts,
							TurnIndex:     turnIndex - 1,
							Text:          block.Thinking,
							TokenUsage:    nextUsage(),
						})
					}

				case "text":
					if streamType == "thinking" {
						flushStream()
					}
					if streamType != "text" {
						streamType = "text"
						streamIdx++
						streamID = fmt.Sprintf("%s-stream-text-%d", evtUUID, streamIdx)
						streamSeq = 1
					} else {
						streamSeq++
					}

					eid := emit(model.RenderEvent{
						EventID:    fmt.Sprintf("%s-text-%d", streamID, streamSeq),
						StreamID:   streamID,
						Seq:        streamSeq,
						Type:       "TextChunk",
						Timestamp:  ts,
						TurnIndex:  turnIndex - 1,
						Text:       block.Text,
						TokenUsage: nextUsage(),
					})
					if streamFirst == "" {
						streamFirst = eid
					}

				case "tool_use":
					if streamType != "" {
						flushStream()
					}

					toolInput := make(map[string]any)
					if block.Input != nil {
						_ = json.Unmarshal(block.Input, &toolInput)
					}

					invID := block.ID
					if invID == "" {
						invID = fmt.Sprintf("%s-tool-%d", evtUUID, bi)
					}

					emit(model.RenderEvent{
						EventID:    invID,
						Type:       "ToolInvocation",
						Timestamp:  ts,
						TurnIndex:  turnIndex - 1,
						ToolName:   block.Name,
						ToolCallID: block.ID,
						ToolInput:  toolInput,
						TokenUsage: nextUsage(),
					})

					// Queue invID (the event's actual EventID), not block.ID:
					// when block.ID is empty, invID is the generated
					// fallback, and the later ToolResult match must resolve
					// to that fallback, not to an empty string that links
					// to nothing.
					if block.Name == "Agent" {
						pendingAgentToolIDs = append(pendingAgentToolIDs, invID)
					} else {
						pendingToolIDs = append(pendingToolIDs, invID)
					}
				}
			}

			// End whatever stream was left open by this message before the
			// next JSONL line (which might be a tool result, then another
			// assistant message) is processed.
			flushStream()

		// ---- system events ----
		case evt.Type == "system":
			switch evt.Subtype {
			case "turn_duration":
				emit(model.RenderEvent{
					EventID:    fmt.Sprintf("%s-system", evtUUID),
					Type:       "AgentSpecific",
					Subtype:    "turn_duration",
					Timestamp:  ts,
					TurnIndex:  turnIndex - 1,
					DurationMs: evt.DurationMs,
				})
			}
		}
	}

	flushStream()

	return dropEmptyTurns(events), foundModel, scanner.Err()
}

// dropEmptyTurns removes TurnBoundary+UserPrompt pairs for turns that carry
// no real content — e.g. a trailing empty user message at the end of a
// transcript. Without this, real session data renders visible turns with
// nothing but a bare "> " prompt and no response (confirmed via an
// end-to-end request against real data during Phase 2 review).
//
// This mirrors the old EventVM-based parser's filterEmptyTurns (claude.go),
// which the RenderEvent pipeline never had an equivalent for. Implemented
// as a post-processing pass here, rather than inline during scanning,
// because emission is streaming/eager and a turn's "is it empty" verdict
// can only be known once all of its events have been seen.
func dropEmptyTurns(events []model.RenderEvent) []model.RenderEvent {
	hasContent := make(map[int]bool)
	for _, e := range events {
		switch e.Type {
		case "TurnBoundary":
			// never itself counts as content
		case "UserPrompt":
			if strings.TrimSpace(e.Text) != "" {
				hasContent[e.TurnIndex] = true
			}
		case "AgentSpecific":
			// A bare turn_duration marker (the only AgentSpecific subtype
			// emitted unconditionally, regardless of whether the turn did
			// anything) shouldn't by itself save an otherwise-empty turn —
			// matches filterEmptyTurns' duration-agnostic semantics.
			if e.Subtype != "turn_duration" {
				hasContent[e.TurnIndex] = true
			}
		default:
			hasContent[e.TurnIndex] = true
		}
	}

	filtered := make([]model.RenderEvent, 0, len(events))
	for _, e := range events {
		if (e.Type == "TurnBoundary" || e.Type == "UserPrompt") && !hasContent[e.TurnIndex] {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

// ---- subagent stitching ----

// ParseClaudeRenderEventsWithSubagents parses mainPath and, for every Agent
// (subagent/Task) tool call found, splices in that subagent's full
// transcript as a nested (Depth+1) branch.
//
// Real Claude Code data lays subagent transcripts out as:
//
//	<session-dir>/<sessionID>.jsonl          (main file, passed as mainPath)
//	<session-dir>/<sessionID>/subagents/agent-<agentID>.jsonl
//
// The main file's wrapping ToolResult for an "Agent" tool_use carries
// toolUseResult.agentId, which is exactly <agentID> above (verified against
// real session data — also cross-checked against the sibling
// agent-<agentID>.meta.json's "toolUseId", which points back to the same
// tool_use.id). We use the ToolResult.Payload["agent_id"] set by
// ParseClaudeRenderEvents to find the join, rather than reading meta.json,
// since it's already resolved during the FIFO tool_use/tool_result match.
//
// Limitation: only one level of nesting is handled. Real data inspected so
// far never shows a "subagents/" directory nested inside another
// "subagents/" directory, and Claude Code's own layout gives no path for
// where a sub-subagent's transcript would live, so deeper nesting is
// unsupported rather than guessed at.
func ParseClaudeRenderEventsWithSubagents(mainPath string) ([]model.RenderEvent, string, error) {
	mainEvents, modelName, err := ParseClaudeRenderEvents(mainPath, 0, "")
	if err != nil {
		return nil, "", err
	}

	sessionDir := strings.TrimSuffix(mainPath, ".jsonl")
	subagentsDir := filepath.Join(sessionDir, "subagents")
	if info, err := os.Stat(subagentsDir); err != nil || !info.IsDir() {
		return mainEvents, modelName, nil
	}

	merged := make([]model.RenderEvent, 0, len(mainEvents))
	for _, e := range mainEvents {
		if e.Type == "ToolResult" && e.Payload != nil {
			if agentID, ok := e.Payload["agent_id"].(string); ok && agentID != "" {
				// agentID comes straight from JSONL content (the JSONL file
				// is a session transcript, not fully trusted input). Reject
				// anything that isn't a plain filename component before
				// building a path from it — filepath.Join does not stop a
				// segment like "../../etc/passwd" from escaping
				// subagentsDir.
				if filepath.Base(agentID) != agentID {
					merged = append(merged, model.RenderEvent{
						EventID:       e.EventID + "-subagent-rejected",
						ParentEventID: e.ParentEventID,
						Type:          "AgentSpecific",
						Subtype:       "subagent_load_error",
						Depth:         e.Depth,
						TurnIndex:     e.TurnIndex,
						Payload:       map[string]any{"reason": "unsafe agent_id", "agent_id": agentID},
					})
					merged = append(merged, e)
					continue
				}

				subPath := filepath.Join(subagentsDir, fmt.Sprintf("agent-%s.jsonl", agentID))
				if _, statErr := os.Stat(subPath); statErr == nil {
					subEvents, _, subErr := ParseClaudeRenderEvents(subPath, e.Depth+1, e.ParentEventID)
					if subErr == nil {
						// Splice the subagent's full transcript in before
						// the summary ToolResult line, so reading order is:
						// ToolInvocation(Agent) -> [subagent transcript] ->
						// ToolResult(Agent summary).
						merged = append(merged, subEvents...)
					} else {
						// Surface the failure as a render event instead of
						// silently dropping the subagent's transcript —
						// otherwise the UI/decoration layer has no way to
						// tell "no subagent ran" apart from "subagent ran
						// but we failed to load its transcript".
						merged = append(merged, model.RenderEvent{
							EventID:       e.EventID + "-subagent-error",
							ParentEventID: e.ParentEventID,
							Type:          "AgentSpecific",
							Subtype:       "subagent_load_error",
							Depth:         e.Depth,
							TurnIndex:     e.TurnIndex,
							Payload:       map[string]any{"reason": subErr.Error(), "agent_id": agentID},
						})
					}
				}
			}
		}
		merged = append(merged, e)
	}

	return merged, modelName, nil
}

// ---- user content parsing ----

var (
	bashInputRe  = regexp.MustCompile(`(?s)<bash-input>(.*?)</bash-input>`)
	bashStdoutRe = regexp.MustCompile(`(?s)<bash-stdout>(.*?)</bash-stdout>`)
	bashStderrRe = regexp.MustCompile(`(?s)<bash-stderr>(.*?)</bash-stderr>`)
)

func parseUserContent(s string) (cleanText string, embedded []userEmbedded) {
	result := s

	inputs := bashInputRe.FindAllStringSubmatch(result, -1)
	if len(inputs) > 0 {
		extracted := make([]userEmbedded, 0, len(inputs))
		for i, m := range inputs {
			extracted = append(extracted, userEmbedded{
				command:   strings.TrimSpace(m[1]),
				commandID: fmt.Sprintf("bash-%d", i),
			})
		}

		stdouts := bashStdoutRe.FindAllStringSubmatch(result, -1)
		for i, m := range stdouts {
			if i < len(extracted) {
				extracted[i].stdout = strings.TrimSpace(m[1])
			}
		}

		stderrs := bashStderrRe.FindAllStringSubmatch(result, -1)
		for i, m := range stderrs {
			if i < len(extracted) {
				extracted[i].stderr = strings.TrimSpace(m[1])
			}
		}

		embedded = extracted

		result = bashInputRe.ReplaceAllString(result, "")
		result = bashStdoutRe.ReplaceAllString(result, "")
		result = bashStderrRe.ReplaceAllString(result, "")
	}

	for _, tag := range []string{
		"command-name", "command-message", "command-args",
		"local-command-stdout", "local-command-caveat",
	} {
		result = stripTag(result, tag)
	}

	return strings.TrimSpace(result), embedded
}

// ---- helpers ----

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func makeMetadata(evt claudeEvent) map[string]any {
	m := make(map[string]any)
	if evt.CWD != "" {
		m["cwd"] = evt.CWD
	}
	if evt.GitBranch != "" {
		m["git_branch"] = evt.GitBranch
	}
	if evt.Version != "" {
		m["version"] = evt.Version
	}
	if evt.SessionID != "" {
		m["session_id"] = evt.SessionID
	}
	if len(m) == 0 {
		return nil
	}
	return m
}
