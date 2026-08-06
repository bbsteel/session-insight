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
	"path"
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
	// Every declared session payload must physically exist under sessions/.
	// Validate already rejected absolute / ".." names in se.File.
	for _, se := range manifest.Sessions {
		// Join only the fixed "sessions" segment with a single validated leaf
		// (Validate → confinedName). Refuse multi-segment leaves here so the
		// Stat path cannot carry archive-controlled separators.
		if se.File == "" || strings.ContainsAny(se.File, `/\`) || se.File == ".." || strings.Contains(se.File, "..") {
			return "", nil, fmt.Errorf("%w: session file %q escapes sessions/", ErrInvalidBundle, se.File)
		}
		sessionPath := filepath.Join(tmp, "sessions", filepath.Base(se.File))
		// Zip Slip / path-injection boundary (must stay inline for CodeQL).
		if !strings.HasPrefix(sessionPath, filepath.Clean(tmp)+string(os.PathSeparator)) {
			return "", nil, fmt.Errorf("%w: session file %q escapes extract root", ErrInvalidBundle, se.File)
		}
		if _, err := os.Stat(sessionPath); err != nil {
			return "", nil, fmt.Errorf("%w: session file %q missing from archive", ErrInvalidBundle, se.File)
		}
	}

	bundleID = newBundleID(time.Now())
	// bundleID is generated locally (timestamp + random hex); never from the archive.
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
//
// Zip Slip / path-injection: every filesystem sink is preceded in-function
// by strings.HasPrefix(path, filepath.Clean(dest)+sep). CodeQL's go/zipslip
// and go/path-injection queries only treat that pattern as a sanitizer when
// it dominates the sink in the same function (a helper return is not enough).
func extractTarGz(r io.Reader, dest string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("%w: not a gzip stream: %v", ErrInvalidBundle, err)
	}
	defer gz.Close()

	destClean := filepath.Clean(dest)
	destPrefix := destClean + string(os.PathSeparator)

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

		rel, err := normalizeArchiveRel(hdr.Name)
		if err != nil {
			return fmt.Errorf("%w: unsafe archive path %q", ErrInvalidBundle, hdr.Name)
		}

		// Build the target segment-by-segment so no full untrusted string is
		// joined in one shot; then re-check the dest prefix (CodeQL sanitizer).
		target := destClean
		for _, part := range strings.Split(rel, "/") {
			// normalizeArchiveRel already rejected empty/".."; defend in depth.
			if part == "" || part == "." || part == ".." {
				return fmt.Errorf("%w: unsafe archive path %q", ErrInvalidBundle, hdr.Name)
			}
			target = filepath.Join(target, part)
		}
		if !strings.HasPrefix(target, destPrefix) {
			return fmt.Errorf("%w: unsafe archive path %q", ErrInvalidBundle, hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if !strings.HasPrefix(target, destPrefix) {
				return fmt.Errorf("%w: unsafe archive path %q", ErrInvalidBundle, hdr.Name)
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("extract dir %s: %w", hdr.Name, err)
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > maxExtractedBytes {
				return fmt.Errorf("%w: bundle exceeds %d GiB decompressed cap", ErrInvalidBundle, maxExtractedBytes>>30)
			}
			dir := filepath.Dir(target)
			if !strings.HasPrefix(dir, destPrefix) && dir != destClean {
				return fmt.Errorf("%w: unsafe archive path %q", ErrInvalidBundle, hdr.Name)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("extract %s: %w", hdr.Name, err)
			}
			if !strings.HasPrefix(target, destPrefix) {
				return fmt.Errorf("%w: unsafe archive path %q", ErrInvalidBundle, hdr.Name)
			}
			// Fixed mode 0o644 — ignore archive-provided mode bits.
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return fmt.Errorf("extract %s: %w", hdr.Name, err)
			}
			// Bound the copy by declared size so a lying header cannot
			// stream past the remaining cap for this entry alone.
			written, err := io.Copy(f, io.LimitReader(tr, hdr.Size+1))
			if closeErr := f.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
			if err != nil {
				return fmt.Errorf("extract %s: %w", hdr.Name, err)
			}
			if written > hdr.Size {
				return fmt.Errorf("%w: entry %q larger than declared size", ErrInvalidBundle, hdr.Name)
			}
		default:
			// Symlinks, hard links, devices, etc. have no place in a session bundle.
			return fmt.Errorf("%w: unsupported tar entry type %d for %q", ErrInvalidBundle, hdr.Typeflag, hdr.Name)
		}
	}
}

// normalizeArchiveRel returns a cleaned, relative, layout-allowed path using
// forward slashes (no leading "/", no ".." segments). It does not join with
// a destination root — callers must still apply the dest-prefix check before
// any filesystem operation.
func normalizeArchiveRel(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty path")
	}
	slashName := strings.ReplaceAll(name, "\\", "/")
	if path.IsAbs(slashName) || strings.HasPrefix(slashName, "/") {
		return "", fmt.Errorf("absolute path")
	}
	slashName = strings.TrimPrefix(slashName, "./")
	cleaned := path.Clean(slashName)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("path traversal")
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == ".." {
			return "", fmt.Errorf("path traversal")
		}
	}
	// Only allow the known bundle layout prefixes.
	if cleaned != "manifest.json" &&
		!strings.HasPrefix(cleaned, "sessions/") &&
		!strings.HasPrefix(cleaned, "raw/") {
		return "", fmt.Errorf("path outside allowed layout")
	}
	return cleaned, nil
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
