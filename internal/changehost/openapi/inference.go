package openapi

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// inference.go: field-candidate scoring over real (probed) response values
// (design §7.5). Everything here is pure; the network probe that produced
// the response bodies lives in the changehost package.
//
// The report is sanitized by construction: candidates carry the JSON pointer,
// a shape descriptor, and a confidence score — never the raw value.

// Confidence thresholds from the design: >= AutoPick is applied silently,
// between ConfirmPick and AutoPick requires one user confirmation, below
// ConfirmPick the field is left unmapped.
const (
	ConfidenceAutoPick    = 0.90
	ConfidenceConfirmPick = 0.65
)

// FieldCandidate is one scored pointer-to-standard-field mapping.
type FieldCandidate struct {
	Field      string
	Pointer    string
	Confidence float64
	Shape      string
	Transform  *FieldTransform
	Evidence   []string
}

// InferenceContext carries the known answers from the example change for
// cross-validation (design §7.6).
type InferenceContext struct {
	SampleNumber         string
	SampleRepositorySlug string
	DisplayOrigin        string
}

var gitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

// observedField is one leaf value with its pointer, produced by walking a
// probed response.
type observedField struct {
	pointer string
	name    string
	value   any
}

// InferChangeFields scores detail-response fields for resolve_change.
func InferChangeFields(response any, ctx InferenceContext) []FieldCandidate {
	leaves := collectLeaves(response, "", 0)
	scored := []FieldCandidate{}
	for _, leaf := range leaves {
		for _, candidate := range scoreChangeLeaf(leaf, ctx) {
			scored = append(scored, candidate)
		}
	}
	return bestCandidatesPerField(scored)
}

// InferListFields locates the items array in a list response and scores
// element-relative fields for list_files / list_commits.
func InferListFields(response any, role OperationID, ctx InferenceContext) (itemsPointer string, candidates []FieldCandidate) {
	itemsPointer, items, found := findItemsArray(response)
	if !found || len(items) == 0 {
		return "", nil
	}
	// Score against the first few elements; keep pointers that hold for every
	// sampled element (a missing key on any element drops the candidate's
	// confidence instead of fabricating presence).
	sampleLimit := len(items)
	if sampleLimit > 3 {
		sampleLimit = 3
	}
	var merged []FieldCandidate
	for i := 0; i < sampleLimit; i++ {
		leaves := collectLeaves(items[i], "", 0)
		var per []FieldCandidate
		for _, leaf := range leaves {
			switch role {
			case OperationListFiles:
				per = append(per, scoreFileLeaf(leaf)...)
			case OperationListCommits:
				per = append(per, scoreCommitLeaf(leaf)...)
			}
		}
		if i == 0 {
			merged = per
			continue
		}
		merged = intersectCandidates(merged, per)
	}
	return itemsPointer, bestCandidatesPerField(merged)
}

func collectLeaves(value any, prefix string, depth int) []observedField {
	if depth > maxSchemaDepth {
		return nil
	}
	switch typed := value.(type) {
	case map[string]any:
		leaves := []observedField{}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := typed[key]
			pointer := prefix + "/" + escapeJSONPointerSegment(key)
			switch child.(type) {
			case map[string]any:
				leaves = append(leaves, collectLeaves(child, pointer, depth+1)...)
			case []any:
				// Arrays are list payloads, not scalar fields; the list
				// inference path handles them via findItemsArray.
			default:
				leaves = append(leaves, observedField{pointer: pointer, name: key, value: child})
			}
		}
		return leaves
	}
	return nil
}

func escapeJSONPointerSegment(segment string) string {
	return strings.ReplaceAll(strings.ReplaceAll(segment, "~", "~0"), "/", "~1")
}

