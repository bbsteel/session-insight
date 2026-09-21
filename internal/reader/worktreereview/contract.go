// Package worktreereview reads Worktree Review Session Journals: one
// directory per review attempt with metadata.json (atomic-replace),
// events.jsonl (append-only worktree-review.event/v1) and result.json
// (immutable worktree-review.cli.result/v1, written once at terminal state).
//
// Contract and tolerance rules (B-200):
//   - Every file carries an explicit schema version. Unknown versions are
//     rejected (metadata/result) or degraded (unknown-version event lines are
//     skipped and counted), never silently parsed.
//   - events.jsonl is append-only: a half-written trailing line is tolerated
//     and counted as skipped, never fatal.
//   - A missing result.json means the attempt never reached a terminal state;
//     it is not an error.
//   - Corrupt or half-written files must never cause old sessions to be
//     dropped or deleted.
package worktreereview

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// SessionMetadataSchemaV1 versions metadata.json.
	SessionMetadataSchemaV1 = "worktree-review.session-metadata/v1"
	// EventSchemaV1 versions each events.jsonl line.
	EventSchemaV1 = "worktree-review.event/v1"
	// ResultSchemaV1 versions result.json.
	ResultSchemaV1 = "worktree-review.cli.result/v1"

	metadataFile = "metadata.json"
	eventsFile   = "events.jsonl"
	resultFile   = "result.json"
)

// UnsupportedSchemaError marks a document whose declared schema version this
// reader does not understand. Callers surface it as "version incompatible",
// never as data corruption.
type UnsupportedSchemaError struct {
	File   string
	Schema string
}

func (e *UnsupportedSchemaError) Error() string {
	return fmt.Sprintf("%s: unsupported schema %q", e.File, e.Schema)
}

// IsUnsupportedSchema reports whether err is (or wraps) an
// UnsupportedSchemaError.
func IsUnsupportedSchema(err error) bool {
	var target *UnsupportedSchemaError
	return errors.As(err, &target)
}

// SessionMetadata is the worktree-review.session-metadata/v1 document.
type SessionMetadata struct {
	Schema                string    `json:"schema"`
	AttemptID             string    `json:"attempt_id"`
	HeartbeatAt           time.Time `json:"heartbeat_at"`
	LastPersistedSequence int       `json:"last_persisted_sequence"`
}

// ReviewEvent is one worktree-review.event/v1 envelope.
type ReviewEvent struct {
	Schema        string         `json:"schema"`
	Sequence      int            `json:"sequence"`
	OccurredAt    time.Time      `json:"occurred_at"`
	AttemptID     string         `json:"attempt_id"`
	Surface       string         `json:"surface"`
	EventType     string         `json:"event_type"`
	Payload       map[string]any `json:"payload"`
	Reconstructed bool           `json:"reconstructed"`
}

// ResultDocument is the subset of worktree-review.cli.result/v1 the reader
// needs. The full document stays opaque to this package.
type ResultDocument struct {
	Schema    string `json:"schema"`
	AttemptID string `json:"attempt_id"`
	GateState string `json:"gate_state"`
	Summary   string `json:"summary"`
}

// EventsReadResult carries the parsed events plus degradation facts.
type EventsReadResult struct {
	Events []ReviewEvent
	// SkippedMalformed counts lines that were not valid JSON (a crash can
	// leave a half-written trailing line).
	SkippedMalformed int
	// SkippedVersion counts well-formed lines whose schema version this
	// reader does not support (forward compatibility degradation).
	SkippedVersion int
}

// SessionIDForAttempt is the stable mapping between a Worktree Review
// attempt and a Session Insight session: one attempt is exactly one session.
func SessionIDForAttempt(attemptID string) string {
	return attemptID
}

// validAttemptID guards against path probes: an attempt id is a single safe
// path element.
func validAttemptID(id string) bool {
	return id != "" && filepath.Base(id) == id && id != "." && id != ".." &&
		!strings.ContainsAny(id, "/\\")
}

// AttemptDir resolves the attempt directory under root, or "" for an
// invalid id. Beyond the single-segment id guard, the joined path itself is
// verified with a HasPrefix guard against the cleaned root — the pattern
// CodeQL models as a path-traversal sanitizer — so a caller-controlled id
// can never escape the journal root.
func AttemptDir(root, attemptID string) string {
	if !validAttemptID(attemptID) {
		return ""
	}
	cleanRoot := filepath.Clean(root)
	dir := filepath.Join(cleanRoot, attemptID)
	if !strings.HasPrefix(dir, cleanRoot+string(os.PathSeparator)) {
		return ""
	}
	return dir
}

