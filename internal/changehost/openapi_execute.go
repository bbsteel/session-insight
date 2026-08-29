package changehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/bbsteel/session-insight/internal/changehost/openapi"
	"github.com/bbsteel/session-insight/internal/model"
)

// openapi_execute.go: response assembly for the profile-driven provider —
// summary/file/commit construction, pagination execution, and unified-diff
// parsing. Every dimension degrades independently (design §6.3).

// summaryFromDetail maps a detail document onto the standard summary model.
// Required-field failures are drift signals; optional fields degrade.
func (p *OpenAPIProvider) summaryFromDetail(document any, reference model.ChangeRequestReference) (model.ChangeRequestSummary, error) {
	fields := p.profile.Operations.ResolveChange.Response.Fields
	read := func(name string) (string, error) {
		selector, ok := fields[name]
		if !ok {
			return "", nil
		}
		return openapi.EvalSelector(document, selector)
	}
	readOptional := func(name string) string {
		value, err := read(name)
		if err != nil {
			return ""
		}
		return value
	}

	objectID, err := read("provider_object_id")
	if err != nil || objectID == "" {
		return model.ChangeRequestSummary{}, providerError(OperationResolveChange, ErrorInvalidResponse,
			fmt.Errorf("required field provider_object_id: %w", err))
	}
	title, err := read("title")
	if err != nil || title == "" {
		return model.ChangeRequestSummary{}, providerError(OperationResolveChange, ErrorInvalidResponse,
			fmt.Errorf("required field title: %w", err))
	}
	lifecycle, err := read("lifecycle_state")
	if err != nil || lifecycle == "" {
		return model.ChangeRequestSummary{}, providerError(OperationResolveChange, ErrorInvalidResponse,
			fmt.Errorf("required field lifecycle_state: %w", err))
	}

	displayNumber := reference.DisplayNumber
	if value := readOptional("display_number"); value != "" {
		displayNumber = value
	}
	webURL := reference.NormalizedURL
	if value := readOptional("web_url"); value != "" && providerURLMatchesOrigin(value, p.host.DisplayOrigin) {
		webURL = value
	}
	repositorySlug := reference.TargetRepositorySlug
	if value := readOptional("target_repository_slug"); value != "" {
		// The response-supplied slug feeds later requests; it must be a plain
		// safe path, never a traversal payload.
		if !safeProviderPath(value) {
			return model.ChangeRequestSummary{}, providerError(OperationResolveChange, ErrorInvalidResponse,
				fmt.Errorf("response repository slug is unsafe"))
		}
		repositorySlug = value
	}
	repository := p.repositoryIdentity(repositorySlug, readOptional("target_repository_id"))

	// The locator must round-trip: GetSnapshot re-fetches by display number.
	providerObjectID := objectID
	if objectID != displayNumber {
		providerObjectID = "obj-" + objectID + "-num-" + displayNumber
	}

	summary := model.ChangeRequestSummary{
		Identity: model.ChangeRequestIdentity{
			Provider:         p.Kind(),
			HostID:           p.host.Key,
			TargetRepository: &repository,
			ProviderObjectID: providerObjectID,
		},
		Kind:            model.ChangeRequestPullRequest,
		DisplayNumber:   displayNumber,
		LifecycleState:  model.ChangeRequestLifecycleState(lifecycle),
		Title:           title,
		WebURL:          webURL,
		SourceRef:       readOptional("source_ref"),
		TargetRef:       readOptional("target_ref"),
		MergeCommitSHA:  readOptional("merge_commit_sha"),
		SquashCommitSHA: readOptional("squash_commit_sha"),
		Completeness:    p.summaryCompleteness(),
	}
	switch model.ChangeRequestLifecycleState(lifecycle) {
	case model.ChangeLifecycleOpen, model.ChangeLifecycleMerged, model.ChangeLifecycleClosed,
		model.ChangeLifecycleAbandoned, model.ChangeLifecycleUnknown:
	default:
		summary.LifecycleState = model.ChangeLifecycleUnknown
	}
	return summary, nil
}

