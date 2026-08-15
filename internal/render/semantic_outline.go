package render

// semantic_outline.go builds the product-level "key event outline" positions
// (kind "outline") on top of the core positions produced by the formatter.
//
// The classifier is the single place that decides which facts become key
// events: adapters only normalize source facts, and the frontend never
// re-derives categories from agent or tool names. Classification is
// deliberately conservative — a false negative (a real check not listed)
// is acceptable, a false positive (noise promoted to a key event) is not.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

// Outline categories (stable machine codes; UI copy comes from i18n).
const (
	OutlineCatAnomaly    = "anomaly"
	OutlineCatContext    = "context"
	OutlineCatFileChange = "file_change"
	OutlineCatKeyResult  = "key_result"
)

// Outline event codes (stable; used for translation, tests, and stats).
const (
	OutlineCodeToolRejected        = "tool_rejected"
	OutlineCodeToolTimeout         = "tool_timeout"
	OutlineCodeToolFailed          = "tool_failed"
	OutlineCodeVerificationFailed  = "verification_failed"
	OutlineCodeDurationSpike       = "duration_spike"
	OutlineCodeMissingShutdown     = "missing_shutdown"
	OutlineCodeCompaction          = "compaction"
	OutlineCodeRollback            = "rollback"
	OutlineCodeFileCreated         = "file_created"
	OutlineCodeFileModified        = "file_modified"
	OutlineCodeFileDeleted         = "file_deleted"
	OutlineCodeFileRenamed         = "file_renamed"
	OutlineCodeFileChangeUnverifed = "file_change_unverified"
	OutlineCodeTestsPassed         = "tests_passed"
	OutlineCodeBuildSucceeded      = "build_succeeded"
	OutlineCodeLintPassed          = "lint_passed"
	OutlineCodeTypecheckPassed     = "typecheck_passed"
	OutlineCodeChecksPassed        = "checks_passed"
)

// outlineSummaryMaxRunes caps raw command/path summaries in outline payloads.
const outlineSummaryMaxRunes = 240

// OutlineStats reports aggregate classifier diagnostics. Counts only — no
// command arguments, diffs, or stderr ever reach logs.
type OutlineStats struct {
	Candidates       int
	Emitted          int
	SkippedNoLanding int
	ByCategory       map[string]int
}

// SemanticOutlineInput carries everything the classifier reads. Detail may be
// nil (session facts unavailable); CorePositions must be the formatter's
// output for the same events and layout.
type SemanticOutlineInput struct {
	Events        []model.RenderEvent
	Detail        *model.SessionDetail
	CorePositions []RenderPosition
}

// coreLanding is a resolved jump target taken from a core position.
type coreLanding struct {
	lineStart    int
	lineEnd      *int
	logicalStart float64
	hasLogical   bool
	sourceKey    string
}

// outlineCandidate is a classified fact before landing resolution.
type outlineCandidate struct {
	category     string
	code         string
	precision    string // "exact" | "estimated"
	severity     string
	summary      string
	toolName     string
	filePath     string
	previousPath string
	durationMs   int64
	tsMs         float64
	turnIndex    int
	stableID     string // call ID / event ID / deterministic fallback
	occurrence   int    // per-call path index for multi-file events
	// preferred landing sources, in priority order
	callID string
}

// BuildSemanticOutline classifies normalized events and session facts into
// sparse "outline" positions. It never executes shell, never reads files
// beyond the session record, and never fabricates a landing: candidates
// without a resolvable core position are skipped and counted in stats.
func BuildSemanticOutline(in SemanticOutlineInput) ([]RenderPosition, OutlineStats) {
	stats := OutlineStats{ByCategory: map[string]int{}}
	candidates := collectOutlineCandidates(in, &stats)
	stats.Candidates = len(candidates)

	idx := indexCorePositions(in.CorePositions)
	out := make([]RenderPosition, 0, len(candidates))
	for _, c := range candidates {
		landing, ok := resolveOutlineLanding(c, idx)
		if !ok {
			stats.SkippedNoLanding++
			continue
		}
		key := fmt.Sprintf("outline:%s:%s:%d:%s:%d",
			c.category, c.code, c.turnIndex, c.stableID, c.occurrence)
		payload := map[string]any{
			"category":            c.category,
			"code":                c.code,
			"precision":           c.precision,
			"source_position_key": landing.sourceKey,
		}
		if c.summary != "" {
			payload["summary"] = c.summary
		}
		if landing.hasLogical {
			payload["logical_start"] = landing.logicalStart
		}
		if c.tsMs != 0 {
			payload["ts_ms"] = c.tsMs
		}
		if c.toolName != "" {
			payload["tool_name"] = c.toolName
		}
		if c.filePath != "" {
			payload["file_path"] = c.filePath
		}
		if c.previousPath != "" {
			payload["previous_file_path"] = c.previousPath
		}
		if c.durationMs > 0 {
			payload["duration_ms"] = float64(c.durationMs)
		}
		label := c.filePath
		if label == "" {
			label = c.summary
		}
		out = append(out, RenderPosition{
			PositionKey: key,
			Kind:        "outline",
			TurnIndex:   c.turnIndex,
			LineStart:   landing.lineStart,
			LineEnd:     landing.lineEnd,
			Label:       label,
			Severity:    c.severity,
			Payload:     payload,
		})
		stats.Emitted++
		stats.ByCategory[c.category]++
	}
	return out, stats
}

