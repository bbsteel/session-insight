// Package provenance provides shared pure helpers for session record
// completeness: enum validation, warning aggregation, overall state, and
// source normalization. Adapters report facts; this package owns priority.
package provenance

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// Input is the fact set one adapter read produces for a snapshot.
type Input struct {
	// StateOverride, when non-empty, forces the overall state (e.g. source_missing
	// tombstones or parser_unsupported) instead of deriving from body/warnings.
	StateOverride model.RecordCompletenessState
	ReasonCode    string
	CapturedAt    time.Time
	AdapterRevision int
	Sources       []model.SessionSourceFile
	Warnings      []model.ParseWarning
	// HasReplayableBody is true when the detail includes usable turn content.
	HasReplayableBody bool
	LastSuccessfulAt  *time.Time
	MissingSince      *time.Time
}

// Build normalizes sources/warnings and computes overall state + summary.
func Build(in Input) model.SessionProvenance {
	captured := in.CapturedAt
	if captured.IsZero() {
		captured = time.Now().UTC()
	} else {
		captured = captured.UTC()
	}

	sources := NormalizeSources(in.Sources)
	warnings := AggregateWarnings(in.Warnings)
	summary := SummarizeWarnings(warnings)

	state := in.StateOverride
	if state == "" {
		state = DeriveState(in.HasReplayableBody, warnings, sources)
	}
	reason := in.ReasonCode
	if reason == "" {
		reason = defaultReason(state, warnings)
	}

	return model.SessionProvenance{
		State:            state,
		ReasonCode:       reason,
		CapturedAt:       captured,
		SourceUpdatedAt:  LatestSourceUpdatedAt(sources),
		AdapterRevision:  in.AdapterRevision,
		Sources:          sources,
		WarningSummary:   summary,
		Warnings:         warnings,
		LastSuccessfulAt: cloneTime(in.LastSuccessfulAt),
		MissingSince:     cloneTime(in.MissingSince),
	}
}

// DeriveState applies the design priority when no override is set:
// source_missing / parser_unsupported come from StateOverride;
// then metadata_only → degraded → complete.
func DeriveState(hasBody bool, warnings []model.ParseWarning, sources []model.SessionSourceFile) model.RecordCompletenessState {
	if !hasBody {
		// Primary present but unreadable/unsupported without body → still metadata_only
		// unless caller overrode to parser_unsupported / source_missing.
		return model.RecordMetadataOnly
	}
	for _, w := range warnings {
		if w.AffectsCompleteness {
			return model.RecordDegraded
		}
	}
	// Primary missing with body is unusual (historical); treat as degraded.
	for _, s := range sources {
		if s.Role == model.SourceRolePrimaryTranscript && s.State != model.SourcePresent {
			return model.RecordDegraded
		}
	}
	return model.RecordComplete
}

func defaultReason(state model.RecordCompletenessState, warnings []model.ParseWarning) string {
	switch state {
	case model.RecordComplete:
		return ""
	case model.RecordDegraded:
		for _, w := range warnings {
			if w.AffectsCompleteness {
				return w.Code
			}
		}
		return "partial_parse"
	case model.RecordMetadataOnly:
		return "no_body"
	case model.RecordSourceMissing:
		return "source_missing"
	case model.RecordParserUnsupported:
		return model.WarnUnsupportedSchema
	default:
		return ""
	}
}