// repositoryIdentity builds the target repository identity. Platform-native
// immutable IDs win; the slug fallback marks identity assessments as
// estimated (design §6.2).
func (p *OpenAPIProvider) repositoryIdentity(slug, platformID string) model.HostedRepositoryIdentity {
	if platformID != "" {
		return model.HostedRepositoryIdentity{HostID: p.host.Key, ImmutableID: platformID, Slug: slug}
	}
	return model.HostedRepositoryIdentity{HostID: p.host.Key, ImmutableID: "slug:" + slug, Slug: slug}
}

func (p *OpenAPIProvider) repositoryFromDocument(document any, fallbackSlug string) (model.HostedRepositoryIdentity, error) {
	operation := p.profile.Operations.ResolveRepository
	if operation == nil {
		return model.HostedRepositoryIdentity{}, providerError(OperationResolveRepository, ErrorUnsupported, nil)
	}
	slug := fallbackSlug
	if selector, ok := operation.Response.Fields["repository_slug"]; ok {
		if value, err := openapi.EvalSelector(document, selector); err == nil && value != "" {
			slug = value
		}
	}
	platformID := ""
	if selector, ok := operation.Response.Fields["repository_id"]; ok {
		value, err := openapi.EvalSelector(document, selector)
		if err != nil {
			return model.HostedRepositoryIdentity{}, providerError(OperationResolveRepository, ErrorInvalidResponse, err)
		}
		platformID = value
	}
	if platformID == "" {
		return model.HostedRepositoryIdentity{}, providerError(OperationResolveRepository, ErrorInvalidResponse,
			fmt.Errorf("repository response carries no platform id"))
	}
	return p.repositoryIdentity(slug, platformID), nil
}

// summaryCompleteness reports metadata as exact and the remaining dimensions
// from the profile declaration.
func (p *OpenAPIProvider) summaryCompleteness() model.ChangeRequestCompleteness {
	dimension := func(state openapi.CapabilityState) model.GitEvidenceAssessment {
		if state == openapi.CapabilitySupported {
			return model.ExactGitEvidence()
		}
		return model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeProviderUnsupported)
	}
	return model.ChangeRequestCompleteness{
		Metadata: model.ExactGitEvidence(),
		FileSet:  dimension(p.profile.Capabilities.FileSet),
		Patches:  dimension(p.profile.Capabilities.Patches),
		Modes:    dimension(p.profile.Capabilities.Modes),
		Commits:  dimension(p.profile.Capabilities.Commits),
	}
}

// --- files ------------------------------------------------------------

// collectFiles gathers the file set from list_files (paginated, embedded
// patches) or get_diff (standalone unified diff), then degrades each
// dimension independently.
func (p *OpenAPIProvider) collectFiles(
	ctx context.Context, repository, number string,
) (files []model.GitFileChange, contents []SnapshotContent, manifest string,
	fileSet, patches, modes model.GitEvidenceAssessment, metadata ResultMetadata, err error,
) {
	unavailable := model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeProviderUnsupported)
	fileSet, patches, modes = unavailable, unavailable, unavailable
	files = []model.GitFileChange{}
	contents = []SnapshotContent{}

	if p.profile.Operations.ListFiles != nil {
		var items []any
		var overflow bool
		items, metadata, overflow, err = p.fetchPages(ctx, OperationGetSnapshot, p.profile.Operations.ListFiles, repository, number, p.fileLimit())
		if err != nil {
			return nil, nil, "", fileSet, patches, modes, metadata, err
		}
		files, contents, patches, modes, err = p.filesFromItems(items)
		if err != nil {
			return nil, nil, "", fileSet, patches, modes, metadata, err
		}
		if overflow {
			fileSet = model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonChangeRequestOverflow)
			if patches.State == model.GitEvidenceExact {
				patches = fileSet
			}
			if modes.State == model.GitEvidenceExact {
				modes = fileSet
			}
		} else {
			fileSet = model.ExactGitEvidence()
		}
	} else if p.profile.Operations.GetDiff != nil {
		var raw []byte
		raw, metadata, err = p.fetchDiffText(ctx, repository, number)
		if err != nil {
			return nil, nil, "", fileSet, patches, modes, metadata, err
		}
		files, contents, err = filesFromUnifiedDiff(p.Kind(), repository, number, raw)
		if err != nil {
			return nil, nil, "", fileSet, patches, modes, metadata, err
		}
		fileSet = model.ExactGitEvidence()
		patches = model.ExactGitEvidence()
	}
	manifest = fileManifestDigest(files)
	return files, contents, manifest, fileSet, patches, modes, metadata, nil
}

