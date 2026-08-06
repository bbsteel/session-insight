package bundle

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"github.com/bbsteel/session-insight/internal/model"
)

// Redaction is a best-effort heuristic over common secret shapes and local
// path conventions. It is NOT a guarantee that a redacted bundle is free of
// sensitive data: unrecognized token formats, secrets embedded in prose, and
// nonstandard env names all pass through untouched. Review before sharing.

const redactedPlaceholder = "<redacted>"

// envSecret matches KEY=value / KEY: value shapes for well-known sensitive
// variable names; the value (6+ non-space chars) is replaced.
var envSecret = regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password|authorization)\s*[:=]\s*["']?\S{6,}`)

// knownToken matches token formats whose shape alone identifies them as
// credentials, regardless of any surrounding key name.
var knownToken = regexp.MustCompile(
	`sk-[A-Za-z0-9_-]{10,}` +
		`|ghp_[A-Za-z0-9]{20,}` +
		`|github_pat_[A-Za-z0-9_]{20,}` +
		`|AKIA[0-9A-Z]{16}` +
		`|xox[baprs]-[A-Za-z0-9-]{10,}` +
		`|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)

// homePath matches a leading /home/<user> or /Users/<user> path segment.
var homePath = regexp.MustCompile(`/(home|Users)/[^/\s:"']+`)

// RedactSession rewrites d and evs in place, replacing env-style secrets,
// known token formats, and home-directory paths. It must only be called on
// deep copies (the values are mutated). Returns the number of replacements.
func RedactSession(d *model.SessionDetail, evs []model.RenderEvent, homeDir string) (redactions int) {
	rc := &redactor{homeDir: homeDir}
	if d != nil {
		rc.walkValue(reflect.ValueOf(d))
	}
	for i := range evs {
		rc.walkValue(reflect.ValueOf(&evs[i]))
	}
	return rc.count
}

// DeepCopyDetail returns an independent copy of d (JSON round trip), for
// callers that must redact without mutating their live session data.
// On marshal/unmarshal failure it returns an empty shell — never the original
// pointer — so a subsequent RedactSession cannot mutate live reader state.
func DeepCopyDetail(d *model.SessionDetail) *model.SessionDetail {
	if d == nil {
		return nil
	}
	data, err := json.Marshal(d)
	if err != nil {
		return &model.SessionDetail{}
	}
	var out model.SessionDetail
	if err := json.Unmarshal(data, &out); err != nil {
		return &model.SessionDetail{}
	}
	return &out
}

// DeepCopyEvents returns an independent copy of evs (JSON round trip).
// On failure it returns an empty slice, never the caller's backing array.
func DeepCopyEvents(evs []model.RenderEvent) []model.RenderEvent {
	if evs == nil {
		return nil
	}
	data, err := json.Marshal(evs)
	if err != nil {
		return []model.RenderEvent{}
	}
	var out []model.RenderEvent
	if err := json.Unmarshal(data, &out); err != nil {
		return []model.RenderEvent{}
	}
	return out
}

type redactor struct {
	homeDir string
	count   int
}

// walkValue visits every settable string in v (structs, pointers, slices,
// maps) and applies the redaction rules.
func (rc *redactor) walkValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		e := v.Elem()
		// Concrete values inside an interface are never settable; replace
		// the interface itself with the redacted string copy.
		if e.Kind() == reflect.String {
			if redacted := rc.redactString(e.String()); redacted != e.String() && v.CanSet() {
				v.Set(reflect.ValueOf(redacted))
			}
			return
		}
		rc.walkValue(e)
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		rc.walkValue(v.Elem())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if f.CanSet() {
				rc.walkValue(f)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			rc.walkValue(v.Index(i))
		}
	case reflect.Map:
		// Map values are not addressable; rewrite via a copy when needed.
		iter := v.MapRange()
		for iter.Next() {
			val := iter.Value()
			newVal := reflect.New(val.Type()).Elem()
			newVal.Set(val)
			rc.walkValue(newVal)
			if !reflect.DeepEqual(val.Interface(), newVal.Interface()) {
				v.SetMapIndex(iter.Key(), newVal)
			}
		}
	case reflect.String:
		if redacted := rc.redactString(v.String()); redacted != v.String() && v.CanSet() {
			v.SetString(redacted)
		}
	}
}

func (rc *redactor) redactString(s string) string {
	if s == "" {
		return s
	}
	out := s
	out = envSecret.ReplaceAllStringFunc(out, func(m string) string {
		rc.count++
		return redactedPlaceholder
	})
	out = knownToken.ReplaceAllStringFunc(out, func(m string) string {
		rc.count++
		return redactedPlaceholder
	})
	if rc.homeDir != "" && strings.Contains(out, rc.homeDir) {
		n := strings.Count(out, rc.homeDir)
		out = strings.ReplaceAll(out, rc.homeDir, "~")
		rc.count += n
	}
	out = homePath.ReplaceAllStringFunc(out, func(m string) string {
		rc.count++
		return "~"
	})
	return out
}
