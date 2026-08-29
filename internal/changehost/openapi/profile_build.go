package openapi

import (
	"fmt"
	"sort"
	"strings"
)

// profile_build.go: assemble a draft Profile from probed candidates (design
// §7.5/§7.6). Pure: the network executor feeds in probe outcomes, this file
// decides what the profile may claim.

// RoleOutcome is the sanitized result of probing one operation candidate.
type RoleOutcome struct {
	Candidate    OperationCandidate
	StatusCode   int
	Fields       []FieldCandidate
	ItemsPointer string
	// LinkNext / CursorPointer record observed pagination signals.
	LinkNext      bool
	CursorPointer string
	CursorParam   string
	RejectReason  string // stable code when the candidate cannot serve the role
}

// RequiredConfirmation is one mapping decision the inference cannot make
// alone: the user picks one of the listed candidate pointers once.
type RequiredConfirmation struct {
	Role       OperationID      `json:"role"`
	Field      string           `json:"field"`
	Candidates []FieldCandidate `json:"candidates"`
	Reason     string           `json:"reason"`
}

// BuildResult is the assembled draft profile plus what still blocks it.
type BuildResult struct {
	Profile               Profile
	RequiredConfirmations []RequiredConfirmation
	Warnings              []string
}

// requiredChangeFields are the detail fields the activation contract cannot
// live without (design §6.1). display_number and target_repository_slug may
// come from the reference template, so they are not listed here.
var requiredChangeFields = []string{"provider_object_id", "title", "lifecycle_state"}

// BuildProfile assembles the draft profile for one host. `outcomes` must hold
// every probed candidate, including rejected ones; the winning candidate per
// role is the first (highest-scored) outcome without a reject reason.
func BuildProfile(
	profileID string,
	revision int,
	displayName string,
	hostID string,
	displayOrigin string,
	endpointOrigins []string,
	reference ReferenceTemplate,
	authentication Authentication,
	specDigest string,
	specVersion string,
	outcomes map[OperationID][]RoleOutcome,
) BuildResult {
	result := BuildResult{Warnings: []string{}, RequiredConfirmations: []RequiredConfirmation{}}
	profile := Profile{
		SchemaVersion:   SchemaVersion,
		ProfileID:       profileID,
		ProfileRevision: revision,
		DisplayName:     displayName,
		Adapter:         AdapterKind,
		HostID:          hostID,
		DisplayOrigin:   displayOrigin,
		EndpointOrigins: endpointOrigins,
		Reference:       reference,
		Authentication:  authentication,
		SpecDigest:      specDigest,
		Capabilities: Capabilities{
			Metadata: CapabilityUnsupported, FileSet: CapabilityUnsupported,
			Patches: CapabilityUnsupported, Modes: CapabilityUnsupported,
			Commits: CapabilityUnsupported, RepositoryID: CapabilityUnsupported,
		},
	}

	changeOutcomes := outcomes[OperationResolveChange]
	var changeWinner *RoleOutcome
	for i := range changeOutcomes {
		if changeOutcomes[i].RejectReason == "" && changeOutcomes[i].StatusCode >= 200 && changeOutcomes[i].StatusCode < 300 {
			changeWinner = &changeOutcomes[i]
			break
		}
	}
	if changeWinner == nil {
		result.Warnings = append(result.Warnings, string(IssueProbeFailed))
		result.Profile = profile
		return result
	}

	fields, confirmations, warnings := selectFields(OperationResolveChange, changeWinner.Fields, requiredChangeFields)
	result.RequiredConfirmations = append(result.RequiredConfirmations, confirmations...)
	result.Warnings = append(result.Warnings, warnings...)

	// Content anchor: a confident head_sha is the primary anchor. updated_at
	// and friends never qualify (they score below the confirmation floor by
	// construction).
	anchor := ""
	if candidate := topFieldCandidate(changeWinner.Fields, "head_sha"); candidate != nil && candidate.Confidence >= ConfidenceConfirmPick {
		anchor = "head_sha"
		if candidate.Confidence < ConfidenceAutoPick {
			result.RequiredConfirmations = append(result.RequiredConfirmations, RequiredConfirmation{
				Role: OperationResolveChange, Field: "head_sha",
				Candidates: []FieldCandidate{*candidate}, Reason: "content_anchor_needs_confirmation",
			})
		}
	}
	if anchor == "" {
		if candidate := topFieldCandidate(changeWinner.Fields, "native_version"); candidate != nil && candidate.Confidence >= ConfidenceConfirmPick {
			anchor = "native_version"
		}
	}
	if anchor == "" {
		result.Warnings = append(result.Warnings, string(IssueContentAnchorMissing))
	}

	profile.Operations.ResolveChange = buildOperation(changeWinner, fields)
	profile.Capabilities.Metadata = CapabilitySupported
	profile.Capabilities.ContentAnchor = anchor
	if _, mapped := fields["target_repository_id"]; mapped {
		profile.Capabilities.RepositoryID = CapabilitySupported
	}

	// Optional roles degrade independently (design §6.3).
	if winner := firstSuccessfulOutcome(outcomes[OperationListFiles]); winner != nil {
		fileFields, confirmations, _ := selectFields(OperationListFiles, winner.Fields, []string{"path", "status"})
		result.RequiredConfirmations = append(result.RequiredConfirmations, confirmations...)
		operation := buildOperation(winner, fileFields)
		operation.Response.ItemsPointer = winner.ItemsPointer
		operation.Pagination = inferPagination(*winner)
		profile.Operations.ListFiles = operation
		profile.Capabilities.FileSet = CapabilitySupported
		if _, hasPatch := fileFields["patch"]; hasPatch {
			profile.Capabilities.Patches = CapabilitySupported
		}
		if _, hasMode := fileFields["new_mode"]; hasMode {
			profile.Capabilities.Modes = CapabilitySupported
		}
	}
	if winner := firstSuccessfulOutcome(outcomes[OperationListCommits]); winner != nil {
		commitFields, confirmations, _ := selectFields(OperationListCommits, winner.Fields, []string{"sha"})
		result.RequiredConfirmations = append(result.RequiredConfirmations, confirmations...)
		operation := buildOperation(winner, commitFields)
		operation.Response.ItemsPointer = winner.ItemsPointer
		operation.Pagination = inferPagination(*winner)
		profile.Operations.ListCommits = operation
		profile.Capabilities.Commits = CapabilitySupported
	}
	if winner := firstSuccessfulOutcome(outcomes[OperationGetDiff]); winner != nil {
		profile.Operations.GetDiff = buildOperation(winner, map[string]FieldSelector{
			"diff_text": {Pointer: ""},
		})
		if profile.Capabilities.FileSet != CapabilitySupported {
			profile.Capabilities.FileSet = CapabilitySupported
		}
		profile.Capabilities.Patches = CapabilitySupported
	}

	result.Profile = profile
	return result
}

