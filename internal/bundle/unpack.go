package bundle

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxExtractedBytes caps the total decompressed payload of a bundle so a
// malicious archive cannot exhaust disk (zip-bomb guard).
const maxExtractedBytes = 2 << 30 // 2 GiB

// Extract decompresses a bundle from r into a fresh directory
// destRoot/<bundleID>/ and returns the generated bundle ID plus the
// validated manifest. Extraction happens in a temp directory under destRoot
// and is atomically renamed into place, so a failed import never leaves a
// half-written bundle visible to the imported reader.
func Extract(r io.Reader, destRoot string) (bundleID string, m *Manifest, err error) {
	if err := os.MkdirAll(destRoot, 0o755); err != nil {
		return "", nil, fmt.Errorf("create import root: %w", err)
	}
	tmp, err := os.MkdirTemp(destRoot, ".extract-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp dir: %w", err)
	}
	// On any failure the temp dir goes away; success renames it instead.
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(tmp)
		}
	}()

	if err := extractTarGz(r, tmp); err != nil {
		return "", nil, err
	}

	manifest, err := readManifest(filepath.Join(tmp, "manifest.json"))
	if err != nil {
		return "", nil, err
	}
	if err := Validate(manifest); err != nil {
		return "", nil, err
	}
	// Every declared session payload must physically exist.
	for _, se := range manifest.Sessions {
		if _, err := os.Stat(filepath.Join(tmp, "sessions", se.File)); err != nil {
			return "", nil, fmt.Errorf("%w: session file %q missing from archive", ErrInvalidBundle, se.File)
		}
	}

	bundleID = newBundleID(time.Now())
	final := filepath.Join(destRoot, bundleID)
	if err := os.Rename(tmp, final); err != nil {
		return "", nil, fmt.Errorf("commit bundle: %w", err)
	}
	committed = true
	return bundleID, manifest, nil
}

// extractTarGz writes every regular file in the archive under dest,
// rejecting absolute paths and ".." traversal and enforcing the global
// decompressed-size cap.
func extractTarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("%w: not a gzip stream: %v", ErrInvalidBundle, err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: tar read: %v", ErrInvalidBundle, err)
		}
		name := filepath.FromSlash(hdr.Name)
		if !safeRelPath(name) {
			return fmt.Errorf("%w: unsafe archive path %q", ErrInvalidBundle, hdr.Name)
		}
		target := filepath.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("extract dir %s: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > maxExtractedBytes {
				return fmt.Errorf("%w: bundle exceeds %d GiB decompressed cap", ErrInvalidBundle, maxExtractedBytes>>30)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("extract %s: %w", hdr.Name, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return fmt.Errorf("extract %s: %w", hdr.Name, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("extract %s: %w", hdr.Name, err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("extract %s: %w", hdr.Name, err)
			}
		default:
			// Symlinks, devices, etc. have no place in a session bundle.
			return fmt.Errorf("%w: unsupported tar entry type %d for %q", ErrInvalidBundle, hdr.Typeflag, hdr.Name)
		}
	}
}

// safeRelPath reports whether name is a relative path with no traversal.
func safeRelPath(name string) bool {
	if name == "" || filepath.IsAbs(name) {
		return false
	}
	clean := filepath.Clean(name)
	if clean != name {
		return false
	}
	for _, part := range strings.Split(name, string(filepath.Separator)) {
		if part == ".." {
			return false
		}
	}
	return true
}

func readManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: manifest.json unreadable: %v", ErrInvalidBundle, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w: manifest.json invalid: %v", ErrInvalidBundle, err)
	}
	return &m, nil
}

// newBundleID builds "20060102-150405-" + 6 random lowercase hex chars.
func newBundleID(t time.Time) string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is essentially unreachable; fall back to time
		// entropy rather than fail the import.
		return t.Format("20060102-150405") + "-" + fmt.Sprintf("%06x", t.UnixNano()&0xFFFFFF)
	}
	return t.Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}
