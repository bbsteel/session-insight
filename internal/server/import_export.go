package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/bundle"
	"github.com/bbsteel/session-insight/internal/packops"
	"github.com/bbsteel/session-insight/internal/reader/imported"
)

// maxImportUpload bounds the (still compressed) multipart upload.
const maxImportUpload = 512 << 20 // 512 MiB

// SetImportRoot wires the directory imported bundles are extracted into
// (call before Serve).
func (s *Server) SetImportRoot(root string) {
	s.importRoot = root
}

// SetIndexKicker wires the indexer's per-agent kick so imports/exports can
// trigger a re-index of the imported reader without a server ↔ indexer
// package dependency (call before Serve).
func (s *Server) SetIndexKicker(fn func(agentType string)) {
	s.kickIndex = fn
}

func (s *Server) kickImportedIndex() {
	if s.kickIndex != nil {
		s.kickIndex(imported.AgentType)
	}
}

// handleExportBundle packs selected sessions into a .sibundle (gzipped tar)
// for migration to another SessionInsight instance.
func (s *Server) handleExportBundle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Sessions []struct {
			AgentType string `json:"agent_type"`
			ID        string `json:"id"`
		} `json:"sessions"`
		IncludeRaw bool   `json:"include_raw"`
		Redact     bool   `json:"redact"`
		CaseLabel  string `json:"case_label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Sessions) == 0 {
		http.Error(w, "no sessions requested", http.StatusBadRequest)
		return
	}

	sels := make([]packops.Selection, 0, len(req.Sessions))
	for _, sel := range req.Sessions {
		sels = append(sels, packops.Selection{AgentType: sel.AgentType, ID: sel.ID})
	}
	res, err := packops.BuildExport(s.Readers, s.DB, sels, packops.ExportOptions{
		IncludeRaw: req.IncludeRaw,
		Redact:     req.Redact,
		CaseLabel:  req.CaseLabel,
		SIVersion:  s.Version,
	})
	if err != nil {
		log.Printf("POST /api/sessions/export-bundle: %v", err)
		http.Error(w, "export failed", http.StatusInternalServerError)
		return
	}
	if len(res.Payloads) == 0 {
		http.Error(w, "none of the requested sessions could be read", http.StatusNotFound)
		return
	}

	filename := fmt.Sprintf("si-bundle-%s.sibundle", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if err := bundle.WriteBundle(w, res.Manifest, res.Payloads); err != nil {
		log.Printf("POST /api/sessions/export-bundle: %v", err)
	}
}

// handleImportBundle accepts a .sibundle upload, extracts it under the
// import root, and records per-session import provenance. The imported
// reader picks the sessions up on the next index pass (kicked here).
func (s *Server) handleImportBundle(w http.ResponseWriter, r *http.Request) {
	if s.importRoot == "" {
		http.Error(w, "import root not configured", http.StatusServiceUnavailable)
		return
	}
	if s.DB == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxImportUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "invalid multipart form (or upload exceeds 512 MiB)", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing form file field \"file\"", http.StatusBadRequest)
		return
	}
	defer file.Close()

	bundleID, manifest, err := packops.ImportBundle(s.DB, s.importRoot, file)
	if err != nil {
		switch {
		case errors.Is(err, bundle.ErrUnsupportedVersion):
			http.Error(w, err.Error(), http.StatusBadRequest)
		case errors.Is(err, bundle.ErrInvalidBundle):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			log.Printf("POST /api/sessions/import-bundle: %v", err)
			http.Error(w, "import failed", http.StatusInternalServerError)
		}
		return
	}

	type importedSession struct {
		ID                string `json:"id"`
		OriginalID        string `json:"original_id"`
		OriginalAgentType string `json:"original_agent_type"`
		Title             string `json:"title"`
	}
	sessions := make([]importedSession, 0, len(manifest.Sessions))
	for _, se := range manifest.Sessions {
		sessions = append(sessions, importedSession{
			ID:                imported.JoinSessionID(bundleID, se.ID),
			OriginalID:        se.ID,
			OriginalAgentType: se.AgentType,
			Title:             se.Title,
		})
	}

	s.kickImportedIndex()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"bundle_id":   bundleID,
		"imported":    len(sessions),
		"case_label":  manifest.CaseLabel,
		"origin_host": manifest.OriginHost,
		"sessions":    sessions,
	})
}

// handleListImportBundles returns one aggregate row per imported bundle.
func (s *Server) handleListImportBundles(w http.ResponseWriter, r *http.Request) {
	if s.DB == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	bundles, err := s.DB.ListImportBundles()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(bundles)
}

// handleDeleteImportBundle removes a whole imported bundle: import records,
// index rows, and the on-disk bundle directory. It never touches anything
// outside the import root.
func (s *Server) handleDeleteImportBundle(w http.ResponseWriter, r *http.Request) {
	if s.importRoot == "" {
		http.Error(w, "import root not configured", http.StatusServiceUnavailable)
		return
	}
	if s.DB == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	bundleID := r.PathValue("bundle")
	// Path traversal guard: only direct child directories of the import
	// root may be removed.
	if bundleID == "" || bundleID == "." || bundleID == ".." ||
		filepath.Base(bundleID) != bundleID || strings.ContainsAny(bundleID, `/\`) {
		http.Error(w, "invalid bundle id", http.StatusBadRequest)
		return
	}

	ids, err := s.DB.DeleteImportRecordsByBundle(bundleID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, id := range ids {
		if err := s.DB.DeleteSessionData(imported.AgentType, id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := os.RemoveAll(filepath.Join(s.importRoot, bundleID)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.kickImportedIndex()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"deleted":  bundleID,
		"sessions": len(ids),
	})
}