func (p *OpenAPIProvider) fileLimit() int64 {
	if p.profile.Limits.MaximumFiles > 0 && p.profile.Limits.MaximumFiles < safetyMaximumFiles {
		return p.profile.Limits.MaximumFiles
	}
	return safetyMaximumFiles
}

func (p *OpenAPIProvider) commitLimit() int64 {
	if p.profile.Limits.MaximumCommits > 0 && p.profile.Limits.MaximumCommits < safetyMaximumCommits {
		return p.profile.Limits.MaximumCommits
	}
	return safetyMaximumCommits
}

const (
	safetyMaximumFiles   = 5000
	safetyMaximumCommits = 500
	safetyMaximumPages   = 50
)

// filesFromItems maps list response elements onto file rows.
func (p *OpenAPIProvider) filesFromItems(items []any) ([]model.GitFileChange, []SnapshotContent, model.GitEvidenceAssessment, model.GitEvidenceAssessment, error) {
	fields := p.profile.Operations.ListFiles.Response.Fields
	patches := model.ExactGitEvidence()
	modes := model.ExactGitEvidence()
	if _, ok := fields["patch"]; !ok {
		patches = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeProviderUnsupported)
	}
	if _, ok := fields["new_mode"]; !ok {
		modes = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeProviderUnsupported)
	}
	files := []model.GitFileChange{}
	contents := []SnapshotContent{}
	readItem := func(item any, name string) string {
		selector, ok := fields[name]
		if !ok {
			return ""
		}
		value, err := openapi.EvalSelector(item, selector)
		if err != nil {
			return ""
		}
		return value
	}
	for i, item := range items {
		path := readItem(item, "path")
		if path == "" || !safeProviderPath(path) {
			return nil, nil, patches, modes, fmt.Errorf("file %d carries an invalid path", i)
		}
		status := readItem(item, "status")
		if !isStandardFileStatus(status) {
			return nil, nil, patches, modes, fmt.Errorf("file %d carries an unmappable status", i)
		}
		oldPath := readItem(item, "old_path")
		if oldPath != "" && !safeProviderPath(oldPath) {
			return nil, nil, patches, modes, fmt.Errorf("file %d carries an invalid old path", i)
		}
		file := model.GitFileChange{
			Ordinal: i, Key: providerFileKey(p.Kind(), p.profile.ProfileID, oldPath, path),
			Layer: model.GitFileLayerHosted, DisplayPath: path, OldDisplayPath: oldPath,
			PathEncoding: model.GitPathUTF8, Status: model.GitFileStatus(status),
			OldMode: readItem(item, "old_mode"), NewMode: readItem(item, "new_mode"),
			StatusAssessment: model.ExactGitEvidence(),
			PatchAssessment:  patches,
			Evidence:         []model.GitEvidenceLink{},
		}
		if patch := readItem(item, "patch"); patch != "" {
			contents = append(contents, SnapshotContent{
				FileKey: file.Key, Purpose: SnapshotContentPatch, Content: []byte(patch),
			})
		} else if patches.State == model.GitEvidenceExact {
			file.PatchAssessment = model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonChangeRequestPartial)
		}
		files = append(files, file)
	}
	return files, contents, patches, modes, nil
}

