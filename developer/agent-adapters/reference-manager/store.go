package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Store layout (always outside the Git repository):
//
//	<root>/<agent>/catalog.json
//	<root>/<agent>/blobs/<sha256><ext>     content-addressed, old content kept
//	<root>/<agent>/<logical-name><ext>     optional drop-in fast path, imported
//	                                       into blobs on scan and removed here

const (
	// StoreRootEnv overrides the reference store root for controlled
	// development environments, other disks and platforms.
	StoreRootEnv = "SI_TERMINAL_REFERENCE_ROOT"

	maxImageBytes = 30 << 20 // 30 MiB decoded-source cap
)

var (
	agentIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	hashPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// defaultStoreRoot resolves the reference store root.
func defaultStoreRoot() (string, error) {
	if override := os.Getenv(StoreRootEnv); override != "" {
		return filepath.Abs(override)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".session-insight-dev", "terminal-references"), nil
}

func checkoutFallbackStore(checkoutDir string) string {
	return filepath.Join(checkoutDir, ".runtime", "terminal-references")
}

// ensureStoreRoot creates and probes preferred. If that fails and allowFallback
// is set, it creates and probes fallback instead and returns a warning note.
func ensureStoreRoot(preferred, fallback string, allowFallback bool) (root, note string, err error) {
	if preferred == "" {
		return "", "", fmt.Errorf("empty store root")
	}
	preferredErr := ensureWritableStoreRoot(preferred)
	if preferredErr == nil {
		return preferred, "", nil
	}
	if !allowFallback || fallback == "" || fallback == preferred {
		return "", "", fmt.Errorf("create store %s: %w", preferred, preferredErr)
	}
	fallbackErr := ensureWritableStoreRoot(fallback)
	if fallbackErr != nil {
		return "", "", fmt.Errorf("create store %s: %v (fallback %s: %w)", preferred, preferredErr, fallback, fallbackErr)
	}
	return fallback, fmt.Sprintf("default store %s is not writable (%v); using %s", preferred, preferredErr, fallback), nil
}

func ensureWritableStoreRoot(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	probe, err := os.CreateTemp(root, ".writability-probe-*")
	if err != nil {
		return err
	}
	probePath := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(probePath)
		return err
	}
	if err := os.Remove(probePath); err != nil {
		return err
	}
	return nil
}

// Store owns the on-disk reference data of every Agent.
//
// Every caller-supplied identifier (agent, logical file name, content hash)
// is mapped back to a server-side canonical value before it touches a
// filesystem path: request text itself is never joined into a path.
type Store struct {
	Root     string
	catalogs *catalogStore
	// resolveAgent maps an agent ID to the registry's own canonical value.
	// Injected so tests can run without the real reader registry.
	resolveAgent func(string) (string, bool)
	// generateMu serializes work-order freeze so two clicks cannot both
	// observe "no active duplicate" and write two directories.
	generateMu sync.Mutex
}

func newStore(root string, resolveAgent func(string) (string, bool)) *Store {
	return &Store{Root: root, catalogs: &catalogStore{root: root}, resolveAgent: resolveAgent}
}

func (s *Store) agentDir(agent string) string { return filepath.Join(s.Root, agent) }
func (s *Store) blobDir(agent string) string  { return filepath.Join(s.agentDir(agent), "blobs") }

func (s *Store) blobPath(agent, hash, ext string) string {
	return filepath.Join(s.blobDir(agent), hash+ext)
}

// canonicalAgent validates a caller-supplied agent ID and returns the
// registry's own string for it, so downstream paths are built from trusted
// data rather than from the request.
func (s *Store) canonicalAgent(agent string) (string, error) {
	if !agentIDPattern.MatchString(agent) {
		return "", fmt.Errorf("invalid agent id %q", agent)
	}
	canonical, ok := s.resolveAgent(agent)
	if !ok {
		return "", fmt.Errorf("unknown agent %q", agent)
	}
	return canonical, nil
}

// LoadCatalog reads the catalog of a caller-named Agent.
func (s *Store) LoadCatalog(agent string) (*AgentCatalog, error) {
	canonical, err := s.canonicalAgent(agent)
	if err != nil {
		return nil, err
	}
	return s.catalogs.load(canonical)
}

// UpdateCatalog applies fn to the catalog of a caller-named Agent.
func (s *Store) UpdateCatalog(agent string, fn func(*AgentCatalog) error) error {
	canonical, err := s.canonicalAgent(agent)
	if err != nil {
		return err
	}
	return s.catalogs.update(canonical, fn)
}

// supportedExt maps a decoded image format to its canonical extension.
func supportedExt(format string) (string, bool) {
	switch format {
	case "png":
		return ".png", true
	case "jpeg":
		return ".jpg", true
	case "gif":
		return ".gif", true
	}
	return "", false
}

// decodeImage validates that data is a decodable image of a supported format
// and returns its canonical extension.
func decodeImage(data []byte) (string, error) {
	_, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("not a decodable PNG/JPEG/GIF image: %w", err)
	}
	ext, ok := supportedExt(format)
	if !ok {
		return "", fmt.Errorf("unsupported image format %q (supported: png, jpeg, gif)", format)
	}
	return ext, nil
}