func firstSuccessfulOutcome(outcomes []RoleOutcome) *RoleOutcome {
	for i := range outcomes {
		if outcomes[i].RejectReason == "" && outcomes[i].StatusCode >= 200 && outcomes[i].StatusCode < 300 {
			return &outcomes[i]
		}
	}
	return nil
}

// selectFields applies the confidence thresholds: auto-pick >= 0.90, user
// confirmation between 0.65 and 0.90, unmapped below. Required fields that
// land below the confirmation floor are reported as blocking.
func selectFields(role OperationID, candidates []FieldCandidate, required []string) (map[string]FieldSelector, []RequiredConfirmation, []string) {
	fields := map[string]FieldSelector{}
	confirmations := []RequiredConfirmation{}
	warnings := []string{}

	byField := map[string][]FieldCandidate{}
	for _, candidate := range candidates {
		byField[candidate.Field] = append(byField[candidate.Field], candidate)
	}
	names := make([]string, 0, len(byField))
	for field := range byField {
		names = append(names, field)
	}
	sort.Strings(names)
	for _, field := range names {
		group := byField[field]
		top := group[0]
		switch {
		case top.Confidence >= ConfidenceAutoPick && len(group) == 1:
			fields[field] = FieldSelector{Pointer: top.Pointer, Transform: top.Transform}
		case top.Confidence >= ConfidenceConfirmPick:
			// Confident but ambiguous, or mid-confidence: confirm once.
			confirmations = append(confirmations, RequiredConfirmation{
				Role: role, Field: field, Candidates: group, Reason: "confidence_or_ambiguity",
			})
		}
	}
	for _, field := range required {
		if _, mapped := fields[field]; mapped {
			continue
		}
		pending := false
		for _, confirmation := range confirmations {
			if confirmation.Field == field {
				pending = true
			}
		}
		if !pending {
			warnings = append(warnings, fmt.Sprintf("%s:%s", IssueMappingIncomplete, field))
		}
	}
	return fields, confirmations, warnings
}

func topFieldCandidate(candidates []FieldCandidate, field string) *FieldCandidate {
	for i := range candidates {
		if candidates[i].Field == field {
			return &candidates[i]
		}
	}
	return nil
}

// buildOperation converts a winning candidate into a profile Operation. The
// origin is the candidate base URL's origin; the path template keeps the
// document's parameter placeholders.
func buildOperation(outcome *RoleOutcome, fields map[string]FieldSelector) *Operation {
	origin := outcome.Candidate.BaseURL
	pathTemplate := outcome.Candidate.Operation.Path
	if idx := strings.Index(origin, "://"); idx >= 0 {
		rest := origin[idx+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			// The document server URL carries a base path; fold it into the
			// operation path and keep a pure origin.
			pathTemplate = rest[slash:] + pathTemplate
			origin = origin[:idx+3+slash]
		}
	}
	return &Operation{
		Method:       "GET",
		Origin:       origin,
		PathTemplate: pathTemplate,
		Parameters:   outcome.Candidate.Bindings,
		Headers:      map[string]string{"Accept": "application/json"},
		Pagination:   Pagination{Mode: PaginationNone},
		Response: OperationResponse{
			Fields: fields,
		},
	}
}

// inferPagination derives the pagination declaration from probe-observed
// signals only; without a signal the operation is a single page.
func inferPagination(outcome RoleOutcome) Pagination {
	if outcome.CursorPointer != "" && outcome.CursorParam != "" {
		return Pagination{
			Mode:              PaginationCursorBody,
			CursorParameter:   outcome.CursorParam,
			NextCursorPointer: outcome.CursorPointer,
		}
	}
	if outcome.LinkNext {
		return Pagination{Mode: PaginationLinkHeader}
	}
	return Pagination{Mode: PaginationNone}
}
