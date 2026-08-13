package codex

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
	"github.com/bbsteel/session-insight/internal/reader/readerr"
	"github.com/bbsteel/session-insight/internal/reader/shared"
	"github.com/bbsteel/session-insight/internal/render"
)

type CodexReader struct {
	sessionsDir string
	pathsMu     sync.RWMutex
	paths       map[string]string
}

func New(sessionsDir string) *CodexReader {
	return &CodexReader{sessionsDir: sessionsDir, paths: make(map[string]string)}
}

func (r *CodexReader) WatchRoots() []string { return []string{r.sessionsDir} }

func (r *CodexReader) AgentType() string   { return "codex" }
func (r *CodexReader) DisplayName() string { return "Codex" }

// RenderANSI implements reader.BaseSessionReader.
func (r *CodexReader) GetRenderEvents(id string) ([]model.RenderEvent, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return nil, fmt.Errorf("codex session not found: %s", id)
	}
	events, err := codexToRenderEvents(path)
	if err != nil {
		return nil, err
	}
	events = shared.InterleaveToolResults(events)
	// A subagent rollout rendered through its backing Session carries the
	// child invocation ID on every event; root events stay unmarked (absent
	// InvocationID means the root invocation).
	if invID := r.childInvocationID(id); invID != "" {
		for i := range events {
			events[i].InvocationID = invID
		}
	}
	return events, nil
}

// ReadIndexSnapshot is the compatibility surface used by the current indexer.
// It delegates to the authoritative envelope so detail and render data still
// come from the same immutable source-byte view.
func (r *CodexReader) ReadIndexSnapshot(ctx context.Context, session model.Session) (*model.SessionDetail, []model.RenderEvent, error) {
	envelope, err := r.ReadIndexSnapshotEnvelope(ctx, session)
	if err != nil {
		return nil, nil, err
	}
	return envelope.Detail, envelope.RenderEvents, nil
}

// ReadIndexSnapshotEnvelope reads the rollout once, then derives every
// index-facing projection from that immutable byte slice. In particular,
// origin Git facts never consult the current repository or worktree.
func (r *CodexReader) ReadIndexSnapshotEnvelope(ctx context.Context, session model.Session) (*model.IndexSnapshotEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := r.findSessionFile(session.ID)
	if path == "" {
		readErr := readerr.New(readerr.SourceMissing, "source_missing",
			fmt.Errorf("codex session not found: %s", session.ID))
		if known := r.knownSessionFile(session.ID); known != "" {
			readErr.WithSources(sourceInventory(known))
		}
		return nil, readErr
	}
	snapshotPath, sourceFingerprint, err := captureCodexSource(ctx, path)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, readerr.New(readerr.SourceUnreadable, "source_unreadable", err).
			WithSources(sourceInventory(path)).
			WithWarnings([]model.ParseWarning{readerr.SourceUnreadableWarning(model.SourceRolePrimaryTranscript)})
	}
	defer os.Remove(snapshotPath)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sourceRevision := string(sourceFingerprint.Algorithm) + ":" + sourceFingerprint.Digest
	snapshotSession, err := codexSessionFromSnapshot(path, snapshotPath, session)
	if err != nil {
		return nil, err
	}
	events, renderSkipped, err := codexToRenderEventsSnapshot(path, snapshotPath)
	if err != nil {
		return nil, err
	}
	var detail *model.SessionDetail
	if renderEventsContainRollback(events) {
		parsed, modelName, modelProvider, detailSkipped, parseErr := parseCodexEventsFile(snapshotPath)
		if parseErr != nil {
			return nil, parseErr
		}
		if detailSkipped < renderSkipped {
			detailSkipped = renderSkipped
		}
		detail = buildCodexDetail(snapshotSession, parsed, modelName, modelProvider, path, sourceFingerprint.SizeBytes, detailSkipped)
	} else {
		detail = indexDetailFromEvents(snapshotSession, events, "", renderSkipped)
		attachAuthoritativeCodexProvenance(detail, path, sourceFingerprint.SizeBytes, renderSkipped)
	}
	origin, finalization, err := parseCodexEnvelopeEvidenceSnapshot(snapshotPath, sourceRevision)
	if err != nil {
		return nil, err
	}
	envelope := &model.IndexSnapshotEnvelope{
		Detail:            detail,
		RenderEvents:      nonNilRenderEvents(events),
		SourceRevision:    sourceRevision,
		SourceFingerprint: sourceFingerprint,
		OriginGit:         origin,
		Finalization:      finalization,
	}
	if validation := model.ValidateIndexSnapshotEnvelope(envelope); !validation.OK() {
		return nil, fmt.Errorf("invalid Codex index snapshot envelope: %+v", validation.Issues)
	}
	return envelope, nil
}

func renderEventsContainRollback(events []model.RenderEvent) bool {
	for _, event := range events {
		if event.Type == "RollbackStart" || event.Type == "RollbackEnd" || event.TurnIndex < 0 {
			return true
		}
	}
	return false
}

func buildCodexDetail(session model.Session, parsed codexParsedTurns, modelName, modelProvider, path string, sourceSize int64, skipped int) *model.SessionDetail {
	if modelName != "" {
		session.ModelName = modelName
	}
	if modelProvider != "" {
		session.ModelProvider = modelProvider
	}
	session.TurnCount = len(parsed.Active)
	session.HistoricalTurnCount = parsed.Historical
	for _, group := range parsed.RollbackGroups {
		session.RolledBackTurnCount += len(group.Turns)
	}
	detail := &model.SessionDetail{Session: session, Turns: parsed.Active, RollbackGroups: parsed.RollbackGroups}
	detail.AnomalySummary = shared.RunAnomalyDetection(parsed.Active)
	attachAuthoritativeCodexProvenance(detail, path, sourceSize, skipped)
	return detail
}

func attachAuthoritativeCodexProvenance(detail *model.SessionDetail, path string, sourceSize int64, skipped int) {
	if detail == nil || path == "" {
		return
	}
	var warnings []model.ParseWarning
	if skipped > 0 {
		warnings = append(warnings, provenance.Warning(
			model.WarnMalformedRecordSkipped, model.WarningSeverityWarning, true,
			[]string{model.ImpactReplay}, model.SourceRolePrimaryTranscript, nil, skipped,
		))
	}
	p := provenance.Build(provenance.Input{
		CapturedAt:      time.Now().UTC(),
		AdapterRevision: Capabilities().AdapterRevision,
		Sources: []model.SessionSourceFile{
			provenance.PresentSource(model.SourceRolePrimaryTranscript, filepath.Clean(path), detail.UpdatedAt, sourceSize),
		},
		Warnings:          warnings,
		HasReplayableBody: len(detail.Turns) > 0,
	})
	detail.Provenance = &p
}