func isStandardFileStatus(status string) bool {
	switch model.GitFileStatus(status) {
	case model.GitFileAdded, model.GitFileModified, model.GitFileDeleted, model.GitFileRenamed, model.GitFileCopied:
		return true
	}
	return false
}

func fileManifestDigest(files []model.GitFileChange) string {
	lines := make([]string, 0, len(files))
	for _, file := range files {
		lines = append(lines, string(file.Status)+":"+file.OldDisplayPath+":"+file.DisplayPath)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// --- commits ------------------------------------------------------------

func (p *OpenAPIProvider) collectCommits(
	ctx context.Context, repository, number string,
) ([]model.GitCandidateCommit, model.GitEvidenceAssessment, ResultMetadata, error) {
	unavailable := model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeProviderUnsupported)
	if p.profile.Operations.ListCommits == nil {
		return []model.GitCandidateCommit{}, unavailable, ResultMetadata{Assessment: unavailable}, nil
	}
	items, metadata, overflow, err := p.fetchPages(ctx, OperationGetSnapshot, p.profile.Operations.ListCommits, repository, number, p.commitLimit())
	if err != nil {
		return nil, unavailable, metadata, err
	}
	fields := p.profile.Operations.ListCommits.Response.Fields
	readItem := func(item any, name string) (string, error) {
		selector, ok := fields[name]
		if !ok {
			return "", nil
		}
		return openapi.EvalSelector(item, selector)
	}
	commits := []model.GitCandidateCommit{}
	for i, item := range items {
		sha, err := readItem(item, "sha")
		if err != nil || sha == "" {
			return nil, unavailable, metadata, fmt.Errorf("commit %d carries no valid sha: %w", i, err)
		}
		subject, err := readItem(item, "subject")
		if err != nil || subject == "" {
			return nil, unavailable, metadata, fmt.Errorf("commit %d carries no subject: %w", i, err)
		}
		commit := model.GitCandidateCommit{
			Ordinal: i, SHA: sha, Subject: subject,
			AuthorName:  mustReadCommitField(fields, item, "author_name"),
			AuthoredAt:  parseProviderTime(mustReadCommitField(fields, item, "authored_at")),
			CommittedAt: parseProviderTime(mustReadCommitField(fields, item, "committed_at")),
			Relation:    model.GitCommitChangeMembership,
			Assessment:  model.ExactGitEvidence(),
			Evidence:    []model.GitEvidenceLink{},
		}
		commits = append(commits, commit)
	}
	completeness := model.ExactGitEvidence()
	if overflow {
		completeness = model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonChangeRequestOverflow)
	}
	return commits, completeness, metadata, nil
}

func mustReadCommitField(fields map[string]openapi.FieldSelector, item any, name string) string {
	selector, ok := fields[name]
	if !ok {
		return ""
	}
	value, err := openapi.EvalSelector(item, selector)
	if err != nil {
		return ""
	}
	return value
}

// --- pagination ------------------------------------------------------------