// collectOutlineCandidates applies the priority chain to every depth-0 tool
// call and folds in session-level facts. Priority per call (only the highest
// semantic survives):
//
//  1. rejected / timeout / failed            → anomaly
//  2. success + file edit                    → file_change
//  3. success + conservative verify command  → key_result
//  4. edit call with missing result          → file_change_unverified
//  5. anything else                          → not an outline event
func collectOutlineCandidates(in SemanticOutlineInput, stats *OutlineStats) []outlineCandidate {
	outcomes := computeToolOutcomes(in.Events)
	var out []outlineCandidate

	for _, evt := range in.Events {
		if evt.Type != "ToolInvocation" || evt.Depth != 0 {
			continue
		}
		outcome := outcomes[evt.ToolCallID]
		tsMs := float64(0)
		if !evt.Timestamp.IsZero() {
			tsMs = float64(evt.Timestamp.UnixMilli())
		}
		stableID := evt.ToolCallID
		if stableID == "" {
			stableID = evt.EventID
		}
		if stableID == "" {
			stableID = fmt.Sprintf("seq%d", evt.Seq)
		}
		base := outlineCandidate{
			turnIndex:  evt.TurnIndex,
			toolName:   evt.ToolName,
			tsMs:       tsMs,
			stableID:   stableID,
			callID:     evt.ToolCallID,
			durationMs: outcome.durationMs,
		}

		verifyKind := classifyVerificationCommand(evt.ToolInput)

		switch {
		case outcome.status == "rejected":
			c := base
			c.category, c.code, c.precision, c.severity =
				OutlineCatAnomaly, OutlineCodeToolRejected, "exact", "warning"
			c.summary = commandSummary(evt.ToolInput)
			out = append(out, c)
		case outcome.status == "timeout":
			c := base
			c.category, c.code, c.precision, c.severity =
				OutlineCatAnomaly, OutlineCodeToolTimeout, "exact", "error"
			c.summary = commandSummary(evt.ToolInput)
			out = append(out, c)
		case outcome.status == "error":
			c := base
			c.category, c.precision, c.severity = OutlineCatAnomaly, "exact", "error"
			if verifyKind != "" {
				c.code = OutlineCodeVerificationFailed
			} else {
				c.code = OutlineCodeToolFailed
			}
			c.summary = commandSummary(evt.ToolInput)
			out = append(out, c)
		case outcome.status == "ok" && model.IsEditTool(evt.ToolName):
			out = append(out, fileChangeCandidates(base, evt)...)
		case outcome.status == "ok" && verifyKind != "":
			c := base
			c.category, c.precision = OutlineCatKeyResult, "exact"
			c.code = map[string]string{
				"tests":     OutlineCodeTestsPassed,
				"build":     OutlineCodeBuildSucceeded,
				"lint":      OutlineCodeLintPassed,
				"typecheck": OutlineCodeTypecheckPassed,
				"checks":    OutlineCodeChecksPassed,
			}[verifyKind]
			c.summary = commandSummary(evt.ToolInput)
			out = append(out, c)
		case outcome.status == "" && model.IsEditTool(evt.ToolName):
			// Call seen, result missing: never claim a successful modification.
			for i, call := range dedupeEditCalls(model.ExtractEditCalls(evt)) {
				c := base
				c.category, c.code, c.precision, c.severity =
					OutlineCatFileChange, OutlineCodeFileChangeUnverifed, "estimated", "warning"
				c.filePath, c.previousPath, c.occurrence = call.FilePath, call.PreviousPath, i
				out = append(out, c)
			}
		}
	}

	// Context events come from core positions (compaction markers and
	// rollback folds), so each underlying event yields exactly one item.
	for i := range in.CorePositions {
		p := &in.CorePositions[i]
		switch {
		case p.Kind == "compaction":
			out = append(out, outlineCandidate{
				category: OutlineCatContext, code: OutlineCodeCompaction,
				precision: "exact", turnIndex: p.TurnIndex,
				stableID: p.PositionKey,
			})
		case p.Kind == "fold" && p.Payload["level"] == "rollback":
			out = append(out, outlineCandidate{
				category: OutlineCatContext, code: OutlineCodeRollback,
				precision: "exact", turnIndex: p.TurnIndex,
				stableID: p.PositionKey,
			})
		}
	}

	if in.Detail != nil {
		out = append(out, sessionFactCandidates(in.Detail)...)
	}
	return out
}