// findItemsArray locates the array-of-objects payload: the root array itself,
// or a one-level wrapper property (values/items/data/list/...).
func findItemsArray(response any) (pointer string, items []any, found bool) {
	// A root-level array has an empty items pointer: elements are the body.
	if array, ok := response.([]any); ok && arrayContainsObjects(array) {
		return "", array, true
	}
	object, ok := response.(map[string]any)
	if !ok {
		return "", nil, false
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if array, ok := object[key].([]any); ok && arrayContainsObjects(array) {
			return "/" + escapeJSONPointerSegment(key), array, true
		}
	}
	return "", nil, false
}

func arrayContainsObjects(array []any) bool {
	if len(array) == 0 {
		return false
	}
	_, ok := array[0].(map[string]any)
	return ok
}

// intersectCandidates keeps only pointers present (with a compatible shape)
// in every sampled element, capping confidence when presence is partial.
func intersectCandidates(accumulated, next []FieldCandidate) []FieldCandidate {
	byPointer := map[string]FieldCandidate{}
	for _, candidate := range next {
		byPointer[candidate.Field+"|"+candidate.Pointer] = candidate
	}
	kept := []FieldCandidate{}
	for _, candidate := range accumulated {
		if _, ok := byPointer[candidate.Field+"|"+candidate.Pointer]; ok {
			kept = append(kept, candidate)
		}
	}
	return kept
}