// Import validates and stores one screenshot for a logical file, then updates
// the catalog. Old content is never overwritten: blobs are content-addressed
// and an accepted capture stays available after a newer image arrives.
func (s *Store) Import(agent, logicalName string, r io.Reader, originalName string) (*CaptureRecord, error) {
	canonicalAgent, err := s.canonicalAgent(agent)
	if err != nil {
		return nil, err
	}
	canonicalName, ok := canonicalLogicalName(logicalName)
	if !ok {
		return nil, fmt.Errorf("unknown logical file name %q", logicalName)
	}
	data, err := io.ReadAll(io.LimitReader(r, maxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty file")
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("image exceeds the %d MiB limit", maxImageBytes>>20)
	}
	ext, err := decodeImage(data)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	cat, err := s.catalogs.load(canonicalAgent)
	if err != nil {
		return nil, err
	}
	// The same image must not be renamed to impersonate a different state.
	for other, st := range cat.Items {
		if other != canonicalName && st.Current != nil && st.Current.Hash == hash {
			return nil, fmt.Errorf("identical image already assigned to %s; one image cannot stand in for two states", other)
		}
		if other != canonicalName && st.LegacyAcceptedHash != "" && st.LegacyAcceptedHash == hash {
			return nil, fmt.Errorf("identical image is retained content of %s; one image cannot stand in for two states", other)
		}
	}
	if st := cat.Items[canonicalName]; st != nil && st.Current != nil && st.Current.Hash == hash {
		return st.Current, nil // unchanged content: no state transition
	}

	if err := os.MkdirAll(s.blobDir(canonicalAgent), 0o755); err != nil {
		return nil, err
	}
	blob := s.blobPath(canonicalAgent, hash, ext)
	if _, statErr := os.Stat(blob); statErr != nil {
		if !os.IsNotExist(statErr) {
			return nil, fmt.Errorf("inspect blob: %w", statErr)
		}
		if err := os.WriteFile(blob, data, 0o600); err != nil {
			return nil, err
		}
	}
	rec := &CaptureRecord{
		Hash:         hash,
		Ext:          ext,
		OriginalName: filepath.Base(originalName),
		ImportedAt:   nowRFC3339(),
	}
	err = s.catalogs.update(canonicalAgent, func(c *AgentCatalog) error {
		c.item(canonicalName).Current = rec
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// ScanDropIns imports canonically named images placed directly in the Agent
// directory (the manual fast path) and removes the drop-in file afterwards.
// Unknown files are left untouched and reported.
func (s *Store) ScanDropIns(agent string) (imported []string, skipped []string, err error) {
	canonicalAgent, err := s.canonicalAgent(agent)
	if err != nil {
		return nil, nil, err
	}
	dir := s.agentDir(canonicalAgent)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "catalog.json" {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		base := strings.TrimSuffix(name, ext)
		canonicalName, ok := canonicalLogicalName(base)
		if !ok {
			skipped = append(skipped, name)
			continue
		}
		path := filepath.Join(dir, name)
		f, err := os.Open(path)
		if err != nil {
			skipped = append(skipped, name)
			continue
		}
		_, importErr := s.Import(canonicalAgent, canonicalName, f, name)
		f.Close() //nolint:errcheck
		if importErr != nil {
			skipped = append(skipped, fmt.Sprintf("%s (%v)", name, importErr))
			continue
		}
		if err := os.Remove(path); err != nil {
			return imported, skipped, fmt.Errorf("remove imported drop-in %s: %w", name, err)
		}
		imported = append(imported, canonicalName)
	}
	sort.Strings(imported)
	sort.Strings(skipped)
	return imported, skipped, nil
}

// blobExists reports whether the current capture's content is on disk. The
// record comes from the catalog, so its hash/ext are store-owned values.
func (s *Store) blobExists(agent string, rec *CaptureRecord) bool {
	canonical, err := s.canonicalAgent(agent)
	if err != nil {
		return false
	}
	if rec == nil || !hashPattern.MatchString(rec.Hash) {
		return false
	}
	_, err = os.Stat(s.blobPath(canonical, rec.Hash, rec.Ext))
	return err == nil
}

// servableHashes returns every content hash the catalog may legitimately
// serve: current captures, accepted content and work-order frozen inputs.
func servableHashes(cat *AgentCatalog) map[string]bool {
	out := map[string]bool{}
	for _, st := range cat.Items {
		if st.Current != nil {
			out[st.Current.Hash] = true
		}
		if st.LegacyAcceptedHash != "" {
			out[st.LegacyAcceptedHash] = true
		}
	}
	for _, wo := range cat.WorkOrders {
		for _, frozenHash := range wo.Frozen {
			out[frozenHash] = true
		}
	}
	return out
}

// lookupBlob resolves a hash to an on-disk blob only when the catalog knows
// the hash. The served path is built from catalog-owned and on-disk values,
// not from the request. The store directory is never exposed as static
// content.
func (s *Store) lookupBlob(agent, hash string, extraHashes ...string) (path string, ext string, err error) {
	canonicalAgent, err := s.canonicalAgent(agent)
	if err != nil {
		return "", "", err
	}
	if !hashPattern.MatchString(hash) {
		return "", "", fmt.Errorf("invalid hash")
	}
	cat, err := s.catalogs.load(canonicalAgent)
	if err != nil {
		return "", "", err
	}
	known := servableHashes(cat)
	for _, extra := range extraHashes {
		if extra != "" {
			known[extra] = true
		}
	}
	if !known[hash] {
		return "", "", fmt.Errorf("unknown image")
	}
	knownHash := hash
	// The on-disk blob name carries the authoritative extension (a frozen
	// input's capture record may no longer be current or accepted).
	matches, err := filepath.Glob(s.blobPath(canonicalAgent, knownHash, ".*"))
	if err != nil {
		return "", "", fmt.Errorf("inspect blob: %w", err)
	}
	for _, match := range matches {
		switch filepath.Ext(match) {
		case ".png", ".jpg", ".gif":
			return match, filepath.Ext(match), nil
		}
	}
	return "", "", fmt.Errorf("image content not available locally")
}