// fileChangeCandidates emits one candidate per normalized, deduplicated path
// of a successful edit call.
func fileChangeCandidates(base outlineCandidate, evt model.RenderEvent) []outlineCandidate {
	calls := dedupeEditCalls(model.ExtractEditCalls(evt))
	out := make([]outlineCandidate, 0, len(calls))
	for i, call := range calls {
		c := base
		c.category, c.precision = OutlineCatFileChange, "exact"
		c.filePath, c.previousPath, c.occurrence = call.FilePath, call.PreviousPath, i
		switch {
		case call.PreviousPath != "":
			c.code = OutlineCodeFileRenamed
			c.summary = call.PreviousPath + " → " + call.FilePath
		case call.NewString == "" && call.OldString != "":
			c.code = OutlineCodeFileDeleted
		case call.OldString == "" && call.NewString != "":
			c.code = OutlineCodeFileCreated
		default:
			c.code = OutlineCodeFileModified
		}
		out = append(out, c)
	}
	return out
}

// dedupeEditCalls normalizes paths (trim, forward slashes, strip leading ./)
// and keeps one entry per path, ordered by normalized path so multi-file
// occurrence numbers never depend on map iteration or patch section order.
func dedupeEditCalls(calls []model.EditCall) []model.EditCall {
	seen := make(map[string]model.EditCall, len(calls))
	for _, c := range calls {
		p := normalizeOutlinePath(c.FilePath)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		c.FilePath = p
		c.PreviousPath = normalizeOutlinePath(c.PreviousPath)
		seen[p] = c
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	out := make([]model.EditCall, 0, len(paths))
	for _, p := range paths {
		out = append(out, seen[p])
	}
	return out
}

func normalizeOutlinePath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	return p
}

// sessionFactCandidates lifts shared session-level anomaly facts (duration
// spikes, missing shutdown) into outline candidates. Both are estimated:
// the fact is real but the landing is the affected turn, not an exact line.
func sessionFactCandidates(detail *model.SessionDetail) []outlineCandidate {
	var out []outlineCandidate
	maxTurn := -1
	for _, tv := range detail.Turns {
		if tv.TurnIndex > maxTurn && !tv.RolledBack {
			maxTurn = tv.TurnIndex
		}
		for _, a := range tv.Anomalies {
			if a != "duration_spike" {
				continue
			}
			out = append(out, outlineCandidate{
				category: OutlineCatAnomaly, code: OutlineCodeDurationSpike,
				precision: "estimated", severity: "warning",
				turnIndex: tv.TurnIndex, durationMs: tv.DurationMs,
				stableID: fmt.Sprintf("turn%d", tv.TurnIndex),
			})
		}
	}
	if detail.AnomalySummary.MissingShutdown && maxTurn >= 0 {
		out = append(out, outlineCandidate{
			category: OutlineCatAnomaly, code: OutlineCodeMissingShutdown,
			precision: "estimated", severity: "warning",
			turnIndex: maxTurn,
			stableID:  "session",
		})
	}
	return out
}

// corePositionIndex provides landing resolution over core positions.
type corePositionIndex struct {
	toolByCallID map[string]*RenderPosition
	editByCallID map[string][]*RenderPosition
	turnByIndex  map[int]*RenderPosition
	byKey        map[string]*RenderPosition
}