// fetchPages executes a list operation across pages. Limits come from the
// profile first and the independent safety caps always win; hitting a cap
// keeps the data and reports overflow instead of pretending exactness.
func (p *OpenAPIProvider) fetchPages(
	ctx context.Context, operation Operation, profileOperation *openapi.Operation,
	repository, number string, maximumItems int64,
) ([]any, ResultMetadata, bool, error) {
	pagination := profileOperation.Pagination
	maxPages := int64(safetyMaximumPages)
	if profileOperation.Pagination.Mode == openapi.PaginationNone {
		maxPages = 1
	} else if p.profile.Limits.MaximumPages > 0 && p.profile.Limits.MaximumPages < safetyMaximumPages {
		maxPages = p.profile.Limits.MaximumPages
	}

	items := []any{}
	metadata := ResultMetadata{Assessment: model.ExactGitEvidence()}
	overflow := false
	query := url.Values{}
	page := 1
	if pagination.Mode == openapi.PaginationPageNumber || pagination.Mode == openapi.PaginationLinkHeader && pagination.PageParameter != "" {
		if pagination.PageParameter != "" {
			query.Set(pagination.PageParameter, strconv.Itoa(page))
		}
	}
	if pagination.PerPageParameter != "" {
		query.Set(pagination.PerPageParameter, "100")
	}

	for pageCount := int64(1); ; pageCount++ {
		requestURL, err := p.operationURL(profileOperation, repository, number, query)
		if err != nil {
			return nil, metadata, overflow, providerError(operation, ErrorInvalidResponse, err)
		}
		result, err := p.client.Do(ctx, operation, "GET", requestURL, nil)
		metadata.PageCount += result.Metadata.PageCount
		metadata.BytesRead += result.Metadata.BytesRead
		if err != nil {
			return nil, metadata, overflow, err
		}
		document, err := decodeOpenAPIJSON(result.Body)
		if err != nil {
			return nil, metadata, overflow, providerError(operation, ErrorInvalidResponse, err)
		}
		pageItems, err := openapi.EvalItems(document, profileOperation.Response.ItemsPointer)
		if err != nil {
			return nil, metadata, overflow, providerError(operation, ErrorInvalidResponse, err)
		}
		items = append(items, pageItems...)
		if int64(len(items)) >= maximumItems {
			return items[:maximumItems], metadata, true, nil
		}
		if pageCount >= maxPages {
			return items, metadata, len(pageItems) > 0 && pagination.Mode != openapi.PaginationNone, nil
		}

		switch pagination.Mode {
		case openapi.PaginationNone:
			return items, metadata, false, nil
		case openapi.PaginationPageNumber:
			if len(pageItems) == 0 {
				return items, metadata, false, nil
			}
			if pagination.TotalPagesPointer != "" {
				if totalValue, err := openapi.EvalPointer(document, pagination.TotalPagesPointer); err == nil {
					if total, ok := totalValue.(float64); ok && pageCount >= int64(total) {
						return items, metadata, false, nil
					}
				}
			}
			page++
			query.Set(pagination.PageParameter, strconv.Itoa(page))
		case openapi.PaginationLinkHeader:
			next := parseLinkNext(result.Link)
			if next == "" {
				return items, metadata, false, nil
			}
			// The next URL must stay on approved origins; the HTTP client
			// enforces that on the request itself.
			parsed, err := url.Parse(next)
			if err != nil {
				return items, metadata, overflow, nil
			}
			query = parsed.Query()
		case openapi.PaginationCursorBody:
			nextValue, err := openapi.EvalPointer(document, pagination.NextCursorPointer)
			if err != nil {
				return items, metadata, false, nil
			}
			next, _ := nextValue.(string)
			if next == "" {
				return items, metadata, false, nil
			}
			query.Set(pagination.CursorParameter, next)
		case openapi.PaginationCursorHeader:
			next := result.Headers.Get(pagination.NextCursorHeader)
			if next == "" {
				return items, metadata, false, nil
			}
			query.Set(pagination.CursorParameter, next)
		default:
			return items, metadata, false, nil
		}
	}
}

// parseLinkNext extracts the rel="next" target from an RFC 8288 Link header.
func parseLinkNext(header string) string {
	for _, part := range strings.Split(header, ",") {
		segments := strings.Split(strings.TrimSpace(part), ";")
		if len(segments) < 2 || !strings.Contains(segments[1], `rel="next"`) {
			continue
		}
		target := strings.TrimSpace(segments[0])
		if strings.HasPrefix(target, "<") && strings.HasSuffix(target, ">") {
			return target[1 : len(target)-1]
		}
	}
	return ""
}

// --- unified diff parsing ------------------------------------------------

