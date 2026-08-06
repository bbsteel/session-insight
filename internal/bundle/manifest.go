// Package bundle implements the portable session export/import archive
// (".sibundle", a gzipped tar) used to migrate sessions between
// SessionInsight instances. It is a pure package: no server or db imports.
//
// Redaction (see redact.go) is a best-effort heuristic over known secret
// shapes and home-directory paths. It is NOT a guarantee that all sensitive
// data is removed; exporters must review bundles before sharing them.
package bundle

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// Format is the required manifest format tag.
const Format = "session-insight-bundle"

// FormatVersion is the current bundle schema version. Importers reject
// bundles with a higher version.
const FormatVersion = 1

// ErrInvalidBundle marks every manifest/content validation failure so
// callers (HTTP layer) can map it to a client error without string scraping.
var ErrInvalidBundle = errors.New("invalid session bundle")

// ErrUnsupportedVersion marks manifests written by a newer SessionInsight.
var ErrUnsupportedVersion = errors.New("unsupported bundle format version")

// Options records how the bundle was produced.
type Options struct {
	IncludeRaw bool `json:"include_raw"`
	Redacted   bool `json:"redacted"`
}

// SessionEntry is one exported session's manifest record.
type SessionEntry struct {
	AgentType string    `json:"agent_type"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	File      string    `json:"file"`
	Redacted  bool      `json:"redacted"`
	RawDir    string    `json:"raw_dir,omitempty"`
}

// Manifest is the bundle's manifest.json.
type Manifest struct {
	Format            string         `json:"format"`
	FormatVersion     int            `json:"format_version"`
	CreatedAt         time.Time      `json:"created_at"`
	OriginHost        string         `json:"origin_host"`
	SIVersion         string         `json:"si_version,omitempty"`
	CaseLabel         string         `json:"case_label,omitempty"`
	RelatedSessionIDs []string       `json:"related_session_ids,omitempty"`
	Options           Options        `json:"options"`
	Sessions          []SessionEntry `json:"sessions"`
}

// Validate checks the manifest's structural contract: format tag, supported
// version, at least one session, and file/raw-dir names that stay confined
// to their bundle subdirectory (no absolute paths, no "..").
func Validate(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("%w: nil manifest", ErrInvalidBundle)
	}
	if m.Format != Format {
		return fmt.Errorf("%w: format %q, want %q", ErrInvalidBundle, m.Format, Format)
	}
	if m.FormatVersion > FormatVersion {
		return fmt.Errorf("%w: bundle format_version %d, this SessionInsight supports up to %d",
			ErrUnsupportedVersion, m.FormatVersion, FormatVersion)
	}
	if m.FormatVersion < 1 {
		return fmt.Errorf("%w: format_version %d", ErrInvalidBundle, m.FormatVersion)
	}
	if len(m.Sessions) == 0 {
		return fmt.Errorf("%w: manifest has no sessions", ErrInvalidBundle)
	}
	seen := make(map[string]bool, len(m.Sessions))
	for i, se := range m.Sessions {
		if strings.TrimSpace(se.ID) == "" {
			return fmt.Errorf("%w: sessions[%d] has empty id", ErrInvalidBundle, i)
		}
		if !confinedName("sessions", se.File) {
			return fmt.Errorf("%w: sessions[%d] file %q escapes sessions/", ErrInvalidBundle, i, se.File)
		}
		if seen[se.File] {
			return fmt.Errorf("%w: duplicate session file %q", ErrInvalidBundle, se.File)
		}
		seen[se.File] = true
		if se.RawDir != "" && !confinedName("raw", se.RawDir) {
			return fmt.Errorf("%w: sessions[%d] raw_dir %q escapes raw/", ErrInvalidBundle, i, se.RawDir)
		}
	}
	return nil
}

// confinedName reports whether name is a single path element (or a safe
// relative path) that stays under dir when joined and cleaned.
func confinedName(dir, name string) bool {
	if name == "" || path.IsAbs(name) || strings.HasPrefix(name, "/") {
		return false
	}
	joined := path.Join(dir, name)
	if !strings.HasPrefix(joined, dir+"/") {
		return false
	}
	for _, part := range strings.Split(joined, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}
