package provenance

import (
	"fmt"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// ValidationError is one contract violation on a provenance snapshot.
type ValidationError struct {
	Code    string
	Message string
}

func (e ValidationError) Error() string {
	return e.Code + ": " + e.Message
}

// IsKnownState reports whether s is a defined RecordCompletenessState.
func IsKnownState(s model.RecordCompletenessState) bool {
	switch s {
	case model.RecordComplete, model.RecordDegraded, model.RecordMetadataOnly,
		model.RecordSourceMissing, model.RecordParserUnsupported:
		return true
	default:
		return false
	}
}

// IsKnownSourceState reports whether s is a defined SourceFileState.
func IsKnownSourceState(s model.SourceFileState) bool {
	switch s {
	case model.SourcePresent, model.SourceMissing, model.SourceUnreadable, model.SourceUnsupported:
		return true
	default:
		return false
	}
}

// IsKnownSeverity reports whether severity is info|warning|error.
func IsKnownSeverity(s string) bool {
	switch s {
	case model.WarningSeverityInfo, model.WarningSeverityWarning, model.WarningSeverityError:
		return true
	default:
		return false
	}
}

// IsKnownImpact reports whether impact is in the design allow-list.
func IsKnownImpact(s string) bool {
	switch s {
	case model.ImpactMetadata, model.ImpactReplay, model.ImpactNavigation,
		model.ImpactTokens, model.ImpactTools, model.ImpactDiff,
		model.ImpactCollaboration, model.ImpactRealtime:
		return true
	default:
		return false
	}
}

// IsKnownSourceRole reports whether role is a stable cross-adapter role.
func IsKnownSourceRole(role string) bool {
	switch role {
	case model.SourceRolePrimaryTranscript, model.SourceRoleMetadata,
		model.SourceRoleEvents, model.SourceRoleUpdates, model.SourceRoleToolResults,
		model.SourceRoleCollaboration, model.SourceRoleOther:
		return true
	default:
		return false
	}
}

// Validate checks design invariants on a SessionProvenance value.
// It does not require sources when state is source_missing (tombstones may
// keep last inventory) but still validates any that are present.
func Validate(p model.SessionProvenance) []ValidationError {
	var errs []ValidationError
	if !IsKnownState(p.State) {
		errs = append(errs, ValidationError{"invalid_state", fmt.Sprintf("unknown state %q", p.State)})
	}
	if p.AdapterRevision <= 0 {
		errs = append(errs, ValidationError{"invalid_adapter_revision", "adapter_revision must be > 0"})
	}
	if p.CapturedAt.IsZero() {
		errs = append(errs, ValidationError{"missing_captured_at", "captured_at is required"})
	}
	if p.MissingSince != nil && p.State != model.RecordSourceMissing {
		errs = append(errs, ValidationError{"missing_since_without_source_missing", "missing_since set but state is not source_missing"})
	}
	if p.State == model.RecordSourceMissing && p.MissingSince == nil {
		// Soft: allow empty missing_since only if never set; still warn for conformance.
		errs = append(errs, ValidationError{"source_missing_without_missing_since", "source_missing should set missing_since"})
	}

	affects := false
	for i, w := range p.Warnings {
		if w.Code == "" {
			errs = append(errs, ValidationError{"empty_warning_code", fmt.Sprintf("warnings[%d] empty code", i)})
		}
		if strings.Contains(w.Code, " ") || strings.Contains(w.Code, "/") {
			errs = append(errs, ValidationError{"raw_warning_code", fmt.Sprintf("warnings[%d] code looks raw: %q", i, w.Code)})
		}
		if !IsKnownSeverity(w.Severity) {
			errs = append(errs, ValidationError{"invalid_severity", fmt.Sprintf("warnings[%d] severity %q", i, w.Severity)})
		}
		if w.Count <= 0 {
			errs = append(errs, ValidationError{"invalid_warning_count", fmt.Sprintf("warnings[%d] count must be > 0", i)})
		}
		for _, imp := range w.Impacts {
			if !IsKnownImpact(imp) {
				errs = append(errs, ValidationError{"invalid_impact", fmt.Sprintf("warnings[%d] impact %q", i, imp)})
			}
		}
		if w.AffectsCompleteness {
			affects = true
		}
	}

	switch p.State {
	case model.RecordComplete:
		if affects {
			errs = append(errs, ValidationError{"complete_with_affects", "complete must not include affects_completeness warnings"})
		}
	case model.RecordDegraded:
		if !affects {
			errs = append(errs, ValidationError{"degraded_without_affects", "degraded requires at least one affects_completeness warning"})
		}
	}

	seenPath := make(map[string]struct{})
	for i, s := range p.Sources {
		if strings.TrimSpace(s.Path) == "" {
			errs = append(errs, ValidationError{"empty_source_path", fmt.Sprintf("sources[%d] empty path", i)})
		}
		if !IsKnownSourceRole(s.Role) {
			errs = append(errs, ValidationError{"invalid_source_role", fmt.Sprintf("sources[%d] role %q", i, s.Role)})
		}
		if !IsKnownSourceState(s.State) {
			errs = append(errs, ValidationError{"invalid_source_state", fmt.Sprintf("sources[%d] state %q", i, s.State)})
		}
		key := s.Role + "\x00" + s.Path
		if _, ok := seenPath[key]; ok {
			errs = append(errs, ValidationError{"duplicate_source", fmt.Sprintf("duplicate source %s %s", s.Role, s.Path)})
		}
		seenPath[key] = struct{}{}
	}

	// Time combo: last_successful_at should not be after captured_at when both set.
	if p.LastSuccessfulAt != nil && !p.CapturedAt.IsZero() && p.LastSuccessfulAt.After(p.CapturedAt.Add(time.Second)) {
		errs = append(errs, ValidationError{"last_successful_after_captured", "last_successful_at after captured_at"})
	}
	return errs
}

// ValidateReplayable asserts non-replayable states do not claim a successful body.
// Call with hasBody=true when SessionDetail.Turns is non-empty.
func ValidateReplayable(p model.SessionProvenance, hasBody bool) []ValidationError {
	var errs []ValidationError
	switch p.State {
	case model.RecordMetadataOnly, model.RecordSourceMissing, model.RecordParserUnsupported:
		if hasBody {
			errs = append(errs, ValidationError{
				"non_replayable_with_body",
				fmt.Sprintf("state %s must not claim replayable body", p.State),
			})
		}
	}
	return errs
}