func indexCorePositions(core []RenderPosition) corePositionIndex {
	idx := corePositionIndex{
		toolByCallID: make(map[string]*RenderPosition),
		editByCallID: make(map[string][]*RenderPosition),
		turnByIndex:  make(map[int]*RenderPosition),
		byKey:        make(map[string]*RenderPosition, len(core)),
	}
	for i := range core {
		p := &core[i]
		idx.byKey[p.PositionKey] = p
		switch p.Kind {
		case "tool":
			if id, _ := p.Payload["tool_call_id"].(string); id != "" {
				if _, exists := idx.toolByCallID[id]; !exists {
					idx.toolByCallID[id] = p
				}
			}
		case "edit":
			if id, _ := p.Payload["tool_call_id"].(string); id != "" {
				idx.editByCallID[id] = append(idx.editByCallID[id], p)
			}
		case "turn":
			if _, exists := idx.turnByIndex[p.TurnIndex]; !exists {
				idx.turnByIndex[p.TurnIndex] = p
			}
		}
	}
	return idx
}

// landingFromPosition derives a jump target from a core position. When
// preferResult is set and the position carries result coordinates (tool
// positions enriched once the result renders), the result range wins.
func landingFromPosition(p *RenderPosition, preferResult bool) coreLanding {
	l := coreLanding{lineStart: p.LineStart, lineEnd: p.LineEnd, sourceKey: p.PositionKey}
	if v, ok := p.Payload["logical_start"].(float64); ok {
		l.logicalStart, l.hasLogical = v, true
	}
	if preferResult {
		if v, ok := p.Payload["result_line_start"].(float64); ok {
			l.lineStart = int(v)
			l.lineEnd = nil
			l.logicalStart, l.hasLogical = 0, false
			if lv, ok := p.Payload["result_logical_start"].(float64); ok {
				l.logicalStart, l.hasLogical = lv, true
			}
		}
	}
	return l
}

// resolveOutlineLanding picks the most precise available target:
// tool result line → matching edit marker → tool call line → turn line.
// Candidates with no resolvable target return ok=false and are skipped.
func resolveOutlineLanding(c outlineCandidate, idx corePositionIndex) (coreLanding, bool) {
	// Context candidates carry their source position key directly.
	if c.category == OutlineCatContext {
		if p, ok := idx.byKey[c.stableID]; ok {
			return landingFromPosition(p, false), true
		}
		return coreLanding{}, false
	}
	if c.callID != "" {
		if tp, ok := idx.toolByCallID[c.callID]; ok {
			// File changes land on their own edit marker when one matches the
			// path; everything else prefers the result line.
			if c.category == OutlineCatFileChange {
				for _, ep := range idx.editByCallID[c.callID] {
					if ep.Label == c.filePath {
						return landingFromPosition(ep, false), true
					}
				}
			}
			return landingFromPosition(tp, true), true
		}
	}
	if tp, ok := idx.turnByIndex[c.turnIndex]; ok {
		return landingFromPosition(tp, false), true
	}
	return coreLanding{}, false
}

// commandSummary extracts a raw, rune-capped command string from structured
// tool input. No shell execution, no filesystem access.
func commandSummary(input map[string]any) string {
	cmd := extractCommand(input)
	return truncateRunes(cmd, outlineSummaryMaxRunes)
}

