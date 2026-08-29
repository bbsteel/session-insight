package openapi

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// candidates.go: sample change-URL analysis and static operation candidate
// scoring (design §7.2/§7.3). Scores are deterministic evidence sums; only
// the probe step confirms them against a real change.

// changeMarkerSegments are path words that introduce a change-request page.
var changeMarkerSegments = map[string]bool{
	"pull": true, "pulls": true, "pull-requests": true, "pr": true, "prs": true,
	"merge_requests": true, "merge-requests": true, "mr": true,
	"reviews": true, "review": true, "changes": true, "change": true,
	"code-reviews": true, "codereview": true,
}

// structuralSegments are grouping words that never belong to a repository
// slug. Leading collection words ("/projects/team/repo/...") are stripped
// from the front; the separator "-" is additionally stripped from the end
// (GitLab's "/repo/-/merge_requests/N"). Singular namespace words like
// "group"/"user" stay: they are usually real slug segments.
var structuralSegments = map[string]bool{
	"projects": true, "repos": true, "repositories": true,
	"orgs": true, "organizations": true, "teams": true,
	"api": true,
}

// trailingSeparators are dropped from the end of the repository range.
var trailingSeparators = map[string]bool{"-": true}

var numericSegment = regexp.MustCompile(`^[0-9]+$`)

// SampleReference is the structural analysis of one example change URL.
type SampleReference struct {
	Origin         string
	Segments       []string
	Number         string
	NumberIndex    int
	MarkerIndex    int
	RepositorySlug string
}

// AnalyzeSampleURL extracts the display origin, repository slug candidate,
// and display number candidate from an example change URL.
func AnalyzeSampleURL(raw string) (SampleReference, bool) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\t\x00") {
		return SampleReference{}, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && u.Scheme != "http") {
		return SampleReference{}, false
	}
	origin := strings.ToLower(u.Scheme) + "://" + u.Host
	trimmed := strings.Trim(u.EscapedPath(), "/")
	if trimmed == "" {
		return SampleReference{}, false
	}
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return SampleReference{}, false
		}
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			return SampleReference{}, false
		}
		_ = decoded
	}

	sample := SampleReference{Origin: origin, Segments: segments, NumberIndex: -1, MarkerIndex: -1}
	for i := len(segments) - 1; i >= 0; i-- {
		if numericSegment.MatchString(segments[i]) {
			sample.Number = segments[i]
			sample.NumberIndex = i
			break
		}
	}
	if sample.Number == "" {
		return SampleReference{}, false
	}
	for i := sample.NumberIndex - 1; i >= 0; i-- {
		if changeMarkerSegments[strings.ToLower(segments[i])] {
			sample.MarkerIndex = i
			break
		}
	}
	repositoryEnd := sample.NumberIndex
	if sample.MarkerIndex >= 0 {
		repositoryEnd = sample.MarkerIndex
	}
	repositoryStart := 0
	for repositoryStart < repositoryEnd && structuralSegments[strings.ToLower(segments[repositoryStart])] {
		repositoryStart++
	}
	// Trailing grouping markers (notably GitLab's "-" separator) never belong
	// to the slug either.
	for repositoryEnd > repositoryStart && trailingSeparators[strings.ToLower(segments[repositoryEnd-1])] {
		repositoryEnd--
	}
	if repositoryStart >= repositoryEnd {
		return SampleReference{}, false
	}
	sample.RepositorySlug = strings.Join(segments[repositoryStart:repositoryEnd], "/")
	return sample, true
}

// OperationCandidate is one scored pairing of a document operation with an
// adapter role.
type OperationCandidate struct {
	Role      OperationID
	Operation SpecOperation
	Score     float64
	Reasons   []string
	// Bindings maps the operation's path parameter names to reference
	// parameters ("reference.repository" / "reference.number"). Every
	// required path parameter must be bound before the candidate is probeable.
	Bindings map[string]string
	// BaseURL is the document server URL this candidate was scored against.
	BaseURL string
}

// candidateMinScore is the floor for a candidate to enter the probe plan.
const candidateMinScore = 0.45

// maxProbeCandidatesPerRole bounds read-only probing per role (design §7.4).
const maxProbeCandidatesPerRole = 3