// bestCandidatesPerField keeps, per standard field, the highest-confidence
// pointers in stable order.
func bestCandidatesPerField(candidates []FieldCandidate) []FieldCandidate {
	best := map[string][]FieldCandidate{}
	for _, candidate := range candidates {
		group := best[candidate.Field]
		if len(group) > 0 && candidate.Confidence < group[0].Confidence-1e-9 {
			continue
		}
		if len(group) > 0 && candidate.Confidence > group[0].Confidence+1e-9 {
			group = nil
		}
		group = append(group, candidate)
		best[candidate.Field] = group
	}
	fields := make([]string, 0, len(best))
	for field := range best {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	merged := []FieldCandidate{}
	for _, field := range fields {
		merged = append(merged, best[field]...)
	}
	return merged
}

// --- leaf scoring ---------------------------------------------------------

func scoreChangeLeaf(leaf observedField, ctx InferenceContext) []FieldCandidate {
	name := normalizeFieldName(leaf.name)
	text, isString := leaf.value.(string)
	number, isNumber := jsonNumber(leaf.value)
	var candidates []FieldCandidate
	add := func(field string, confidence float64, shape string, transform *FieldTransform, evidence ...string) {
		candidates = append(candidates, FieldCandidate{
			Field: field, Pointer: leaf.pointer, Confidence: confidence,
			Shape: shape, Transform: transform, Evidence: evidence,
		})
	}

	if isString && gitSHAPattern.MatchString(text) {
		switch {
		case strings.Contains(name, "head") || strings.Contains(name, "source") || strings.Contains(name, "latestcommit") || strings.Contains(name, "fromref"):
			add("head_sha", 0.95, "git_sha", &FieldTransform{Name: TransformGitSHA}, "name_and_sha_format")
		case strings.Contains(name, "diffbase") || strings.Contains(name, "mergebase"):
			add("diff_base_sha", 0.9, "git_sha", &FieldTransform{Name: TransformGitSHA}, "name_and_sha_format")
		case strings.Contains(name, "base") || strings.Contains(name, "target") || strings.Contains(name, "destination"):
			add("base_sha", 0.85, "git_sha", &FieldTransform{Name: TransformGitSHA}, "name_and_sha_format")
		case strings.Contains(name, "mergecommit"):
			add("merge_commit_sha", 0.9, "git_sha", &FieldTransform{Name: TransformGitSHA}, "name_and_sha_format")
		default:
			add("head_sha", 0.5, "git_sha", &FieldTransform{Name: TransformGitSHA}, "sha_format_only")
		}
	}

	switch name {
	case "title":
		if isString && len(text) > 0 {
			add("title", 0.95, "string", nil, "name_exact")
		}
	case "subject", "name", "summary":
		if isString && len(text) > 0 {
			add("title", 0.6, "string", nil, "name_similar")
		}
	}

	if name == "state" || name == "status" || name == "phase" {
		if isString {
			confidence := 0.7
			evidence := []string{"name_match"}
			if looksLikeLifecycle(text) {
				confidence = 0.95
				evidence = append(evidence, "lifecycle_vocabulary")
			}
			add("lifecycle_state", confidence, "string", &FieldTransform{Name: TransformChangeStatus}, evidence...)
		}
	}

	if isNumber && ctx.SampleNumber != "" && formatJSONNumber(number) == ctx.SampleNumber {
		confidence := 0.75
		evidence := []string{"matches_sample_number"}
		if name == "number" || name == "iid" || strings.Contains(name, "number") {
			confidence = 0.97
			evidence = append(evidence, "name_match")
		}
		add("display_number", confidence, "integer", &FieldTransform{Name: TransformIntegerToStr}, evidence...)
	}

	if isNumber && (name == "id" || strings.HasSuffix(name, "id") && !strings.Contains(name, "number")) {
		confidence := 0.55
		if name == "id" {
			confidence = 0.7
		}
		if name == "reviewid" || name == "pullrequestid" || name == "mergequestid" || name == "changeid" || name == "crid" {
			confidence = 0.9
		}
		add("provider_object_id", confidence, "integer", &FieldTransform{Name: TransformIntegerToStr}, "name_match")
	}
	if isString && (name == "uuid" || name == "key" && strings.Contains(leaf.pointer, "review")) {
		add("provider_object_id", 0.6, "string", nil, "name_match")
	}

	if isString && (name == "url" || name == "weburl" || name == "htmlurl" || name == "link" || name == "weburlabsolute") {
		confidence := 0.6
		if ctx.DisplayOrigin != "" && strings.HasPrefix(strings.ToLower(text), strings.ToLower(ctx.DisplayOrigin)) {
			confidence = 0.95
		}
		add("web_url", confidence, "url", nil, "url_value")
	}

	if isString && (name == "slug" || name == "fullpath" || name == "pathwithnamespace") {
		confidence := 0.6
		if ctx.SampleRepositorySlug != "" && text == ctx.SampleRepositorySlug {
			confidence = 0.95
		}
		add("target_repository_slug", confidence, "string", &FieldTransform{Name: TransformRepositorySlug}, "slug_value")
	}
	if isNumber && (name == "repositoryid" || name == "projectid" || name == "repoid") {
		add("target_repository_id", 0.8, "integer", &FieldTransform{Name: TransformIntegerToStr}, "name_match")
	}

	if isString && (strings.Contains(name, "branch") || strings.HasSuffix(name, "ref")) {
		if strings.Contains(name, "source") || strings.Contains(name, "from") || strings.Contains(name, "head") {
			add("source_ref", 0.8, "string", nil, "branch_name")
		} else if strings.Contains(name, "target") || strings.Contains(name, "destination") || strings.Contains(name, "base") || strings.Contains(name, "into") {
			add("target_ref", 0.8, "string", nil, "branch_name")
		}
	}

	if isString && isRFC3339(text) {
		if strings.Contains(name, "update") {
			add("native_version", 0.2, "rfc3339", &FieldTransform{Name: TransformRFC3339Time}, "updated_at_not_a_content_anchor")
		}
	}
	return candidates
}

func scoreFileLeaf(leaf observedField) []FieldCandidate {
	name := normalizeFieldName(leaf.name)
	text, isString := leaf.value.(string)
	var candidates []FieldCandidate
	add := func(field string, confidence float64, shape string, transform *FieldTransform, evidence ...string) {
		candidates = append(candidates, FieldCandidate{
			Field: field, Pointer: leaf.pointer, Confidence: confidence,
			Shape: shape, Transform: transform, Evidence: evidence,
		})
	}
	if !isString {
		return candidates
	}
	switch {
	case name == "path" || name == "filepath" || name == "newpath" || name == "filename":
		add("path", 0.9, "string", nil, "name_match")
	case name == "oldpath" || name == "previouspath":
		add("old_path", 0.9, "string", nil, "name_match")
	case name == "status" || name == "state" || name == "changetype":
		confidence := 0.7
		if looksLikeFileStatus(text) {
			confidence = 0.95
		}
		add("status", confidence, "string", &FieldTransform{Name: TransformFileStatus}, "status_value")
	case name == "patch" || name == "diff" || name == "hunks":
		confidence := 0.6
		if strings.Contains(text, "@@") || strings.HasPrefix(text, "diff ") {
			confidence = 0.95
		}
		add("patch", confidence, "string", nil, "patch_shape")
	case name == "oldmode" || name == "newmode":
		add(strings.Replace(name, "mode", "_mode", 1), 0.85, "string", nil, "name_match")
	}
	return candidates
}

func scoreCommitLeaf(leaf observedField) []FieldCandidate {
	name := normalizeFieldName(leaf.name)
	text, isString := leaf.value.(string)
	var candidates []FieldCandidate
	add := func(field string, confidence float64, shape string, transform *FieldTransform, evidence ...string) {
		candidates = append(candidates, FieldCandidate{
			Field: field, Pointer: leaf.pointer, Confidence: confidence,
			Shape: shape, Transform: transform, Evidence: evidence,
		})
	}
	if isString && gitSHAPattern.MatchString(text) {
		confidence := 0.75
		if name == "sha" || name == "id" || name == "commit" || name == "commitid" || name == "hash" {
			confidence = 0.95
		}
		add("sha", confidence, "git_sha", &FieldTransform{Name: TransformGitSHA}, "sha_format")
		return candidates
	}
	if !isString {
		return candidates
	}
	switch name {
	case "subject", "message", "title", "summary":
		add("subject", 0.85, "string", nil, "name_match")
	case "authorname", "author", "committer", "committername":
		add("author_name", 0.8, "string", nil, "name_match")
	case "authoredat", "authoreddate", "authordate", "createdat":
		if isRFC3339(text) {
			add("authored_at", 0.9, "rfc3339", &FieldTransform{Name: TransformRFC3339Time}, "rfc3339_value")
		}
	case "committedat", "committerdate", "commitdate":
		if isRFC3339(text) {
			add("committed_at", 0.9, "rfc3339", &FieldTransform{Name: TransformRFC3339Time}, "rfc3339_value")
		}
	}
	return candidates
}

// --- value shape helpers ---------------------------------------------------

// normalizeFieldName removes separators and case so sourceBranch,
// source_branch and source-branch compare equal.
func normalizeFieldName(name string) string {
	name = strings.ToLower(name)
	return strings.NewReplacer("_", "", "-", "", ".", "").Replace(name)
}

func jsonNumber(value any) (float64, bool) {
	// Responses are decoded with encoding/json's default number handling, so
	// float64 is the only numeric representation here; int64 covers decoders
	// that use UseNumber-adjacent integer paths.
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int64:
		return float64(typed), true
	}
	return 0, false
}

func formatJSONNumber(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d", int64(value))
	}
	return fmt.Sprintf("%v", value)
}

func looksLikeLifecycle(value string) bool {
	switch normalizeFieldName(value) {
	case "open", "opened", "merged", "closed", "abandoned", "draft",
		"approved", "active", "new", "inreview", "reviewing":
		return true
	}
	return false
}

func looksLikeFileStatus(value string) bool {
	switch normalizeFieldName(value) {
	case "added", "modified", "deleted", "removed", "renamed", "copied",
		"changed", "new", "add", "delete", "modify":
		return true
	}
	return false
}

func isRFC3339(value string) bool {
	if len(value) < 16 || len(value) > 40 {
		return false
	}
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}