// MetadataPath, EventsPath and ResultPath name the three journal documents.
// An invalid attempt id yields "", matching the repo's invalid-path
// convention (findSessionFile and friends return empty strings); callers must
// never join a filename into the process working directory.
func MetadataPath(root, attemptID string) string {
	dir := AttemptDir(root, attemptID)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, metadataFile)
}
func EventsPath(root, attemptID string) string {
	dir := AttemptDir(root, attemptID)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, eventsFile)
}
func ResultPath(root, attemptID string) string {
	dir := AttemptDir(root, attemptID)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, resultFile)
}

// ParseSessionMetadata validates and parses metadata.json. Unknown schema
// versions are rejected with UnsupportedSchemaError.
func ParseSessionMetadata(data []byte) (*SessionMetadata, error) {
	var meta SessionMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("metadata.json: %w", err)
	}
	if meta.Schema != SessionMetadataSchemaV1 {
		return nil, &UnsupportedSchemaError{File: metadataFile, Schema: meta.Schema}
	}
	if !validAttemptID(meta.AttemptID) {
		return nil, fmt.Errorf("metadata.json: invalid attempt_id %q", meta.AttemptID)
	}
	return &meta, nil
}

// ParseEventLine validates and parses one events.jsonl line. Unknown schema
// versions are rejected with UnsupportedSchemaError so the caller can count
// them as degraded rather than corrupt.
func ParseEventLine(line []byte) (*ReviewEvent, error) {
	var event ReviewEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return nil, fmt.Errorf("events.jsonl line: %w", err)
	}
	if event.Schema != EventSchemaV1 {
		return nil, &UnsupportedSchemaError{File: eventsFile, Schema: event.Schema}
	}
	if event.Sequence < 1 {
		return nil, fmt.Errorf("events.jsonl line: sequence %d < 1", event.Sequence)
	}
	if event.AttemptID == "" {
		return nil, fmt.Errorf("events.jsonl line: empty attempt_id")
	}
	return &event, nil
}

// maxEventLineBytes bounds one events.jsonl line. An oversized line is
// malformed input: it is skipped and counted, and reading continues with the
// next line instead of failing the whole journal.
const maxEventLineBytes = 4 * 1024 * 1024

// ReadEventsFile tolerantly parses an append-only events.jsonl. Malformed,
// oversized and unknown-version lines are skipped and counted; valid events
// keep file order. Sequence uniqueness and strict increase are the writer's
// responsibility; the reader trusts persisted order.
func ReadEventsFile(path string) (*EventsReadResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := &EventsReadResult{}
	reader := bufio.NewReader(file)
	var line []byte
	for {
		fragment, readErr := reader.ReadSlice('\n')
		if len(line)+len(fragment) > maxEventLineBytes {
			// Discard the whole oversized line: drain fragments until the
			// newline, then count it once as malformed.
			line = nil
			for readErr == bufio.ErrBufferFull {
				_, readErr = reader.ReadSlice('\n')
			}
			result.SkippedMalformed++
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return result, fmt.Errorf("events.jsonl read: %w", readErr)
			}
			continue
		}
		line = append(line, fragment...)
		if readErr == bufio.ErrBufferFull {
			continue
		}

		if len(strings.TrimSpace(string(line))) != 0 {
			event, parseErr := ParseEventLine(line)
			if parseErr != nil {
				if IsUnsupportedSchema(parseErr) {
					result.SkippedVersion++
				} else {
					result.SkippedMalformed++
				}
			} else {
				result.Events = append(result.Events, *event)
			}
		}
		line = nil

		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return result, fmt.Errorf("events.jsonl read: %w", readErr)
		}
	}
	return result, nil
}

// ParseResultDocument validates and parses result.json. Unknown schema
// versions are rejected with UnsupportedSchemaError.
func ParseResultDocument(data []byte) (*ResultDocument, error) {
	var doc ResultDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("result.json: %w", err)
	}
	if doc.Schema != ResultSchemaV1 {
		return nil, &UnsupportedSchemaError{File: resultFile, Schema: doc.Schema}
	}
	return &doc, nil
}