// ScoreOperations ranks every document operation for every adapter role.
// Operations whose required path parameters cannot be bound to reference
// parameters are excluded.
func ScoreOperations(doc *Document, sample SampleReference, baseURL string) []OperationCandidate {
	candidates := []OperationCandidate{}
	for _, operation := range doc.Operations {
		if operation.Method != "GET" {
			continue
		}
		bindings, ok := bindSampleParameters(operation, sample)
		if !ok {
			continue
		}
		for _, role := range OperationIDs() {
			score, reasons := scoreOperationForRole(role, operation, sample, doc)
			if score < candidateMinScore {
				continue
			}
			candidates = append(candidates, OperationCandidate{
				Role: role, Operation: operation, Score: score,
				Reasons: reasons, Bindings: bindings, BaseURL: baseURL,
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].Role != candidates[j].Role {
			return candidates[i].Role < candidates[j].Role
		}
		return candidates[i].Operation.ID < candidates[j].Operation.ID
	})
	return candidates
}

// TopCandidatesPerRole keeps the highest-scoring probeable candidates per
// role, capped for a bounded probe plan.
func TopCandidatesPerRole(candidates []OperationCandidate) map[OperationID][]OperationCandidate {
	grouped := map[OperationID][]OperationCandidate{}
	for _, candidate := range candidates {
		group := grouped[candidate.Role]
		if len(group) >= maxProbeCandidatesPerRole {
			continue
		}
		grouped[candidate.Role] = append(group, candidate)
	}
	return grouped
}

// bindSampleParameters maps operation path parameters onto the sample URL's
// reference parameters. Name evidence decides: number-like names bind the
// display number, repository-like names bind the repository slug.
func bindSampleParameters(operation SpecOperation, sample SampleReference) (map[string]string, bool) {
	bindings := map[string]string{}
	numberBound := false
	repositoryBound := false
	for _, parameter := range operation.PathParams {
		name := strings.ToLower(parameter.Name)
		switch {
		case isNumberParameterName(name):
			if sample.Number == "" {
				return nil, false
			}
			bindings[parameter.Name] = "reference.number"
			numberBound = true
		case isRepositoryParameterName(name):
			if sample.RepositorySlug == "" {
				return nil, false
			}
			bindings[parameter.Name] = "reference.repository"
			repositoryBound = true
		default:
			if parameter.Required {
				return nil, false
			}
		}
	}
	// A change operation needs at least the change locator bound.
	if len(operation.PathParams) > 0 && !numberBound && !repositoryBound {
		return nil, false
	}
	return bindings, true
}

func isNumberParameterName(name string) bool {
	for _, word := range []string{"number", "iid", "num"} {
		if name == word || strings.Contains(name, word) {
			return true
		}
	}
	return name == "id" || strings.HasSuffix(name, "_id") && !isRepositoryParameterName(name)
}

func isRepositoryParameterName(name string) bool {
	for _, word := range []string{"repository", "repo", "project", "namespace", "owner", "slug", "group"} {
		if name == word || strings.Contains(name, word) {
			return true
		}
	}
	return false
}

// scoreOperationForRole accumulates weighted evidence for one role.
func scoreOperationForRole(role OperationID, operation SpecOperation, sample SampleReference, doc *Document) (float64, []string) {
	score := 0.0
	reasons := []string{}
	add := func(weight float64, reason string) {
		score += weight
		reasons = append(reasons, reason)
	}

	idText := strings.ToLower(operation.ID + " " + strings.Join(operation.Tags, " ") + " " + operation.Summary + " " + operation.Description)
	pathText := strings.ToLower(operation.Path)

	// Shared evidence: domain vocabulary and sample-path overlap.
	domainHit := false
	for _, word := range []string{"pull", "merge", "review", "change", "request"} {
		if strings.Contains(idText, word) {
			domainHit = true
			break
		}
	}
	if domainHit {
		add(0.10, "domain_word_in_operation")
	}
	overlap := 0
	for _, segment := range sample.Segments {
		lower := strings.ToLower(segment)
		if changeMarkerSegments[lower] && strings.Contains(pathText, lower) {
			overlap++
		}
	}
	if overlap > 0 {
		add(0.05, "sample_path_marker_overlap")
	}
	hasNumberParam := false
	hasRepositoryParam := false
	for _, parameter := range operation.PathParams {
		if isNumberParameterName(strings.ToLower(parameter.Name)) {
			hasNumberParam = true
		}
		if isRepositoryParameterName(strings.ToLower(parameter.Name)) {
			hasRepositoryParam = true
		}
	}

	single, list, itemSchema := responseShape(operation)

	switch role {
	case OperationResolveChange:
		if !single {
			return 0, nil
		}
		if hasNumberParam {
			add(0.20, "change_number_parameter")
		}
		if hasRepositoryParam {
			add(0.10, "repository_parameter")
		}
		if schemaHasField(operation.ResponseSchema, "title", "subject", "name", "summary") {
			add(0.20, "title_field")
		}
		if schemaHasField(operation.ResponseSchema, "state", "status", "phase") {
			add(0.15, "state_field")
		}
		if schemaHasField(operation.ResponseSchema, "id", "uuid") {
			add(0.10, "id_field")
		}
	case OperationListFiles:
		if !list {
			return 0, nil
		}
		if schemaHasField(itemSchema, "path", "file", "filename", "new_path", "newPath") {
			add(0.25, "file_path_field")
		}
		if schemaHasField(itemSchema, "status", "state", "type") {
			add(0.15, "file_status_field")
		}
		if schemaHasField(itemSchema, "patch", "diff", "hunks") {
			add(0.10, "embedded_patch_field")
		}
		if strings.Contains(pathText, "file") || strings.Contains(pathText, "diff") || strings.Contains(pathText, "change") {
			add(0.10, "path_file_word")
		}
		if hasNumberParam {
			add(0.10, "change_number_parameter")
		}
	case OperationListCommits:
		if !list {
			return 0, nil
		}
		if schemaHasField(itemSchema, "sha", "commit", "commit_id", "commitId", "hash") {
			add(0.25, "commit_sha_field")
		}
		if schemaHasField(itemSchema, "subject", "message", "title") {
			add(0.10, "commit_subject_field")
		}
		if strings.Contains(pathText, "commit") {
			add(0.15, "path_commit_word")
		}
		if hasNumberParam {
			add(0.10, "change_number_parameter")
		}
	case OperationGetDiff:
		if operation.ProducesText {
			add(0.35, "text_diff_response")
		}
		if strings.Contains(pathText, "diff") || strings.Contains(pathText, "patch") {
			add(0.20, "path_diff_word")
		}
		if hasNumberParam {
			add(0.15, "change_number_parameter")
		}
	case OperationResolveRepository:
		if !single {
			return 0, nil
		}
		if hasRepositoryParam && !hasNumberParam {
			add(0.20, "repository_only_parameter")
		}
		if schemaHasField(operation.ResponseSchema, "id", "uuid") {
			add(0.15, "repository_id_field")
		}
		if schemaHasField(operation.ResponseSchema, "slug", "full_path", "fullPath", "path_with_namespace", "name") {
			add(0.15, "repository_slug_field")
		}
		if strings.Contains(idText, "repo") || strings.Contains(idText, "project") {
			add(0.10, "repository_domain_word")
		}
	}
	return score, reasons
}

// responseShape classifies the success response: single object, list of
// objects, and the item schema for list responses (the array itself or a
// one-level wrapper such as values/items/data).
func responseShape(operation SpecOperation) (single bool, list bool, itemSchema *SpecSchema) {
	schema := operation.ResponseSchema
	if schema == nil {
		return operation.ResponseExample != nil, false, nil
	}
	if schema.Type == "array" && schema.Items != nil {
		return false, true, schema.Items
	}
	if schema.Type == "object" || len(schema.Properties) > 0 {
		for _, wrapper := range []string{"values", "items", "data", "list", "results", "records", "entries"} {
			if property := schema.Properties[wrapper]; property != nil && property.Type == "array" && property.Items != nil {
				return false, true, property.Items
			}
		}
		return true, false, nil
	}
	return false, false, nil
}

// schemaHasField reports whether the schema tree (bounded depth) declares any
// of the named properties.
func schemaHasField(schema *SpecSchema, names ...string) bool {
	return schemaHasFieldDepth(schema, 0, names)
}

func schemaHasFieldDepth(schema *SpecSchema, depth int, names []string) bool {
	if schema == nil || depth > 6 {
		return false
	}
	for _, name := range names {
		if _, ok := schema.Properties[name]; ok {
			return true
		}
	}
	for _, property := range schema.Properties {
		if schemaHasFieldDepth(property, depth+1, names) {
			return true
		}
	}
	return false
}