// indexDetailFromEvents remains as the focused projection helper exercised by
// render tests. The authoritative envelope uses buildCodexDetail so rollback
// history is retained from the same immutable source bytes.
func indexDetailFromEvents(session model.Session, events []model.RenderEvent, path string, skipped int) *model.SessionDetail {
	turns := make([]model.TurnVM, 0)
	ensureTurn := func(index int) *model.TurnVM {
		for len(turns) <= index {
			turns = append(turns, model.TurnVM{TurnIndex: len(turns)})
		}
		return &turns[index]
	}
	for _, event := range events {
		if event.TurnIndex < 0 {
			continue
		}
		turn := ensureTurn(event.TurnIndex)
		switch event.Type {
		case "UserPrompt":
			if event.Text != "" {
				turn.UserMessage = event.Text
			}
		case "TextChunk":
			turn.AssistantMessage += event.Text
		case "ToolInvocation":
			turn.ToolCallCount++
			if event.ToolName != "" {
				turn.ToolNames = append(turn.ToolNames, event.ToolName)
			}
		case "ToolResult":
			if event.ExitCode != 0 || event.Stderr != "" || event.ErrorKind != "" || event.Rejected {
				turn.ErrorCount++
			}
		}
	}
	session.TurnCount = len(turns)
	detail := &model.SessionDetail{Session: session, Turns: turns}
	detail.AnomalySummary = shared.RunAnomalyDetection(turns)
	if path != "" {
		var warnings []model.ParseWarning
		if skipped > 0 {
			warnings = append(warnings, provenance.Warning(
				model.WarnMalformedRecordSkipped, model.WarningSeverityWarning, true,
				[]string{model.ImpactReplay}, model.SourceRolePrimaryTranscript, nil, skipped,
			))
		}
		p := provenance.Build(provenance.Input{
			CapturedAt:        time.Now().UTC(),
			AdapterRevision:   Capabilities().AdapterRevision,
			Sources:           sourceInventory(path),
			Warnings:          warnings,
			HasReplayableBody: len(turns) > 0,
		})
		detail.Provenance = &p
	}
	return detail
}

func nonNilRenderEvents(events []model.RenderEvent) []model.RenderEvent {
	if events == nil {
		return []model.RenderEvent{}
	}
	return events
}

func (r *CodexReader) RenderANSI(id string, cols int) (string, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return "", fmt.Errorf("codex session not found: %s", id)
	}
	events, err := codexToRenderEvents(path)
	if err != nil {
		return "", err
	}
	return render.FormatEvents(events, cols), nil
}

// ---- JSONL shapes ----

type codexEvent struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID               string        `json:"id"`
	SessionID        string        `json:"session_id"`
	ParentThreadID   string        `json:"parent_thread_id"`
	ForkedFromID     string        `json:"forked_from_id"`
	ThreadSource     string        `json:"thread_source"`
	AgentPath        string        `json:"agent_path"`
	Timestamp        string        `json:"timestamp"`
	CWD              string        `json:"cwd"`
	ModelProvider    string        `json:"model_provider"`
	Git              *codexGitMeta `json:"git"`
	BaseInstructions struct {
		Text string `json:"text"`
	} `json:"base_instructions"`
}

type codexGitMeta struct {
	CommitHash    string `json:"commit_hash"`
	Branch        string `json:"branch"`
	RepositoryURL string `json:"repository_url"`
}

type codexPayload struct {
	Type    string          `json:"type"`
	TurnID  string          `json:"turn_id"`
	Message string          `json:"message"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	// turn_context
	Model string `json:"model"`
	// function_call / custom_tool_call
	Name           string          `json:"name"`
	CustomToolName string          `json:"custom_tool_name"`
	CallID         string          `json:"call_id"`
	Output         json.RawMessage `json:"output"`
	Success        bool            `json:"success"`
	Status         string          `json:"status"`
	Stdout         string          `json:"stdout"`
	Stderr         string          `json:"stderr"`
	// token_count
	Info *codexTokenCountInfo `json:"info"`
	// task_complete / turn_aborted
	DurationMs int64 `json:"duration_ms"`
	// turn_aborted reason
	Reason string `json:"reason"`
	// thread_rolled_back
	NumTurns int `json:"num_turns"`
	// thread_goal_updated
	Goal *codexGoal `json:"goal"`
	// function_call arguments (JSON string)
	Arguments string `json:"arguments"`
	// custom_tool_call input (raw string)
	Input string `json:"input"`
}

type codexGoal struct {
	Objective string `json:"objective"`
}

type codexTokenCountInfo struct {
	TotalTokenUsage codexTokenUsage `json:"total_token_usage"`
	LastTokenUsage  codexTokenUsage `json:"last_token_usage"`
}

type codexTokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}

var exitCodeRe = regexp.MustCompile(`Process exited with code (\d+)`)
var applyPatchExecRe = regexp.MustCompile(`(?s)\b(?:const|let|var)\s+patch\s*=\s*("(?:\\.|[^"\\])*")\s*;.*?tools\.apply_patch\(\s*patch\s*\)`)
var cellIDRe = regexp.MustCompile(`Script running with cell ID ([^\s]+)`)
var execCommandRe = regexp.MustCompile(`(?s)tools\.exec_command\(\s*\{.*?["']?cmd["']?\s*:\s*("(?:\\.|[^"\\])*")`)

// ---- helpers ----

func extractExitCode(output string) int {
	m := exitCodeRe.FindStringSubmatch(output)
	if len(m) == 2 {
		var code int
		fmt.Sscanf(m[1], "%d", &code)
		return code
	}
	return 0
}

// outputText accepts both historical string outputs and the newer structured
// input_text blocks emitted by functions.wait.
func outputText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "")
	}
	return ""
}