// extractCommand reads the command from common structured fields.
func extractCommand(input map[string]any) string {
	if input == nil {
		return ""
	}
	for _, key := range []string{"command", "cmd"} {
		if v, ok := input[key].(string); ok && v != "" {
			return v
		}
	}
	if v, ok := input["args"].([]any); ok {
		parts := make([]string, 0, len(v))
		for _, a := range v {
			if s, ok := a.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// verificationRule matches a normalized command segment against a
// conservative allowlist. kind feeds the key_result / verification_failed
// code mapping.
type verificationRule struct {
	kind string // tests | build | lint | typecheck | checks
	head []string
}

// verificationAllowlist is table-driven on purpose: every entry is covered by
// classifier tests, and extending coverage means extending this table, never
// loosening the matcher.
var verificationAllowlist = []verificationRule{
	{"tests", []string{"go", "test"}},
	{"lint", []string{"go", "vet"}},
	{"build", []string{"go", "build"}},
	{"tests", []string{"pytest"}},
	{"tests", []string{"python", "-m", "pytest"}},
	{"tests", []string{"python3", "-m", "pytest"}},
	{"tests", []string{"cargo", "test"}},
	{"build", []string{"cargo", "build"}},
	{"checks", []string{"cargo", "check"}},
	{"lint", []string{"cargo", "clippy"}},
	{"tests", []string{"dotnet", "test"}},
	{"build", []string{"dotnet", "build"}},
	{"tests", []string{"mvn", "test"}},
	{"checks", []string{"mvn", "verify"}},
	{"build", []string{"mvn", "package"}},
	{"tests", []string{"gradle", "test"}},
	{"build", []string{"gradle", "build"}},
	{"checks", []string{"gradle", "check"}},
	{"tests", []string{"./gradlew", "test"}},
	{"build", []string{"./gradlew", "build"}},
	{"checks", []string{"./gradlew", "check"}},
	{"tests", []string{"ctest"}},
	{"tests", []string{"swift", "test"}},
	{"tests", []string{"make", "test"}},
	{"build", []string{"make", "build"}},
	{"checks", []string{"make", "check"}},
}

// npmLikeSubcommand maps `npm|pnpm|yarn|bun [run] <sub>` subcommands.
var npmLikeSubcommands = map[string]string{
	"test": "tests", "build": "build", "lint": "lint",
	"typecheck": "typecheck", "type-check": "typecheck", "check": "checks",
}

// classifyVerificationCommand returns the verification kind for commands that
// conservatively match the allowlist, or "" for everything else. It handles
// whitespace normalization, simple `cd <dir> &&` prefixes, and `&&`/`;`
// command chains; it is not a general shell parser and never will be.
//
// A single `&` (background operator) or one inside a quoted argument makes
// the outcome status unattributable to the matched segment — e.g. in
// `go test ./... & echo done` the recorded status belongs to `echo done`.
// Conservative rule: any `&` outside `&&` rejects the command (a false
// negative), never a guessed key result.
func classifyVerificationCommand(input map[string]any) string {
	cmd := extractCommand(input)
	if cmd == "" {
		return ""
	}
	if strings.Contains(strings.ReplaceAll(cmd, "&&", ""), "&") {
		return ""
	}
	// Split chained commands; each segment is matched independently so
	// `cd x && go test ./...` resolves via its second segment. The single-&
	// rejection above makes this normalization safe.
	for _, segment := range strings.FieldsFunc(strings.ReplaceAll(cmd, "&&", ";"), func(r rune) bool { return r == ';' }) {
		if kind := matchVerificationSegment(segment); kind != "" {
			return kind
		}
	}
	return ""
}

func matchVerificationSegment(segment string) string {
	tokens := strings.Fields(segment)
	if len(tokens) == 0 {
		return ""
	}
	// Strip a leading `cd <dir>` prefix.
	if tokens[0] == "cd" && len(tokens) > 2 {
		tokens = tokens[2:]
	}
	switch tokens[0] {
	case "npm", "pnpm", "yarn", "bun":
		rest := tokens[1:]
		if len(rest) > 0 && rest[0] == "run" {
			rest = rest[1:]
		}
		if len(rest) > 0 {
			return npmLikeSubcommands[rest[0]]
		}
		return ""
	case "tsc":
		for _, t := range tokens[1:] {
			if t == "--noEmit" || t == "--noemit" {
				return "typecheck"
			}
		}
		return ""
	case "make":
		// `make -C dir test` style flag prefixes resolve to their target.
		rest := tokens[1:]
		for i := 0; i < len(rest); i++ {
			tok := rest[i]
			if tok == "-C" || tok == "-f" || tok == "--directory" || tok == "--file" {
				i++ // these flags consume the next token as their argument
				continue
			}
			if strings.HasPrefix(tok, "-") {
				continue
			}
			return matchVerificationHead([]string{"make", tok})
		}
		return ""
	}
	return matchVerificationHead(tokens)
}

func matchVerificationHead(tokens []string) string {
	for _, rule := range verificationAllowlist {
		if len(tokens) < len(rule.head) {
			continue
		}
		match := true
		for i, tok := range rule.head {
			if tokens[i] != tok {
				match = false
				break
			}
		}
		if match {
			return rule.kind
		}
	}
	return ""
}

// SortPositions orders positions deterministically: line_start, then
// line_end (absent sorts as line_start), then position_key. The classifier
// merge and the DB read path share this tie-breaker.
func SortPositions(positions []RenderPosition) {
	sort.SliceStable(positions, func(i, j int) bool {
		a, b := positions[i], positions[j]
		if a.LineStart != b.LineStart {
			return a.LineStart < b.LineStart
		}
		ae, be := a.LineStart, b.LineStart
		if a.LineEnd != nil {
			ae = *a.LineEnd
		}
		if b.LineEnd != nil {
			be = *b.LineEnd
		}
		if ae != be {
			return ae < be
		}
		return a.PositionKey < b.PositionKey
	})
}
