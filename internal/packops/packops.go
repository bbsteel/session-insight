// Package packops holds the session-migration bundle assembly logic shared
// by the HTTP export/import handlers (internal/server) and the `pack` CLI
// subcommand (package main), so the two paths cannot diverge. The bundle
// bytes themselves are produced/consumed by internal/bundle; this package
// adds reader lookups, provenance hydration, raw-file capture, redaction,
// and import-record bookkeeping.
package packops

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/bundle"
	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/imported"
)

// Per-file and total caps for raw source files attached to an export bundle.
const (
	exportRawFileCap  = 64 << 20  // 64 MiB per source file
	exportRawTotalCap = 256 << 20 // 256 MiB across all sessions
)

// Selection identifies one session to export.
type Selection struct {
	AgentType string
	ID        string
}

// ExportOptions controls how a bundle is assembled.
type ExportOptions struct {
	IncludeRaw bool
	Redact     bool
	CaseLabel  string
	SIVersion  string
}

// ExportResult is the assembled bundle content plus the selections that
// were requested but unreadable (unknown agent type, missing session).
type ExportResult struct {
	Manifest bundle.Manifest
	Payloads []bundle.SessionPayload
	Skipped  []Selection
}

// BuildExport assembles manifest + payloads for the requested selections.
// Unknown or unreadable sessions are skipped (recorded in Skipped), not a
// hard failure; callers decide how to surface them.
func BuildExport(readers []reader.BaseSessionReader, database *db.DB, sels []Selection, opts ExportOptions) (*ExportResult, error) {
	homeDir, _ := os.UserHomeDir()
	hostname, _ := os.Hostname()

	res := &ExportResult{
		Manifest: bundle.Manifest{
			Format:        bundle.Format,
			FormatVersion: bundle.FormatVersion,
			CreatedAt:     time.Now().UTC(),
			OriginHost:    hostname,
			SIVersion:     opts.SIVersion,
			CaseLabel:     strings.TrimSpace(opts.CaseLabel),
			Options:       bundle.Options{IncludeRaw: opts.IncludeRaw, Redacted: opts.Redact},
		},
	}

	var rawTotal int64
	for _, sel := range sels {
		rd := readerForAgent(readers, sel.AgentType)
		if rd == nil {
			res.Skipped = append(res.Skipped, sel)
			continue
		}
		detail, err := rd.GetSession(sel.ID)
		if err != nil || detail == nil {
			// Unknown/unreadable sessions are skipped, not a hard failure.
			res.Skipped = append(res.Skipped, sel)
			continue
		}
		events, err := rd.GetRenderEvents(sel.ID)
		if err != nil {
			events = []model.RenderEvent{}
		}

		// Prefer live reader provenance; if absent, hydrate stored snapshot
		// (same pattern as the session detail handler).
		if detail.Provenance == nil && database != nil {
			if p, ok, _ := database.GetProvenance(detail.AgentType, detail.ID); ok {
				detail.Provenance = p
			}
		}

		entry := bundle.SessionEntry{
			AgentType: detail.AgentType,
			ID:        detail.ID,
			Title:     detail.Name,
			CreatedAt: detail.CreatedAt,
			UpdatedAt: detail.UpdatedAt,
			File:      bundleSessionFileName(detail.AgentType, detail.ID),
		}

		var rawFiles map[string][]byte
		// Raw source files are not covered by RedactSession heuristics. When
		// redaction is requested, skip attaching them rather than shipping
		// unredacted transcripts alongside a redacted detail payload.
		if opts.IncludeRaw && !opts.Redact && detail.Provenance != nil && homeDir != "" {
			entry.RawDir = rawDirName(detail.AgentType, detail.ID)
			rawFiles = collectRawFiles(detail.Provenance.Sources, homeDir, &rawTotal)
			if len(rawFiles) == 0 {
				entry.RawDir = ""
			}
		}

		if opts.Redact {
			detail = bundle.DeepCopyDetail(detail)
			events = bundle.DeepCopyEvents(events)
			bundle.RedactSession(detail, events, homeDir)
			entry.Redacted = true
		}

		res.Manifest.Sessions = append(res.Manifest.Sessions, entry)
		res.Manifest.RelatedSessionIDs = append(res.Manifest.RelatedSessionIDs, detail.ID)
		res.Payloads = append(res.Payloads, bundle.SessionPayload{
			Entry: entry, Detail: detail, RenderEvents: events, RawFiles: rawFiles,
		})
	}
	return res, nil
}

// ImportBundle extracts a bundle into importRoot/<bundleID>/ and records
// per-session import provenance in the database. The imported reader picks
// the sessions up on the next index pass.
func ImportBundle(database *db.DB, importRoot string, r io.Reader) (bundleID string, m *bundle.Manifest, err error) {
	bundleID, manifest, err := bundle.Extract(r, importRoot)
	if err != nil {
		return "", nil, err
	}

	now := time.Now().UTC()
	for _, se := range manifest.Sessions {
		importedID := imported.JoinSessionID(bundleID, se.ID)
		if err := database.UpsertImportRecord(db.ImportRecord{
			AgentType:         imported.AgentType,
			SessionID:         importedID,
			BundleID:          bundleID,
			OriginHost:        manifest.OriginHost,
			OriginalAgentType: se.AgentType,
			OriginalSessionID: se.ID,
			CaseLabel:         manifest.CaseLabel,
			Redacted:          se.Redacted,
			ImportedAt:        now,
		}); err != nil {
			return "", nil, fmt.Errorf("record import %s: %w", importedID, err)
		}
	}
	return bundleID, manifest, nil
}

func readerForAgent(readers []reader.BaseSessionReader, agentType string) reader.BaseSessionReader {
	for _, rd := range readers {
		if rd.AgentType() == agentType {
			return rd
		}
	}
	return nil
}

// bundleSessionFileName derives the sessions/ entry name, prefixed by agent
// type so ids from different agents cannot collide inside one bundle.
func bundleSessionFileName(agentType, id string) string {
	return imported.SanitizeID(agentType) + "-" + imported.SanitizeID(id) + ".json"
}

func rawDirName(agentType, id string) string {
	return imported.SanitizeID(agentType) + "-" + imported.SanitizeID(id)
}

// collectRawFiles reads provenance source files under the user's home
// directory, enforcing per-file and total caps. Files outside home or
// unreadable are skipped (best-effort attachment).
func collectRawFiles(sources []model.SessionSourceFile, homeDir string, total *int64) map[string][]byte {
	var out map[string][]byte
	for _, src := range sources {
		if src.State != model.SourcePresent || src.Path == "" {
			continue
		}
		abs, err := filepath.Abs(src.Path)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(homeDir, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue // never attach files outside the user's home
		}
		info, err := os.Stat(abs)
		if err != nil || !info.Mode().IsRegular() || info.Size() > exportRawFileCap {
			continue
		}
		if *total+info.Size() > exportRawTotalCap {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		if out == nil {
			out = make(map[string][]byte)
		}
		name := imported.SanitizeID(filepath.Base(abs))
		// Disambiguate same-name sources deterministically.
		for i := 1; ; i++ {
			if _, taken := out[name]; !taken {
				break
			}
			name = fmt.Sprintf("%d-%s", i, imported.SanitizeID(filepath.Base(abs)))
		}
		out[name] = data
		*total += int64(len(data))
	}
	return out
}