func cellIDFromText(text string) string {
	m := cellIDRe.FindStringSubmatch(text)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

// summarizeExecInput removes Codex's JavaScript bridge from the replay while
// retaining the actual command the agent asked to run.
func summarizeExecInput(name, input string) (string, map[string]any) {
	if name != "exec" {
		return name, parseArguments(input)
	}
	m := execCommandRe.FindStringSubmatch(input)
	if len(m) != 2 {
		return name, parseArguments(input)
	}
	var command string
	if json.Unmarshal([]byte(m[1]), &command) != nil || command == "" {
		return name, parseArguments(input)
	}
	return name, map[string]any{"command": command}
}

// unwrapApplyPatchExec recognises Codex's tool-wrapper form. Recent Codex
// clients call apply_patch inside functions.exec JavaScript instead of
// emitting apply_patch as the tool name. Preserve the underlying edit so the
// replay uses the existing diff renderer rather than a generic exec box.
func unwrapApplyPatchExec(name, input string) (toolName, toolInput string) {
	if name != "exec" {
		return name, input
	}
	m := applyPatchExecRe.FindStringSubmatch(input)
	if len(m) != 2 {
		return name, input
	}
	var patch string
	if json.Unmarshal([]byte(m[1]), &patch) != nil {
		return name, input
	}
	begin := strings.Index(patch, "*** Begin Patch")
	end := strings.Index(patch, "*** End Patch")
	if begin < 0 || end < begin {
		return name, input
	}
	return "apply_patch", patch
}

func extractModelName(meta *codexSessionMeta) string {
	if meta == nil {
		return ""
	}
	text := meta.BaseInstructions.Text
	if idx := strings.Index(text, "based on "); idx >= 0 {
		rest := text[idx+len("based on "):]
		if end := strings.IndexAny(rest, ".\n"); end > 0 {
			return rest[:end]
		}
		return rest
	}
	if meta.ModelProvider != "" {
		return meta.ModelProvider
	}
	return ""
}

func parseTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

func captureCodexSource(ctx context.Context, path string) (snapshotPath string, fingerprint model.SourceFingerprint, err error) {
	source, err := os.Open(path)
	if err != nil {
		return "", model.SourceFingerprint{}, err
	}
	defer source.Close()

	snapshot, err := os.CreateTemp("", "session-insight-codex-snapshot-*.jsonl")
	if err != nil {
		return "", model.SourceFingerprint{}, err
	}
	snapshotPath = snapshot.Name()
	keep := false
	defer func() {
		if closeErr := snapshot.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
		if !keep || err != nil {
			_ = os.Remove(snapshotPath)
		}
	}()

	hasher := sha256.New()
	buffer := make([]byte, 256*1024)
	var size int64
	for {
		if contextErr := ctx.Err(); contextErr != nil {
			return "", model.SourceFingerprint{}, contextErr
		}
		n, readErr := source.Read(buffer)
		if n > 0 {
			chunk := buffer[:n]
			if _, writeErr := snapshot.Write(chunk); writeErr != nil {
				return "", model.SourceFingerprint{}, writeErr
			}
			if _, hashErr := hasher.Write(chunk); hashErr != nil {
				return "", model.SourceFingerprint{}, hashErr
			}
			size += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", model.SourceFingerprint{}, readErr
		}
	}
	fingerprint = model.SourceFingerprint{
		Algorithm: model.SourceFingerprintSHA256,
		Digest:    fmt.Sprintf("%x", hasher.Sum(nil)),
		SizeBytes: size,
	}
	keep = true
	return snapshotPath, fingerprint, nil
}

// codexSessionFromSnapshot rebuilds index-facing metadata from the same
// immutable bytes used for turns and render events. User-owned bookmark fields
// are the only values retained from the discovery hint.
func codexSessionFromSnapshot(path, snapshotPath string, discovery model.Session) (model.Session, error) {
	snapshot, err := os.Open(snapshotPath)
	if err != nil {
		return model.Session{}, err
	}
	defer snapshot.Close()
	return codexSessionFromReader(path, snapshot, discovery)
}

func codexSessionFromReader(path string, source io.Reader, discovery model.Session) (model.Session, error) {
	var (
		cwd           string
		nativeID      string
		parentID      string
		agentPath     string
		isSubagent    bool
		modelName     string
		modelProvider string
		repositoryURL string
		firstUserMsg  string
		userMessages  []string
		createdAt     time.Time
		updatedAt     time.Time
		lineCount     int
	)

	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		lineCount++
		var evt codexEvent
		if json.Unmarshal(scanner.Bytes(), &evt) != nil {
			continue
		}
		if ts := parseTimestamp(evt.Timestamp); !ts.IsZero() {
			if createdAt.IsZero() || ts.Before(createdAt) {
				createdAt = ts
			}
			if ts.After(updatedAt) {
				updatedAt = ts
			}
		}
		switch evt.Type {
		case "session_meta":
			var meta codexSessionMeta
			if json.Unmarshal(evt.Payload, &meta) != nil {
				continue
			}
			if cwd == "" {
				cwd = meta.CWD
			}
			if nativeID == "" {
				if meta.ID != "" {
					nativeID = meta.ID
				} else {
					nativeID = meta.SessionID
				}
			}
			if modelName == "" {
				modelName = extractModelName(&meta)
			}
			if modelProvider == "" {
				modelProvider = meta.ModelProvider
			}
			if repositoryURL == "" && meta.Git != nil && validRecordedRepositoryURL(meta.Git.RepositoryURL) {
				repositoryURL = meta.Git.RepositoryURL
			}
			if meta.ThreadSource == "subagent" {
				isSubagent = true
				if parentID == "" {
					parentID = meta.ParentThreadID
					if parentID == "" {
						parentID = meta.ForkedFromID
					}
				}
				if agentPath == "" {
					agentPath = meta.AgentPath
				}
			}
		case "event_msg":
			var payload codexPayload
			if json.Unmarshal(evt.Payload, &payload) == nil && payload.Type == "user_message" && payload.Message != "" {
				if firstUserMsg == "" {
					firstUserMsg = payload.Message
				}
				if len(userMessages) < 5 {
					userMessages = append(userMessages, payload.Message)
				}
			}
		case "turn_context":
			var payload codexPayload
			if json.Unmarshal(evt.Payload, &payload) == nil && payload.Model != "" {
				modelName = payload.Model
			}
		}
	}
	if !validRecordedWorktreePath(cwd) {
		cwd = ""
	}

	session := model.Session{
		ID:              strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		AgentType:       "codex",
		CWD:             cwd,
		Project:         codexRecordedProject(cwd, repositoryURL),
		Name:            resolveName(firstUserMsg, createdAt),
		ModelName:       modelName,
		ModelProvider:   modelProvider,
		ResumeID:        nativeID,
		ParentSessionID: parentID,
		AgentPath:       agentPath,
		IsSubagent:      isSubagent,
		PreviewText:     shared.BuildPreviewText(userMessages),
		MessageCount:    lineCount,
		Bookmarked:      discovery.Bookmarked,
		BookmarkNote:    discovery.BookmarkNote,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
	// Keep the legacy detail fields unchanged. Source-qualified repository and
	// branch facts belong to IndexSnapshotEnvelope.OriginGit.
	return session, scanner.Err()
}

func codexRecordedProject(cwd, repositoryURL string) string {
	if parsed, err := url.Parse(repositoryURL); err == nil && parsed.Path != "" {
		name := strings.TrimSuffix(pathBasePortable(parsed.Path), ".git")
		if name != "" {
			return name
		}
	}
	slashPath := strings.ReplaceAll(cwd, `\`, "/")
	for _, marker := range []string{"/.claude/worktrees/", "/.worktrees/"} {
		if index := strings.Index(slashPath, marker); index >= 0 {
			if name := pathBasePortable(slashPath[:index]); name != "" {
				return name
			}
		}
	}
	return pathBasePortable(slashPath)
}

func pathBasePortable(path string) string {
	path = strings.TrimRight(strings.ReplaceAll(path, `\`, "/"), "/")
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[index+1:]
	}
	return path
}

func parseCodexEnvelopeEvidenceSnapshot(snapshotPath, sourceRevision string) (*model.SessionGitOrigin, model.SessionFinalizationEvidence, error) {
	snapshot, err := os.Open(snapshotPath)
	if err != nil {
		return nil, model.SessionFinalizationEvidence{}, err
	}
	defer snapshot.Close()
	return parseCodexEnvelopeEvidence(snapshot, sourceRevision)
}

func parseCodexEnvelopeEvidence(source io.Reader, sourceRevision string) (*model.SessionGitOrigin, model.SessionFinalizationEvidence, error) {
	var (
		metaSeen        bool
		metaInvalid     bool
		meta            codexSessionMeta
		metaRecordedAt  *time.Time
		signalKind      = model.SessionSignalNone
		signalTime      *time.Time
		signalTimeValid bool
	)
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		var evt codexEvent
		if json.Unmarshal(scanner.Bytes(), &evt) != nil {
			continue
		}
		if evt.Type == "session_meta" && !metaSeen {
			metaSeen = true
			if json.Unmarshal(evt.Payload, &meta) != nil {
				metaInvalid = true
			} else if recorded := parseTimestamp(evt.Timestamp); !recorded.IsZero() {
				metaRecordedAt = timePtr(recorded)
			} else if recorded := parseTimestamp(meta.Timestamp); !recorded.IsZero() {
				metaRecordedAt = timePtr(recorded)
			}
		}
		if evt.Type != "event_msg" {
			continue
		}
		var payload codexPayload
		if json.Unmarshal(evt.Payload, &payload) != nil {
			continue
		}
		var kind model.SessionFinalizationSignalKind
		switch payload.Type {
		case "task_started":
			kind = model.SessionSignalTurnOpen
		case "task_complete":
			kind = model.SessionSignalTurnComplete
		case "turn_aborted":
			kind = model.SessionSignalTurnAborted
		case "thread_rolled_back":
			kind = model.SessionSignalTurnsRolledBack
		default:
			continue
		}
		signalKind = kind
		if recorded := parseTimestamp(evt.Timestamp); !recorded.IsZero() {
			signalTime = timePtr(recorded)
			signalTimeValid = true
		} else {
			signalTime = nil
			signalTimeValid = false
		}
	}

	missingAssessment := model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonAgentGitFactMissing)
	invalidAssessment := model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonAgentGitFactInvalid)
	untimedAssessment := model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonAgentGitFactTimestampUnavailable)
	stringFact := func(value string, valid func(string) bool) model.GitFact[string] {
		fact := model.GitFact[string]{
			Source:         model.GitSourceAgentRecorded,
			RecordedAt:     metaRecordedAt,
			SourceRevision: sourceRevision,
		}
		switch {
		case metaInvalid:
			fact.Assessment = invalidAssessment
		case value == "":
			fact.Assessment = missingAssessment
		case !valid(value):
			fact.Assessment = invalidAssessment
		case metaRecordedAt == nil:
			fact.Value = value
			fact.Assessment = untimedAssessment
		default:
			fact.Value = value
			fact.Assessment = model.ExactGitEvidence()
		}
		return fact
	}
	gitMeta := codexGitMeta{}
	if meta.Git != nil {
		gitMeta = *meta.Git
	}
	origin := &model.SessionGitOrigin{
		RepositoryURL: stringFact(gitMeta.RepositoryURL, validRecordedRepositoryURL),
		WorktreePath:  stringFact(meta.CWD, validRecordedWorktreePath),
		Branch:        stringFact(gitMeta.Branch, validRecordedBranch),
		HeadSHA:       stringFact(gitMeta.CommitHash, validRecordedSHA),
		DirtyState: model.GitFact[model.GitDirtyState]{
			Value:          model.GitDirtyUnknown,
			Assessment:     missingAssessment,
			Source:         model.GitSourceAgentRecorded,
			RecordedAt:     metaRecordedAt,
			SourceRevision: sourceRevision,
		},
	}

	finalization := model.SessionFinalizationEvidence{
		State:      model.SessionFinalizationUnknown,
		SignalKind: signalKind,
	}
	switch signalKind {
	case model.SessionSignalNone:
		finalization.Assessment = model.NonExactSessionEvidence(model.SessionEvidenceMissing, model.ReasonSessionStateNotRecorded)
		finalization.SignalAssessment = model.NonExactSessionEvidence(model.SessionEvidenceMissing, model.ReasonSessionStateNotRecorded)
	case model.SessionSignalTurnOpen:
		finalization.Assessment = model.NonExactSessionEvidence(model.SessionEvidenceMissing, model.ReasonTurnMarkerNotSessionLiveness)
	case model.SessionSignalTurnComplete, model.SessionSignalTurnAborted, model.SessionSignalTurnsRolledBack:
		finalization.Assessment = model.NonExactSessionEvidence(model.SessionEvidenceMissing, model.ReasonTurnMarkerNotSessionFinalization)
	}
	if signalKind != model.SessionSignalNone {
		if signalTimeValid {
			finalization.SignalRecordedAt = signalTime
			finalization.SignalAssessment = model.ExactSessionEvidence()
		} else {
			finalization.SignalAssessment = model.NonExactSessionEvidence(model.SessionEvidenceEstimated, model.ReasonSessionSignalTimestampInvalid)
		}
	}
	return origin, finalization, scanner.Err()
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func validRecordedSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validRecordedBranch(value string) bool {
	return value == strings.TrimSpace(value) && value != "" && len(value) <= 1024 && !strings.ContainsAny(value, "\x00\r\n")
}

func validRecordedWorktreePath(value string) bool {
	if value != strings.TrimSpace(value) || value == "" || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//") {
		return true
	}
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '\\' || value[2] == '/')
}

func validRecordedRepositoryURL(value string) bool {
	if value != strings.TrimSpace(value) || strings.ContainsRune(value, '\x00') {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

// ---- ListSessions ----

func (r *CodexReader) ListSessions() ([]model.Session, error) {
	sessions, _, err := r.ListSessionsDetailed()
	return sessions, err
}

func (r *CodexReader) ListSessionsDetailed() (sessions []model.Session, complete bool, err error) {
	var files []string
	walkIncomplete := false
	err = filepath.WalkDir(r.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			walkIncomplete = true
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}

	type result struct {
		session model.Session
		ok      bool
		skip    bool
	}
	results := make(chan result, len(files))
	sem := make(chan struct{}, 20)
	var wg sync.WaitGroup

	for _, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(path string) {
			defer func() { <-sem; wg.Done() }()
			if sess, ok := readSessionMeta(path); ok {
				results <- result{session: sess, ok: true}
				return
			}
			if _, err := os.Stat(path); err == nil || !os.IsNotExist(err) {
				results <- result{skip: true}
			}
		}(f)
	}
	wg.Wait()
	close(results)

	complete = !walkIncomplete
	paths := make(map[string]string, len(files))
	for res := range results {
		if res.ok {
			sessions = append(sessions, res.session)
		} else if res.skip {
			complete = false
		}
	}
	for _, path := range files {
		paths[strings.TrimSuffix(filepath.Base(path), ".jsonl")] = path
	}
	r.pathsMu.Lock()
	r.paths = paths
	r.pathsMu.Unlock()

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, complete, nil
}

func readSessionMeta(jsonlPath string) (model.Session, bool) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return model.Session{}, false
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return model.Session{}, false
	}
	fileSize := fi.Size()

	sessionID := strings.TrimSuffix(filepath.Base(jsonlPath), ".jsonl")

	var (
		cwd           string
		nativeID      string
		parentID      string
		agentPath     string
		isSubagent    bool
		modelName     string
		modelProvider string
		firstUserMsg  string
		userMessages  []string
		createdAt     time.Time
		updatedAt     time.Time
		headLines     int
		headBytes     int64
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)

	const headMax = 200
	for scanner.Scan() && headLines < headMax {
		headLines++
		headBytes += int64(len(scanner.Bytes()) + 1)

		var evt codexEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}

		if evt.Timestamp != "" {
			if t := parseTimestamp(evt.Timestamp); !t.IsZero() {
				if createdAt.IsZero() || t.Before(createdAt) {
					createdAt = t
				}
				if t.After(updatedAt) {
					updatedAt = t
				}
			}
		}

		switch evt.Type {
		case "session_meta":
			var m codexSessionMeta
			if json.Unmarshal(evt.Payload, &m) == nil {
				if cwd == "" && m.CWD != "" {
					cwd = m.CWD
				}
				if modelName == "" {
					modelName = extractModelName(&m)
				}
				if modelProvider == "" && m.ModelProvider != "" {
					modelProvider = m.ModelProvider
				}
				if nativeID == "" {
					// Codex resume resolves the rollout's own payload.id. For
					// subagent rollouts, session_id points at the parent thread and
					// must not replace the child rollout's resumable UUID.
					if m.ID != "" {
						nativeID = m.ID
					} else if m.SessionID != "" {
						nativeID = m.SessionID
					}
				}
				if m.ThreadSource == "subagent" {
					isSubagent = true
					if parentID == "" {
						parentID = m.ParentThreadID
						if parentID == "" {
							parentID = m.ForkedFromID
						}
					}
					if agentPath == "" {
						agentPath = m.AgentPath
					}
				}
			}

		case "event_msg":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			if p.Type == "user_message" && p.Message != "" {
				if firstUserMsg == "" {
					firstUserMsg = p.Message
				}
				if len(userMessages) < 5 {
					userMessages = append(userMessages, p.Message)
				}
			}

		case "response_item":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			if p.Type == "message" && p.Role == "assistant" && modelName == "" {
				// Best-effort: try content for model hints (unlikely to find here)
				_ = p
			}

		case "turn_context":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) == nil && p.Model != "" {
				modelName = p.Model
			}
		}
	}

	if updatedAt.IsZero() {
		updatedAt = fi.ModTime()
	}
	if createdAt.IsZero() {
		createdAt = updatedAt
	}

	name := resolveName(firstUserMsg, createdAt)
	previewText := shared.BuildPreviewText(userMessages)

	msgCount := shared.EstimateLineCount(headLines, headBytes, fileSize)

	// Tail scan for updatedAt from last event timestamp
	const tailBytes = 8 * 1024
	if fileSize > tailBytes {
		seekPos := fileSize - tailBytes
		if _, err := f.Seek(seekPos, 0); err == nil {
			tailScanner := bufio.NewScanner(f)
			tailScanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
			tailScanner.Scan() // skip partial first line
			for tailScanner.Scan() {
				var evt codexEvent
				if json.Unmarshal(tailScanner.Bytes(), &evt) != nil {
					continue
				}
				if evt.Timestamp != "" {
					if t := parseTimestamp(evt.Timestamp); !t.IsZero() && t.After(updatedAt) {
						updatedAt = t
					}
				}
			}
		}
	}

	return model.Session{
		ID:              sessionID,
		AgentType:       "codex",
		CWD:             cwd,
		Project:         shared.ResolveProject(cwd, ""),
		Name:            name,
		ModelName:       modelName,
		ModelProvider:   modelProvider,
		ResumeID:        nativeID,
		ParentSessionID: parentID,
		AgentPath:       agentPath,
		IsSubagent:      isSubagent,
		PreviewText:     previewText,
		MessageCount:    msgCount,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}, true
}

func resolveName(firstUserMsg string, createdAt time.Time) string {
	if firstUserMsg != "" {
		return shared.TruncateRunes(firstUserMsg, 50)
	}
	if !createdAt.IsZero() {
		return "Codex " + createdAt.Format("01-02 15:04")
	}
	return "Codex Session"
}

// ---- GetSession ----

func (r *CodexReader) GetSession(id string) (*model.SessionDetail, error) {
	jsonlPath := r.findSessionFile(id)
	if jsonlPath == "" {
		readErr := readerr.New(readerr.SourceMissing, "source_missing",
			fmt.Errorf("codex session not found: %s", id))
		if known := r.knownSessionFile(id); known != "" {
			readErr.WithSources(sourceInventory(known))
		}
		return nil, readErr
	}

	session, ok := readSessionMeta(jsonlPath)
	if !ok {
		return nil, readerr.New(readerr.SourceUnreadable, "source_unreadable",
			fmt.Errorf("failed to read codex session: %s", id)).
			WithSources(sourceInventory(jsonlPath)).
			WithWarnings([]model.ParseWarning{readerr.SourceUnreadableWarning(model.SourceRolePrimaryTranscript)})
	}

	parsed, modelName, modelProvider, skipped := parseCodexEvents(jsonlPath)
	if modelName != "" {
		session.ModelName = modelName
	}
	if modelProvider != "" {
		session.ModelProvider = modelProvider
	}
	session.TurnCount = len(parsed.Active)
	session.HistoricalTurnCount = parsed.Historical
	for _, group := range parsed.RollbackGroups {
		session.RolledBackTurnCount += len(group.Turns)
	}

	detail := &model.SessionDetail{Session: session, Turns: parsed.Active, RollbackGroups: parsed.RollbackGroups}

	detail.AnomalySummary = shared.RunAnomalyDetection(parsed.Active)
	var warnings []model.ParseWarning
	if skipped > 0 {
		warnings = append(warnings, provenance.Warning(
			model.WarnMalformedRecordSkipped, model.WarningSeverityWarning, true,
			[]string{model.ImpactReplay}, model.SourceRolePrimaryTranscript, nil, skipped,
		))
	}
	p := provenance.Build(provenance.Input{
		CapturedAt:        time.Now().UTC(),
		AdapterRevision:   Capabilities().AdapterRevision,
		Sources:           sourceInventory(jsonlPath),
		Warnings:          warnings,
		HasReplayableBody: len(parsed.Active) > 0,
	})
	detail.Provenance = &p

	return detail, nil
}

func (r *CodexReader) findSessionFile(sessionID string) string {
	r.pathsMu.RLock()
	cached := r.paths[sessionID]
	r.pathsMu.RUnlock()
	if cached != "" {
		if _, err := os.Stat(cached); err == nil {
			return cached
		}
	}
	var found string
	filepath.WalkDir(r.sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil
		}
		if !d.IsDir() && d.Name() == sessionID+".jsonl" {
			found = path
		}
		return nil
	})
	if found != "" {
		r.pathsMu.Lock()
		r.paths[sessionID] = found
		r.pathsMu.Unlock()
	}
	return found
}

// knownSessionFile returns the last discovered path without requiring it to
// still exist. Read failures use it to persist a missing source inventory when
// the file disappears between discovery and detail capture.
func (r *CodexReader) knownSessionFile(sessionID string) string {
	r.pathsMu.RLock()
	defer r.pathsMu.RUnlock()
	return r.paths[sessionID]
}

// ---- Event parsing ----

type codexParsedTurns struct {
	Active         []model.TurnVM
	RollbackGroups []model.RollbackGroupVM
	Historical     int
}

type codexTurnAttempt struct {
	turn          model.TurnVM
	originalIndex int
}

type codexRollbackAttempt struct {
	after     *codexTurnAttempt
	timestamp string
	removed   []*codexTurnAttempt
}

func parseCodexEvents(path string) (codexParsedTurns, string, string, int) {
	parsed, modelName, modelProvider, skipped, err := parseCodexEventsFile(path)
	if err != nil {
		return codexParsedTurns{}, "", "", skipped
	}
	return parsed, modelName, modelProvider, skipped
}

func parseCodexEventsFile(path string) (codexParsedTurns, string, string, int, error) {
	source, err := os.Open(path)
	if err != nil {
		return codexParsedTurns{}, "", "", 0, err
	}
	defer source.Close()
	return parseCodexEventsReader(source)
}

func parseCodexEventsReader(source io.Reader) (codexParsedTurns, string, string, int, error) {

	var (
		attempts      []*codexTurnAttempt
		active        []*codexTurnAttempt
		rollbacks     []codexRollbackAttempt
		foundModel    string
		foundProvider string
		current       *codexTurnAttempt
		turnStartTS   string
		pendingGoal   string
		lastGoal      string
		skipped       int
	)

	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var evt codexEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			skipped++
			continue
		}

		switch evt.Type {
		case "session_meta":
			var m codexSessionMeta
			if json.Unmarshal(evt.Payload, &m) == nil {
				if foundModel == "" {
					foundModel = extractModelName(&m)
				}
				if foundProvider == "" && m.ModelProvider != "" {
					foundProvider = m.ModelProvider
				}
			}

		case "turn_context":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) == nil && p.Model != "" {
				foundModel = p.Model
			}

		case "event_msg":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			switch p.Type {
			case "thread_goal_updated":
				if p.Goal != nil {
					if objective := strings.TrimSpace(p.Goal.Objective); objective != "" && objective != lastGoal {
						pendingGoal = objective
						lastGoal = objective
					}
				}
			case "task_started":
				current = &codexTurnAttempt{turn: model.TurnVM{
					TurnIndex: len(attempts),
					Events:    []model.EventVM{},
				}}
				if pendingGoal != "" {
					current.turn.UserMessage = pendingGoal
					pendingGoal = ""
				}
				attempts = append(attempts, current)
				active = append(active, current)
				turnStartTS = evt.Timestamp

			case "thread_rolled_back":
				n := p.NumTurns
				if n < 0 {
					n = 0
				}
				if n > len(active) {
					n = len(active)
				}
				removed := append([]*codexTurnAttempt(nil), active[len(active)-n:]...)
				active = active[:len(active)-n]
				var after *codexTurnAttempt
				if len(active) > 0 {
					after = active[len(active)-1]
				}
				rollbacks = append(rollbacks, codexRollbackAttempt{after: after, timestamp: evt.Timestamp, removed: removed})
				current = nil
				turnStartTS = ""

			case "user_message":
				if current != nil && p.Message != "" {
					current.turn.UserMessage = p.Message
				}
				// user_message before any task_started: record for session name but don't create turn
				if current == nil {
					continue
				}

			case "agent_message":
				if current != nil && p.Message != "" {
					current.turn.AssistantMessage += p.Message
				}

			case "token_count":
				if current != nil && p.Info != nil {
					current.turn.RequestCount++
					u := &current.turn.TokenUsage
					last := p.Info.LastTokenUsage
					// Codex uses inclusive semantics: cached_input_tokens is a
					// subset of input_tokens, and reasoning_output_tokens a
					// subset of output_tokens. Canonical buckets are mutually
					// exclusive, so subtract cache from input; reasoning stays
					// an annotation and is never added to output.
					fresh := last.InputTokens - last.CachedInputTokens
					if fresh < 0 {
						fresh = 0
					}
					u.PromptTokens += fresh
					u.CompletionTokens += last.OutputTokens
					u.CacheReadTokens += last.CachedInputTokens
					u.ReasoningTokens += last.ReasoningOutputTokens
					u.Present.Input = model.PresenceExact
					u.Present.Output = model.PresenceExact
					u.Present.CacheRead = model.PresenceExact
					u.Present.Reasoning = model.PresenceExact
					// OpenAI prompt caching is automatic and free: the
					// cache_write concept does not exist for this agent.
					u.Present.CacheWrite = model.PresenceNA
				}

			case "patch_apply_end":
				if current != nil {
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "patch_apply_end",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"success": p.Success, "stdout": p.Stdout, "stderr": p.Stderr},
					})
					if !p.Success {
						current.turn.ErrorCount++
					}
				}

			case "task_complete":
				// turn boundary marker — keep turn open but record completion
				if current != nil {
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "task_complete",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{},
					})
				}

			case "turn_aborted":
				if current != nil {
					current.turn.DurationMs = p.DurationMs
				}
			}

		case "response_item":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			switch p.Type {
			case "message":
				if current == nil {
					continue
				}
				switch p.Role {
				case "assistant":
					// Skip: the event_msg/agent_message branch already
					// accumulated this assistant text. Codex logs every
					// assistant message twice (once as agent_message, once as
					// this response_item), so appending here would duplicate it
					// — the same reason codexToRenderEvents skips it.
				case "user":
					// Ordinary user messages have a matching event_msg/user_message.
					// Goal-mode objectives are carried by thread_goal_updated so they
					// can be rendered once, instead of once per continuation turn.
				}

			case "reasoning":
				// reasoning may be encrypted; skip text extraction

			case "function_call":
				if current != nil {
					current.turn.ToolCallCount++
					if p.Name != "" {
						current.turn.ToolNames = append(current.turn.ToolNames, p.Name)
					}
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "function_call",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"name": p.Name, "call_id": p.CallID},
					})
				}

			case "function_call_output":
				if current != nil {
					exitCode := extractExitCode(outputText(p.Output))
					isErr := exitCode != 0
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "function_call_output",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"call_id": p.CallID, "exit_code": exitCode},
					})
					if isErr {
						current.turn.ErrorCount++
					}
				}

			case "custom_tool_call":
				if current != nil {
					current.turn.ToolCallCount++
					name := p.Name
					if name == "" {
						name = p.CustomToolName
					}
					name, _ = unwrapApplyPatchExec(name, p.Input)
					if name != "" {
						current.turn.ToolNames = append(current.turn.ToolNames, name)
					}
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "custom_tool_call",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"name": name, "call_id": p.CallID, "status": p.Status},
					})
				}

			case "custom_tool_call_output":
				if current != nil {
					exitCode := extractExitCode(outputText(p.Output))
					isErr := exitCode != 0
					current.turn.Events = append(current.turn.Events, model.EventVM{
						Type:      "custom_tool_call_output",
						Timestamp: evt.Timestamp,
						Data:      map[string]any{"call_id": p.CallID, "exit_code": exitCode},
					})
					if isErr {
						current.turn.ErrorCount++
					}
				}
			}

		case "compacted":
			// Context window compaction: the model's context was
			// compressed/summarized. Mark the current turn so the
			// frontend can surface it.
			if current != nil {
				current.turn.Anomalies = append(current.turn.Anomalies, "compaction")
			}
		}

		// Track duration from turn start to latest event
		if current != nil && turnStartTS != "" && evt.Timestamp != "" {
			if t1 := parseTimestamp(turnStartTS); !t1.IsZero() {
				if t2 := parseTimestamp(evt.Timestamp); !t2.IsZero() {
					dur := t2.Sub(t1).Milliseconds()
					if dur > current.turn.DurationMs {
						current.turn.DurationMs = dur
					}
				}
			}
		}
	}

	// Rollback counts operate on raw task attempts. Only after replaying them
	// may empty/noise turns be removed; doing this earlier would make an
	// interrupted empty task's rollback delete the preceding real turn.
	visible := make(map[*codexTurnAttempt]bool, len(attempts))
	original := 0
	for _, a := range attempts {
		filtered := shared.FilterEmptyTurns([]model.TurnVM{a.turn})
		if len(filtered) == 0 {
			continue
		}
		a.turn = filtered[0]
		a.originalIndex = original
		original++
		visible[a] = true
	}

	result := codexParsedTurns{Historical: original}
	activeIndex := make(map[*codexTurnAttempt]int)
	for _, a := range active {
		if !visible[a] {
			continue
		}
		a.turn.TurnIndex = len(result.Active)
		a.turn.OriginalTurnIndex = a.originalIndex
		activeIndex[a] = a.turn.TurnIndex
		result.Active = append(result.Active, a.turn)
	}
	for _, rb := range rollbacks {
		group := model.RollbackGroupVM{AfterTurnIndex: -1, Timestamp: rb.timestamp}
		if idx, ok := activeIndex[rb.after]; ok {
			group.AfterTurnIndex = idx
		}
		for _, a := range rb.removed {
			if !visible[a] {
				continue
			}
			t := a.turn
			t.TurnIndex = a.originalIndex
			t.OriginalTurnIndex = a.originalIndex
			t.RolledBack = true
			group.Turns = append(group.Turns, t)
		}
		if len(group.Turns) > 0 {
			result.RollbackGroups = append(result.RollbackGroups, group)
		}
	}

	return result, foundModel, foundProvider, skipped, scanner.Err()
}

// ---- RenderEvent adapter ----

type codexRenderAttempt struct {
	events          []model.RenderEvent
	groups          []*codexRenderRollback
	originalIndex   int
	goalPromptEvent int
}

type codexRenderRollback struct {
	timestamp time.Time
	removed   []*codexRenderAttempt
}

type codexCellTask struct {
	toolCallID    string
	parentEventID string
}

func codexRenderAttemptVisible(a *codexRenderAttempt) bool {
	if a == nil {
		return false
	}
	for _, evt := range a.events {
		switch evt.Type {
		case "TurnBoundary":
		case "UserPrompt":
			if strings.TrimSpace(evt.Text) != "" {
				return true
			}
		case "AgentSpecific":
			if evt.Subtype != "turn_duration" {
				return true
			}
		default:
			return true
		}
	}
	return false
}

// codexToRenderEvents parses a Codex JSONL session file into a flat
// []model.RenderEvent stream suitable for render.FormatEvents.
func codexToRenderEvents(path string) ([]model.RenderEvent, error) {
	events, _, err := codexToRenderEventsDetailed(path)
	return events, err
}

func codexToRenderEventsDetailed(path string) ([]model.RenderEvent, int, error) {
	source, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer source.Close()
	var modTime *time.Time
	if info, statErr := os.Stat(path); statErr == nil {
		value := info.ModTime()
		modTime = &value
	}
	return codexToRenderEventsDetailedReader(path, source, modTime)
}

func codexToRenderEventsSnapshot(path, snapshotPath string) ([]model.RenderEvent, int, error) {
	snapshot, err := os.Open(snapshotPath)
	if err != nil {
		return nil, 0, err
	}
	defer snapshot.Close()
	return codexToRenderEventsDetailedReader(path, snapshot, nil)
}

func codexToRenderEventsDetailedReader(path string, source io.Reader, sourceModTime *time.Time) ([]model.RenderEvent, int, error) {

	var (
		attempts     []*codexRenderAttempt
		active       []*codexRenderAttempt
		rootGroups   []*codexRenderRollback
		current      *codexRenderAttempt
		fileTag      = strings.TrimSuffix(filepath.Base(path), ".jsonl")
		eventCtr     int
		pendingTools = make(map[string]string) // callID -> ToolInvocation EventID
		completed    = make(map[string]bool)   // patch_apply_end already emitted the result
		cellTasks    = make(map[string]codexCellTask)
		waitTasks    = make(map[string]codexCellTask)
		skipped      int

		// Codex rollouts carry explicit turn brackets: task_started opens a
		// turn, task_complete / turn_aborted closes it. An open bracket at
		// EOF means the CLI is still working (or died mid-turn — the
		// LiveWindow guard in shared.TrailingInProgress bounds that case).
		turnOpen      bool
		pendingGoal   string
		pendingGoalTS time.Time
		lastGoal      string
	)

	emit := func(evt model.RenderEvent) string {
		if current == nil {
			return ""
		}
		if evt.EventID == "" {
			evt.EventID = fmt.Sprintf("evt-%s-%04d", fileTag, eventCtr)
			eventCtr++
		}
		if evt.AgentType == "" {
			evt.AgentType = "codex"
		}
		current.events = append(current.events, evt)
		return evt.EventID
	}

	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		var evt codexEvent
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			skipped++
			continue
		}

		ts := parseTimestamp(evt.Timestamp)

		switch evt.Type {
		case "event_msg":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				skipped++
				continue
			}
			switch p.Type {
			case "thread_goal_updated":
				if p.Goal != nil {
					if objective := strings.TrimSpace(p.Goal.Objective); objective != "" && objective != lastGoal {
						pendingGoal = objective
						pendingGoalTS = ts
						lastGoal = objective
					}
				}
			case "task_started":
				turnOpen = true
				current = &codexRenderAttempt{goalPromptEvent: -1}
				attempts = append(attempts, current)
				active = append(active, current)
				emit(model.RenderEvent{
					Type:      "TurnBoundary",
					Timestamp: ts,
					TurnIndex: len(attempts) - 1,
				})
				if pendingGoal != "" {
					current.goalPromptEvent = len(current.events)
					emit(model.RenderEvent{
						Type:      "UserPrompt",
						Timestamp: pendingGoalTS,
						TurnIndex: len(attempts) - 1,
						Text:      pendingGoal,
					})
					pendingGoal = ""
					pendingGoalTS = time.Time{}
				}

			case "task_complete", "turn_aborted":
				turnOpen = false

			case "thread_rolled_back":
				n := p.NumTurns
				if n < 0 {
					n = 0
				}
				if n > len(active) {
					n = len(active)
				}
				if n > 0 {
					group := &codexRenderRollback{
						timestamp: ts,
						removed:   append([]*codexRenderAttempt(nil), active[len(active)-n:]...),
					}
					active = active[:len(active)-n]
					if len(active) == 0 {
						rootGroups = append(rootGroups, group)
					} else {
						target := active[len(active)-1]
						target.groups = append(target.groups, group)
					}
				}
				current = nil
				turnOpen = false

			case "user_message":
				if current == nil || p.Message == "" {
					continue
				}
				// A direct user message in the same turn supersedes a goal
				// update that was queued just before task_started.
				if current.goalPromptEvent >= 0 {
					current.events = append(current.events[:current.goalPromptEvent], current.events[current.goalPromptEvent+1:]...)
					current.goalPromptEvent = -1
				}
				emit(model.RenderEvent{
					Type:      "UserPrompt",
					Timestamp: ts,
					TurnIndex: len(attempts) - 1,
					Text:      p.Message,
				})

			case "agent_message":
				if p.Message != "" {
					emit(model.RenderEvent{
						Type:      "TextChunk",
						Timestamp: ts,
						TurnIndex: len(attempts) - 1,
						Text:      p.Message,
					})
				}

			case "patch_apply_end":
				parentEventID := ""
				if p.CallID != "" {
					parentEventID = pendingTools[p.CallID]
					delete(pendingTools, p.CallID)
					completed[p.CallID] = true
				}
				exitCode := 0
				if !p.Success {
					exitCode = 1
				}
				emit(model.RenderEvent{
					Type:          "ToolResult",
					Timestamp:     ts,
					TurnIndex:     len(attempts) - 1,
					ToolCallID:    p.CallID,
					Stdout:        p.Stdout,
					Stderr:        p.Stderr,
					ExitCode:      exitCode,
					ParentEventID: parentEventID,
				})
			}

		case "response_item":
			var p codexPayload
			if json.Unmarshal(evt.Payload, &p) != nil {
				continue
			}
			switch p.Type {
			case "message":
				// agent_message already covers assistant text; response_item
				// assistant blocks would duplicate it.
			case "function_call":
				input := parseArguments(p.Arguments)
				if p.Name == "wait" {
					if cellID, _ := input["cell_id"].(string); cellID != "" {
						if task, ok := cellTasks[cellID]; ok {
							waitTasks[p.CallID] = task
							continue
						}
					}
				}
				invID := emit(model.RenderEvent{
					Type:       "ToolInvocation",
					Timestamp:  ts,
					TurnIndex:  len(attempts) - 1,
					ToolName:   p.Name,
					ToolCallID: p.CallID,
					ToolInput:  input,
				})
				if p.CallID != "" {
					pendingTools[p.CallID] = invID
				}

			case "function_call_output":
				output := outputText(p.Output)
				if task, ok := waitTasks[p.CallID]; ok {
					delete(waitTasks, p.CallID)
					if cellIDFromText(output) != "" {
						continue
					}
					emit(model.RenderEvent{
						Type:          "ToolResult",
						Timestamp:     ts,
						TurnIndex:     len(attempts) - 1,
						ToolCallID:    task.toolCallID,
						Stdout:        output,
						ExitCode:      extractExitCode(output),
						ParentEventID: task.parentEventID,
					})
					for cellID, active := range cellTasks {
						if active == task {
							delete(cellTasks, cellID)
						}
					}
					continue
				}
				parentEventID := ""
				if p.CallID != "" {
					parentEventID = pendingTools[p.CallID]
					delete(pendingTools, p.CallID)
				}
				emit(model.RenderEvent{
					Type:          "ToolResult",
					Timestamp:     ts,
					TurnIndex:     len(attempts) - 1,
					ToolCallID:    p.CallID,
					Stdout:        output,
					ExitCode:      extractExitCode(output),
					ParentEventID: parentEventID,
				})

			case "custom_tool_call":
				name := p.Name
				if name == "" {
					name = p.CustomToolName
				}
				input := p.Input
				if input == "" {
					input = p.Arguments
				}
				name, input = unwrapApplyPatchExec(name, input)
				name, toolInput := summarizeExecInput(name, input)
				invID := emit(model.RenderEvent{
					Type:       "ToolInvocation",
					Timestamp:  ts,
					TurnIndex:  len(attempts) - 1,
					ToolName:   name,
					ToolCallID: p.CallID,
					ToolInput:  toolInput,
				})
				if p.CallID != "" {
					pendingTools[p.CallID] = invID
				}

			case "custom_tool_call_output":
				output := outputText(p.Output)
				if cellID := cellIDFromText(output); cellID != "" {
					if parentEventID := pendingTools[p.CallID]; parentEventID != "" {
						cellTasks[cellID] = codexCellTask{toolCallID: p.CallID, parentEventID: parentEventID}
						delete(pendingTools, p.CallID)
						continue
					}
				}
				if p.CallID != "" && completed[p.CallID] {
					delete(completed, p.CallID)
					continue
				}
				parentEventID := ""
				if p.CallID != "" {
					parentEventID = pendingTools[p.CallID]
					delete(pendingTools, p.CallID)
				}
				emit(model.RenderEvent{
					Type:          "ToolResult",
					Timestamp:     ts,
					TurnIndex:     len(attempts) - 1,
					ToolCallID:    p.CallID,
					Stdout:        output,
					ExitCode:      extractExitCode(output),
					ParentEventID: parentEventID,
				})
			}

		case "compacted":
			// Emit a CompactionBoundary so the formatter records a
			// minimap compaction marker (blue "C") and the terminal
			// view can show a context-compression indicator.
			if current != nil {
				emit(model.RenderEvent{
					Type:      "CompactionBoundary",
					Timestamp: ts,
					TurnIndex: len(attempts) - 1,
				})
			}
		}
	}

	// Trailing "推理中…" row for a turn still bracket-open at EOF. Runs
	// only when the trailing turn already has visible content: a bare
	// task_started must not be resurrected by the progress marker.
	if turnOpen && current != nil && codexRenderAttemptVisible(current) {
		if sourceModTime != nil {
			if evt, ok := shared.TrailingInProgress(true, *sourceModTime, len(attempts)-1); ok {
				emit(evt)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, skipped, err
	}

	visible := make(map[*codexRenderAttempt]bool, len(attempts))
	original := 0
	for _, attempt := range attempts {
		if !codexRenderAttemptVisible(attempt) {
			continue
		}
		attempt.originalIndex = original
		original++
		visible[attempt] = true
	}
	activeIndex := make(map[*codexRenderAttempt]int, len(active))
	for _, attempt := range active {
		if visible[attempt] {
			activeIndex[attempt] = len(activeIndex)
		}
	}

	var events []model.RenderEvent
	var appendAttempt func(*codexRenderAttempt, bool)
	appendGroup := func(group *codexRenderRollback, target *codexRenderAttempt, rolledBackTarget bool) {}
	appendAttempt = func(attempt *codexRenderAttempt, rolledBack bool) {
		if !visible[attempt] {
			return
		}
		idx := activeIndex[attempt]
		if rolledBack {
			idx = -(attempt.originalIndex + 1)
		}
		for i, evt := range attempt.events {
			evt.TurnIndex = idx
			if i == 0 && evt.Type == "TurnBoundary" {
				if evt.Metadata == nil {
					evt.Metadata = map[string]any{}
				}
				evt.Metadata["original_turn_index"] = attempt.originalIndex
				if rolledBack {
					evt.Metadata["rolled_back"] = true
				}
			}
			events = append(events, evt)
		}
		for _, group := range attempt.groups {
			appendGroup(group, attempt, rolledBack)
		}
	}
	appendGroup = func(group *codexRenderRollback, target *codexRenderAttempt, rolledBackTarget bool) {
		visibleRemoved := make([]*codexRenderAttempt, 0, len(group.removed))
		for _, attempt := range group.removed {
			if visible[attempt] {
				visibleRemoved = append(visibleRemoved, attempt)
			}
		}
		if len(visibleRemoved) == 0 {
			return
		}
		targetIndex := -1
		resumeTurn := 0
		if target != nil {
			resumeTurn = target.originalIndex + 1
			if rolledBackTarget {
				targetIndex = -(target.originalIndex + 1)
			} else {
				targetIndex = activeIndex[target]
			}
		}
		meta := map[string]any{
			"count":       len(visibleRemoved),
			"resume_turn": resumeTurn,
		}
		events = append(events, model.RenderEvent{
			Type:      "RollbackStart",
			Timestamp: group.timestamp,
			TurnIndex: targetIndex,
			AgentType: "codex",
			Metadata:  meta,
		})
		for _, attempt := range visibleRemoved {
			appendAttempt(attempt, true)
		}
		events = append(events, model.RenderEvent{
			Type:      "RollbackEnd",
			Timestamp: group.timestamp,
			TurnIndex: targetIndex,
			AgentType: "codex",
			Metadata:  meta,
		})
	}

	for _, group := range rootGroups {
		appendGroup(group, nil, false)
	}
	for _, attempt := range active {
		appendAttempt(attempt, false)
	}

	return events, skipped, nil
}

// parseArguments attempts to unmarshal a JSON string into map[string]any.
// On failure, it wraps the raw string as {"args": args}.
func parseArguments(args string) map[string]any {
	if args == "" {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(args), &result); err != nil {
		return map[string]any{"args": args}
	}
	return result
}

// LiveRevision is a stat-only change marker for live-tail polling.
func (r *CodexReader) LiveRevision(id string) (int64, error) {
	path := r.findSessionFile(id)
	if path == "" {
		return 0, fmt.Errorf("codex session not found: %s", id)
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.ModTime().UnixNano() + info.Size(), nil
}