// fetchDiffText downloads the standalone diff document.
func (p *OpenAPIProvider) fetchDiffText(ctx context.Context, repository, number string) ([]byte, ResultMetadata, error) {
	operation := p.profile.Operations.GetDiff
	requestURL, err := p.operationURL(operation, repository, number, nil)
	if err != nil {
		return nil, ResultMetadata{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, err)
	}
	result, err := p.client.Do(ctx, OperationGetSnapshot, "GET", requestURL, nil)
	if err != nil {
		return nil, result.Metadata, err
	}
	return result.Body, result.Metadata, nil
}

// filesFromUnifiedDiff parses a bounded unified diff into file rows with
// per-file patch contents. Mode changes surface from git headers when
// present; otherwise modes stay unavailable per-file but the diff itself is
// authoritative for the file set and patches.
func filesFromUnifiedDiff(provider model.ChangeProviderKind, repository, number string, raw []byte) ([]model.GitFileChange, []SnapshotContent, error) {
	text := string(raw)
	// Split only on lines that START with the marker: a patch body line can
	// legitimately contain "diff --git " as content (e.g. a stored fixture).
	lines := strings.Split(text, "\n")
	sectionStarts := []int{}
	for i, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			sectionStarts = append(sectionStarts, i)
		}
	}
	if len(sectionStarts) == 0 {
		return nil, nil, fmt.Errorf("diff response is not a unified diff")
	}
	files := []model.GitFileChange{}
	contents := []SnapshotContent{}
	for sectionIndex, start := range sectionStarts {
		end := len(lines)
		if sectionIndex+1 < len(sectionStarts) {
			end = sectionStarts[sectionIndex+1]
		}
		body := strings.Join(lines[start:end], "\n")
		header := lines[start]
		oldPath, newPath, ok := parseDiffGitHeader(header)
		if !ok || !safeProviderPath(newPath) || !safeProviderPath(oldPath) {
			return nil, nil, fmt.Errorf("diff section %d carries an invalid path", sectionIndex)
		}
		status := model.GitFileModified
		switch {
		case strings.Contains(body, "\nnew file mode"):
			status = model.GitFileAdded
		case strings.Contains(body, "\ndeleted file mode"):
			status = model.GitFileDeleted
		case strings.Contains(body, "\nrename from"):
			status = model.GitFileRenamed
		}
		oldMode, newMode := parseDiffModes(body)
		fileKey := providerFileKey(provider, "diff:"+repository+":"+number, oldPath, newPath)
		ordinal := len(files)
		files = append(files, model.GitFileChange{
			Ordinal: ordinal, Key: fileKey,
			Layer: model.GitFileLayerHosted, DisplayPath: newPath, OldDisplayPath: oldPath,
			PathEncoding: model.GitPathUTF8, Status: status,
			OldMode: oldMode, NewMode: newMode,
			StatusAssessment: model.ExactGitEvidence(),
			PatchAssessment:  model.ExactGitEvidence(),
			Evidence:         []model.GitEvidenceLink{},
		})
		contents = append(contents, SnapshotContent{FileKey: fileKey, Purpose: SnapshotContentPatch, Content: []byte(body)})
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("diff response contains no file sections")
	}
	return files, contents, nil
}

// parseDiffGitHeader extracts the a/ and b/ paths from a "diff --git" line.
func parseDiffGitHeader(line string) (oldPath, newPath string, ok bool) {
	rest := strings.TrimPrefix(line, "diff --git ")
	if rest == line {
		return "", "", false
	}
	parts := strings.SplitN(rest, " b/", 2)
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "a/") {
		return "", "", false
	}
	return strings.TrimPrefix(parts[0], "a/"), parts[1], true
}

func parseDiffModes(body string) (oldMode, newMode string) {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "old mode ") {
			oldMode = strings.TrimPrefix(line, "old mode ")
		}
		if strings.HasPrefix(line, "new mode ") {
			newMode = strings.TrimPrefix(line, "new mode ")
		}
		if strings.HasPrefix(line, "@@") {
			break
		}
	}
	return oldMode, newMode
}
