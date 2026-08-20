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

// Store owns the on-disk reference data of every Agent.
type Store struct {
	Root     string
	catalogs *catalogStore
	// validAgent reports whether an agent ID may have a catalog. Injected so
	// tests can run without the real reader registry.
	validAgent func(string) bool
}

func newStore(root string, validAgent func(string) bool) *Store {
	return &Store{Root: root, catalogs: &catalogStore{root: root}, validAgent: validAgent}
}

func (s *Store) agentDir(agent string) string { return filepath.Join(s.Root, agent) }
func (s *Store) blobDir(agent string) string  { return filepath.Join(s.agentDir(agent), "blobs") }

func (s *Store) blobPath(agent, hash, ext string) string {
	return filepath.Join(s.blobDir(agent), hash+ext)
}

func (s *Store) checkAgent(agent string) error {
	if !agentIDPattern.MatchString(agent) {
		return fmt.Errorf("invalid agent id %q", agent)
	}
	if s.validAgent != nil && !s.validAgent(agent) {
		return fmt.Errorf("unknown agent %q", agent)
	}
	return nil
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
	if err := s.checkAgent(agent); err != nil {
		return nil, err
	}
	if !knownLogicalNames[logicalName] {
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

	cat, err := s.catalogs.load(agent)
	if err != nil {
		return nil, err
	}
	// The same image must not be renamed to impersonate a different state.
	for other, st := range cat.Items {
		if other != logicalName && st.Current != nil && st.Current.Hash == hash {
			return nil, fmt.Errorf("identical image already assigned to %s; one image cannot stand in for two states", other)
		}
		if other != logicalName && st.AcceptedHash != "" && st.AcceptedHash == hash {
			return nil, fmt.Errorf("identical image is the accepted content of %s; one image cannot stand in for two states", other)
		}
	}
	if st := cat.Items[logicalName]; st != nil && st.Current != nil && st.Current.Hash == hash {
		return st.Current, nil // unchanged content: no state transition
	}

	if err := os.MkdirAll(s.blobDir(agent), 0o755); err != nil {
		return nil, err
	}
	blob := s.blobPath(agent, hash, ext)
	if _, err := os.Stat(blob); os.IsNotExist(err) {
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
	err = s.catalogs.update(agent, func(c *AgentCatalog) error {
		c.item(logicalName).Current = rec
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
	if err := s.checkAgent(agent); err != nil {
		return nil, nil, err
	}
	dir := s.agentDir(agent)
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
		if !knownLogicalNames[base] {
			skipped = append(skipped, name)
			continue
		}
		path := filepath.Join(dir, name)
		f, err := os.Open(path)
		if err != nil {
			skipped = append(skipped, name)
			continue
		}
		_, importErr := s.Import(agent, base, f, name)
		f.Close() //nolint:errcheck
		if importErr != nil {
			skipped = append(skipped, fmt.Sprintf("%s (%v)", name, importErr))
			continue
		}
		if err := os.Remove(path); err != nil {
			return imported, skipped, fmt.Errorf("remove imported drop-in %s: %w", name, err)
		}
		imported = append(imported, base)
	}
	sort.Strings(imported)
	sort.Strings(skipped)
	return imported, skipped, nil
}

// blobExists reports whether the current capture's content is on disk.
func (s *Store) blobExists(agent string, rec *CaptureRecord) bool {
	if rec == nil || !hashPattern.MatchString(rec.Hash) {
		return false
	}
	_, err := os.Stat(s.blobPath(agent, rec.Hash, rec.Ext))
	return err == nil
}

// knownHashes returns every content hash the catalog may legitimately serve:
// current captures, accepted content and work-order frozen inputs.
func knownHashes(cat *AgentCatalog) map[string]string {
	out := map[string]string{} // hash -> ext
	for _, st := range cat.Items {
		if st.Current != nil {
			out[st.Current.Hash] = st.Current.Ext
		}
		if st.AcceptedHash != "" && st.AcceptedExt != "" {
			out[st.AcceptedHash] = st.AcceptedExt
		}
	}
	return out
}

// lookupBlob resolves a hash to an on-disk blob only when the catalog knows
// the hash. The store directory is never exposed as static content.
func (s *Store) lookupBlob(agent, hash string) (path string, ext string, err error) {
	if err := s.checkAgent(agent); err != nil {
		return "", "", err
	}
	if !hashPattern.MatchString(hash) {
		return "", "", fmt.Errorf("invalid hash")
	}
	cat, err := s.catalogs.load(agent)
	if err != nil {
		return "", "", err
	}
	ext, ok := knownHashes(cat)[hash]
	if !ok {
		return "", "", fmt.Errorf("unknown image")
	}
	path = s.blobPath(agent, hash, ext)
	if _, err := os.Stat(path); err != nil {
		return "", "", fmt.Errorf("image content not available locally")
	}
	return path, ext, nil
}
