package bundle

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// SessionPayload carries one session's normalized replay data plus the raw
// source files captured at export time (RawFiles maps raw file name to
// content; names must be plain base names).
type SessionPayload struct {
	Entry        SessionEntry
	Detail       *model.SessionDetail
	RenderEvents []model.RenderEvent
	RawFiles     map[string][]byte
}

// sessionFile is the on-disk shape of sessions/<file> inside the bundle.
type sessionFile struct {
	Detail       *model.SessionDetail `json:"detail"`
	RenderEvents []model.RenderEvent  `json:"render_events"`
}

// WriteBundle streams the bundle as a gzipped tar to w: manifest.json, one
// sessions/<file> JSON document per payload, and raw/<raw_dir>/<name>
// entries for attached source files.
func WriteBundle(w io.Writer, m Manifest, payloads []SessionPayload) error {
	if err := Validate(&m); err != nil {
		return err
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)

	manifestJSON, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := writeTarEntry(tw, "manifest.json", manifestJSON); err != nil {
		return err
	}

	for _, p := range payloads {
		body, err := json.Marshal(sessionFile{Detail: p.Detail, RenderEvents: p.RenderEvents})
		if err != nil {
			return fmt.Errorf("encode session %q: %w", p.Entry.ID, err)
		}
		if err := writeTarEntry(tw, "sessions/"+p.Entry.File, body); err != nil {
			return err
		}
		if p.Entry.RawDir == "" {
			continue
		}
		names := make([]string, 0, len(p.RawFiles))
		for name := range p.RawFiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := writeTarEntry(tw, "raw/"+p.Entry.RawDir+"/"+name, p.RawFiles[name]); err != nil {
				return err
			}
		}
	}

	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func writeTarEntry(tw *tar.Writer, name string, body []byte) error {
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(body)),
		ModTime: time.Now().UTC(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar header %s: %w", name, err)
	}
	if _, err := tw.Write(body); err != nil {
		return fmt.Errorf("tar write %s: %w", name, err)
	}
	return nil
}