// NormalizeSources dedupes by path (first role wins after stable sort by role+path),
// drops empty paths, and sorts for stable API output.
func NormalizeSources(in []model.SessionSourceFile) []model.SessionSourceFile {
	if len(in) == 0 {
		return []model.SessionSourceFile{}
	}
	type key struct{ path, role string }
	seen := make(map[key]struct{}, len(in))
	out := make([]model.SessionSourceFile, 0, len(in))
	for _, s := range in {
		path := strings.TrimSpace(s.Path)
		if path == "" {
			continue
		}
		role := s.Role
		if role == "" {
			role = model.SourceRoleOther
		}
		k := key{path: path, role: role}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		s.Path = path
		s.Role = role
		if s.State == "" {
			s.State = model.SourcePresent
		}
		if s.UpdatedAt != nil {
			t := s.UpdatedAt.UTC()
			s.UpdatedAt = &t
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Role != out[j].Role {
			return out[i].Role < out[j].Role
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// AggregateWarnings merges identical code+role+impacts facts by summing Count.
func AggregateWarnings(in []model.ParseWarning) []model.ParseWarning {
	if len(in) == 0 {
		return []model.ParseWarning{}
	}
	type key struct {
		code, role, severity string
		affects              bool
		impacts              string
	}
	order := make([]key, 0, len(in))
	merged := make(map[key]*model.ParseWarning, len(in))
	for _, w := range in {
		if w.Code == "" {
			continue
		}
		if w.Count <= 0 {
			w.Count = 1
		}
		impacts := normalizeImpacts(w.Impacts)
		k := key{
			code:     w.Code,
			role:     w.SourceRole,
			severity: w.Severity,
			affects:  w.AffectsCompleteness,
			impacts:  strings.Join(impacts, ","),
		}
		if existing, ok := merged[k]; ok {
			existing.Count += w.Count
			if existing.FirstRecord == nil && w.FirstRecord != nil {
				existing.FirstRecord = w.FirstRecord
			} else if existing.FirstRecord != nil && w.FirstRecord != nil && *w.FirstRecord < *existing.FirstRecord {
				existing.FirstRecord = w.FirstRecord
			}
			continue
		}
		cp := w
		cp.Impacts = impacts
		merged[k] = &cp
		order = append(order, k)
	}
	out := make([]model.ParseWarning, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		if out[i].SourceRole != out[j].SourceRole {
			return out[i].SourceRole < out[j].SourceRole
		}
		return out[i].Severity < out[j].Severity
	})
	return out
}

func normalizeImpacts(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.TrimSpace(raw)
		if v == "" || !IsKnownImpact(v) {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// SummarizeWarnings rolls up severity and impact counts.
func SummarizeWarnings(warnings []model.ParseWarning) model.WarningSummary {
	var s model.WarningSummary
	if len(warnings) == 0 {
		return s
	}
	impacts := make(map[string]int)
	for _, w := range warnings {
		n := w.Count
		if n <= 0 {
			n = 1
		}
		s.Total += n
		switch w.Severity {
		case model.WarningSeverityInfo:
			s.Info += n
		case model.WarningSeverityError:
			s.Error += n
		default:
			s.Warning += n
		}
		for _, imp := range w.Impacts {
			impacts[imp] += n
		}
	}
	if len(impacts) > 0 {
		s.ImpactCounts = impacts
	}
	return s
}

// LatestSourceUpdatedAt returns the newest mtime among primary sources, else any.
func LatestSourceUpdatedAt(sources []model.SessionSourceFile) *time.Time {
	var best *time.Time
	consider := func(s model.SessionSourceFile) {
		if s.UpdatedAt == nil || s.UpdatedAt.IsZero() {
			return
		}
		if best == nil || s.UpdatedAt.After(*best) {
			t := s.UpdatedAt.UTC()
			best = &t
		}
	}
	for _, s := range sources {
		if s.Role == model.SourceRolePrimaryTranscript {
			consider(s)
		}
	}
	if best != nil {
		return best
	}
	for _, s := range sources {
		consider(s)
	}
	return best
}

// StatSource builds a SessionSourceFile from an on-disk path.
func StatSource(role, path string) model.SessionSourceFile {
	sf := model.SessionSourceFile{
		Role:  role,
		Path:  path,
		State: model.SourceMissing,
	}
	if path == "" {
		return sf
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			sf.State = model.SourceMissing
		} else {
			sf.State = model.SourceUnreadable
		}
		return sf
	}
	sf.State = model.SourcePresent
	mt := info.ModTime().UTC()
	sf.UpdatedAt = &mt
	sz := info.Size()
	sf.SizeBytes = &sz
	return sf
}

// PresentSource is a present source with optional known mtime/size.
func PresentSource(role, path string, updatedAt time.Time, size int64) model.SessionSourceFile {
	sf := model.SessionSourceFile{
		Role:  role,
		Path:  path,
		State: model.SourcePresent,
	}
	if !updatedAt.IsZero() {
		t := updatedAt.UTC()
		sf.UpdatedAt = &t
	}
	if size >= 0 {
		sz := size
		sf.SizeBytes = &sz
	}
	return sf
}

// Warning constructs a single ParseWarning (count defaults to 1).
func Warning(code, severity string, affects bool, impacts []string, sourceRole string, firstRecord *int64, count int) model.ParseWarning {
	if count <= 0 {
		count = 1
	}
	if severity == "" {
		severity = model.WarningSeverityWarning
	}
	return model.ParseWarning{
		Code:                code,
		Severity:            severity,
		AffectsCompleteness: affects,
		Impacts:             normalizeImpacts(impacts),
		Count:               count,
		SourceRole:          sourceRole,
		FirstRecord:         firstRecord,
	}
}

// AttachComplete builds complete provenance for a single present primary path.
func AttachComplete(adapterRevision int, primaryPath string, capturedAt time.Time) model.SessionProvenance {
	return Build(Input{
		CapturedAt:        capturedAt,
		AdapterRevision:   adapterRevision,
		Sources:           []model.SessionSourceFile{StatSource(model.SourceRolePrimaryTranscript, primaryPath)},
		HasReplayableBody: true,
	})
}

// AttachWithWarnings builds provenance from a primary path plus warnings.
func AttachWithWarnings(adapterRevision int, primaryPath string, hasBody bool, warnings []model.ParseWarning, capturedAt time.Time) model.SessionProvenance {
	return Build(Input{
		CapturedAt:        capturedAt,
		AdapterRevision:   adapterRevision,
		Sources:           []model.SessionSourceFile{StatSource(model.SourceRolePrimaryTranscript, primaryPath)},
		Warnings:          warnings,
		HasReplayableBody: hasBody,
	})
}

// CleanPath returns the cleaned absolute-looking path for display (no resolve).
func CleanPath(p string) string {
	return filepath.Clean(p)
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	c := t.UTC()
	return &c
}
