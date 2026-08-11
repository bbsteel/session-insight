package changehost

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

const (
	gitLabPageSize = 100
	gitLabMaxPages = 100
)

type GitLabProvider struct {
	host   HostIdentity
	client *HTTPClient
}

func NewGitLabProvider(host HostIdentity, client *HTTPClient) (*GitLabProvider, error) {
	if client == nil || host.Provider != model.ChangeProviderGitLab ||
		!sameHostIdentity(host, client.approved.Identity()) || host.DisplayOrigin != publicGitLabOrigin {
		return nil, ErrProviderContract
	}
	provider := &GitLabProvider{host: host, client: client}
	if errs := ValidateProvider(provider); len(errs) != 0 {
		return nil, errs
	}
	return provider, nil
}

func (p *GitLabProvider) Kind() model.ChangeProviderKind { return model.ChangeProviderGitLab }
func (p *GitLabProvider) Host() HostIdentity             { return p.host }
func (p *GitLabProvider) Capabilities() ProviderCapabilities {
	operations := make(map[CapabilityID]CapabilityDeclaration, len(CapabilityIDs()))
	for _, id := range CapabilityIDs() {
		operations[id] = CapabilityDeclaration{State: CapabilitySupported}
	}
	return ProviderCapabilities{
		Operations: operations, HostModes: []HostMode{HostModePublicSaaS},
		AuthenticationModes: []AuthenticationMode{AuthAnonymous, AuthTokenEnvironment, AuthOSKeyring, AuthProviderCLI},
		Limits:              ProviderLimits{MaximumPages: gitLabMaxPages, MaximumResponseBytes: maximumResponseBytes, ReportsOverflow: true},
	}
}

func (p *GitLabProvider) ParseReference(raw string) (model.ChangeRequestReference, bool) {
	return (GitLabParser{}).ParseReference(raw)
}

func (p *GitLabProvider) ParseRemote(raw string) (model.HostedRepositoryReference, bool) {
	return (GitLabParser{}).ParseRemote(raw)
}

func (p *GitLabProvider) ResolveRepository(ctx context.Context, ref model.HostedRepositoryReference) (RepositoryResult, error) {
	if ref.Provider != p.Kind() || ref.DisplayOrigin != p.host.DisplayOrigin {
		return RepositoryResult{}, providerError(OperationResolveRepository, ErrorInvalidResponse, nil)
	}
	repository, metadata, err := p.fetchProject(ctx, OperationResolveRepository, url.PathEscape(ref.Slug))
	if err != nil {
		return RepositoryResult{}, err
	}
	return RepositoryResult{Repository: repository, Metadata: metadata}, nil
}

func (p *GitLabProvider) Resolve(ctx context.Context, ref model.ChangeRequestReference) (ResolveResult, error) {
	if ref.Provider != p.Kind() || ref.DisplayOrigin != p.host.DisplayOrigin {
		return ResolveResult{}, providerError(OperationResolveChange, ErrorInvalidResponse, nil)
	}
	repository, repositoryMetadata, err := p.fetchProject(ctx, OperationResolveChange, url.PathEscape(ref.TargetRepositorySlug))
	if err != nil {
		return ResolveResult{}, err
	}
	iid, err := strconv.ParseInt(ref.DisplayNumber, 10, 64)
	if err != nil || iid < 1 {
		return ResolveResult{}, providerError(OperationResolveChange, ErrorInvalidResponse, err)
	}
	mergeRequest, metadata, _, err := p.fetchMergeRequest(ctx, OperationResolveChange, repository, iid)
	if err != nil {
		return ResolveResult{}, err
	}
	var source *model.HostedRepositoryIdentity
	var sourceMetadata ResultMetadata
	if repositoryID, ok := gitLabProjectID(repository); ok && mergeRequest.SourceProjectID == repositoryID {
		copy := repository
		source = &copy
	} else if mergeRequest.SourceProjectID > 0 {
		resolved, resolvedMetadata, fetchErr := p.fetchProject(
			ctx, OperationResolveChange, strconv.FormatInt(mergeRequest.SourceProjectID, 10),
		)
		if fetchErr != nil {
			return ResolveResult{}, fetchErr
		}
		source = &resolved
		sourceMetadata = resolvedMetadata
	}
	summary, err := p.summary(mergeRequest, repository, source)
	if err != nil {
		return ResolveResult{}, providerError(OperationResolveChange, ErrorInvalidResponse, err)
	}
	return ResolveResult{Change: summary, Metadata: aggregateResultMetadata(repositoryMetadata, metadata, sourceMetadata)}, nil
}

func (p *GitLabProvider) DiscoverForHead(
	ctx context.Context,
	sourceRepo, targetRepo model.HostedRepositoryIdentity,
	branch, headSHA string,
) (DiscoveryResult, error) {
	if !p.validRepository(sourceRepo) || !p.validRepository(targetRepo) || branch == "" {
		return DiscoveryResult{}, providerError(OperationDiscoverHead, ErrorInvalidResponse, nil)
	}
	targetID, ok := gitLabProjectID(targetRepo)
	if !ok {
		return DiscoveryResult{}, providerError(OperationDiscoverHead, ErrorInvalidResponse, nil)
	}
	sourceID, ok := gitLabProjectID(sourceRepo)
	if !ok {
		return DiscoveryResult{}, providerError(OperationDiscoverHead, ErrorInvalidResponse, nil)
	}
	path := fmt.Sprintf("/api/v4/projects/%d/merge_requests?scope=all&state=all&source_branch=%s", targetID, url.QueryEscape(branch))
	items, metadata, err := fetchGitLabPages[gitLabMergeRequest](ctx, p.client, OperationDiscoverHead, p.host.DisplayOrigin, path)
	if err != nil {
		return DiscoveryResult{}, err
	}
	candidates := make([]DiscoveryCandidate, 0)
	for _, item := range items {
		if item.SourceProjectID != sourceID || item.TargetProjectID != targetID || headSHA != "" && item.SHA != headSHA {
			continue
		}
		summary, err := p.summary(item, targetRepo, &sourceRepo)
		if err != nil {
			return DiscoveryResult{}, providerError(OperationDiscoverHead, ErrorInvalidResponse, err)
		}
		assessment := model.ExactGitEvidence()
		match := DiscoveryHeadSHA
		if headSHA == "" {
			assessment = model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonChangeLinkAmbiguous)
			match = DiscoveryBranch
		}
		candidates = append(candidates, DiscoveryCandidate{Change: summary, Match: match, Assessment: assessment})
	}
	metadata.ItemCount = len(candidates)
	return DiscoveryResult{Candidates: candidates, Metadata: metadata}, nil
}

func (p *GitLabProvider) DiscoverForCommit(
	ctx context.Context,
	repository model.HostedRepositoryIdentity,
	sha string,
) (DiscoveryResult, error) {
	if !p.validRepository(repository) || !isProviderSHA(sha) {
		return DiscoveryResult{}, providerError(OperationDiscoverCommit, ErrorInvalidResponse, nil)
	}
	projectID, _ := gitLabProjectID(repository)
	path := fmt.Sprintf("/api/v4/projects/%d/repository/commits/%s/merge_requests", projectID, url.PathEscape(sha))
	items, metadata, err := fetchGitLabPages[gitLabMergeRequest](ctx, p.client, OperationDiscoverCommit, p.host.DisplayOrigin, path)
	if err != nil {
		return DiscoveryResult{}, err
	}
	candidates := make([]DiscoveryCandidate, 0, len(items))
	for _, item := range items {
		if item.TargetProjectID != projectID {
			continue
		}
		summary, err := p.summary(item, repository, nil)
		if err != nil {
			return DiscoveryResult{}, providerError(OperationDiscoverCommit, ErrorInvalidResponse, err)
		}
		candidates = append(candidates, DiscoveryCandidate{
			Change: summary, Match: DiscoveryCommitMembership, Assessment: model.ExactGitEvidence(),
		})
	}
	metadata.ItemCount = len(candidates)
	return DiscoveryResult{Candidates: candidates, Metadata: metadata}, nil
}

func (p *GitLabProvider) GetSnapshot(
	ctx context.Context,
	identity model.ChangeRequestIdentity,
	requestedVersion model.ContentVersionKey,
) (SnapshotResult, error) {
	if validation := model.ValidateChangeRequestIdentity(identity); !validation.OK() || identity.Provider != p.Kind() || identity.HostID != p.host.Key {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, nil)
	}
	projectID, ok := gitLabProjectID(*identity.TargetRepository)
	if !ok {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, nil)
	}
	iid, ok := parseProviderNumber(identity.ProviderObjectID, "merge_request:")
	if !ok {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, nil)
	}
	targetRepository, targetMetadata, err := p.fetchProject(ctx, OperationGetSnapshot, strconv.FormatInt(projectID, 10))
	if err != nil {
		return SnapshotResult{}, err
	}
	if targetRepository.ImmutableID != identity.TargetRepository.ImmutableID {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, nil)
	}
	initial, initialMetadata, etag, err := p.fetchMergeRequest(ctx, OperationGetSnapshot, targetRepository, iid)
	if err != nil {
		return SnapshotResult{}, err
	}
	if initial.TargetProjectID != projectID || initial.DiffRefs.BaseSHA == "" || initial.DiffRefs.StartSHA == "" || initial.DiffRefs.HeadSHA == "" {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorPartial, nil)
	}
	var sourceRepository *model.HostedRepositoryIdentity
	var sourceMetadata ResultMetadata
	if initial.SourceProjectID == projectID {
		copy := targetRepository
		sourceRepository = &copy
	} else if initial.SourceProjectID > 0 {
		resolved, metadata, fetchErr := p.fetchProject(ctx, OperationGetSnapshot, strconv.FormatInt(initial.SourceProjectID, 10))
		sourceMetadata = metadata
		err = fetchErr
		if err != nil {
			return SnapshotResult{}, err
		}
		sourceRepository = &resolved
	}
	diffPath := fmt.Sprintf("/api/v4/projects/%d/merge_requests/%d/diffs?unidiff=true", projectID, iid)
	diffs, diffMetadata, err := fetchGitLabPages[gitLabDiff](ctx, p.client, OperationGetSnapshot, p.host.DisplayOrigin, diffPath)
	if err != nil {
		return SnapshotResult{}, err
	}
	commitPath := fmt.Sprintf("/api/v4/projects/%d/merge_requests/%d/commits", projectID, iid)
	commits, commitMetadata, err := fetchGitLabPages[gitLabCommit](ctx, p.client, OperationGetSnapshot, p.host.DisplayOrigin, commitPath)
	if err != nil {
		return SnapshotResult{}, err
	}
	files, contents, completeness, manifest, err := gitLabFiles(identity.ProviderObjectID, diffs, commits)
	if err != nil {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, err)
	}
	content := model.ChangeRequestContentVersion{
		BaseRefSHA: initial.DiffRefs.StartSHA, DiffBaseSHA: initial.DiffRefs.BaseSHA,
		HeadSHA: initial.DiffRefs.HeadSHA, FileManifestDigest: manifest,
	}
	content.Key = providerContentKey(p.Kind(), identity.ProviderObjectID, "", content.BaseRefSHA, content.DiffBaseSHA, content.HeadSHA, manifest)
	if requestedVersion != "" && requestedVersion != content.Key {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorNotFound, nil)
	}
	finalState, finalMetadata, _, err := p.fetchMergeRequest(ctx, OperationGetSnapshot, targetRepository, iid)
	if err != nil {
		return SnapshotResult{}, err
	}
	if initial.SHA != finalState.SHA || initial.DiffRefs != finalState.DiffRefs {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorCaptureRaced, nil)
	}
	providerCommits, err := gitLabCommits(commits)
	if err != nil {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, err)
	}
	summary, err := p.summary(initial, targetRepository, sourceRepository)
	if err != nil {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, err)
	}
	now := time.Now().UTC()
	snapshotIdentity := identity
	snapshotIdentity.TargetRepository = &targetRepository
	snapshot := model.ChangeRequestSnapshot{
		SnapshotID: providerOpaqueKey("gitlab-snapshot", string(content.Key)),
		Identity:   snapshotIdentity, Content: content,
		MetadataRevision: providerMetadataRevision(
			initial.UpdatedAt, initial.State, initial.Title, strconv.FormatBool(initial.Draft),
			initial.MergeCommitSHA, initial.SquashCommitSHA,
		),
		Kind: summary.Kind, DisplayNumber: summary.DisplayNumber,
		LifecycleState: summary.LifecycleState, Draft: summary.Draft,
		Title: summary.Title, WebURL: summary.WebURL,
		SourceRepository: sourceRepository, SourceRef: summary.SourceRef, TargetRef: summary.TargetRef,
		MergeCommitSHA: summary.MergeCommitSHA, SquashCommitSHA: summary.SquashCommitSHA,
		Files: files, Commits: providerCommits, Completeness: completeness,
		ETag: etag, FetchedAt: now,
	}
	metadata := aggregateResultMetadata(
		targetMetadata, sourceMetadata, initialMetadata, diffMetadata, commitMetadata, finalMetadata,
	)
	metadata.ItemCount = len(files) + len(providerCommits)
	if completeness.Patches.State != model.GitEvidenceExact || completeness.Modes.State != model.GitEvidenceExact {
		metadata.Assessment = completeness.Patches
		if metadata.Assessment.State == model.GitEvidenceExact {
			metadata.Assessment = completeness.Modes
		}
	}
	result := SnapshotResult{Snapshot: snapshot, Contents: contents, Metadata: metadata}
	if errs := ValidateSnapshotResult(result); len(errs) != 0 {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, errs)
	}
	return result, nil
}

func (p *GitLabProvider) validRepository(repository model.HostedRepositoryIdentity) bool {
	return repository.HostID == p.host.Key && safeProviderText(repository.Slug, 4096)
}

func (p *GitLabProvider) fetchProject(ctx context.Context, operation Operation, encodedID string) (model.HostedRepositoryIdentity, ResultMetadata, error) {
	result, err := p.client.Do(ctx, operation, http.MethodGet,
		p.host.DisplayOrigin+"/api/v4/projects/"+encodedID, nil)
	if err != nil {
		return model.HostedRepositoryIdentity{}, result.Metadata, err
	}
	var project gitLabProject
	if err := decodeProviderJSON(result.Body, &project); err != nil || project.ID < 1 || !safeProviderText(project.PathWithNamespace, 4096) {
		return model.HostedRepositoryIdentity{}, result.Metadata, providerError(operation, ErrorInvalidResponse, err)
	}
	result.Metadata.ItemCount = 1
	return model.HostedRepositoryIdentity{
		HostID: p.host.Key, ImmutableID: fmt.Sprintf("project:%d", project.ID), Slug: project.PathWithNamespace,
	}, result.Metadata, nil
}

func (p *GitLabProvider) fetchMergeRequest(
	ctx context.Context,
	operation Operation,
	repository model.HostedRepositoryIdentity,
	iid int64,
) (gitLabMergeRequest, ResultMetadata, string, error) {
	projectID, ok := gitLabProjectID(repository)
	if !ok || iid < 1 {
		return gitLabMergeRequest{}, ResultMetadata{}, "", providerError(operation, ErrorInvalidResponse, nil)
	}
	requestURL := fmt.Sprintf("%s/api/v4/projects/%d/merge_requests/%d", p.host.DisplayOrigin, projectID, iid)
	result, err := p.client.Do(ctx, operation, http.MethodGet, requestURL, nil)
	if err != nil {
		return gitLabMergeRequest{}, result.Metadata, "", err
	}
	var mergeRequest gitLabMergeRequest
	if err := decodeProviderJSON(result.Body, &mergeRequest); err != nil {
		return gitLabMergeRequest{}, result.Metadata, "", providerError(operation, ErrorInvalidResponse, err)
	}
	result.Metadata.ItemCount = 1
	return mergeRequest, result.Metadata, result.ETag, nil
}

func (p *GitLabProvider) summary(
	mergeRequest gitLabMergeRequest,
	target model.HostedRepositoryIdentity,
	source *model.HostedRepositoryIdentity,
) (model.ChangeRequestSummary, error) {
	if mergeRequest.IID < 1 || mergeRequest.TargetProjectID == 0 || !safeProviderText(mergeRequest.Title, 8192) ||
		!safeProviderText(mergeRequest.WebURL, 8192) || !safeProviderText(mergeRequest.SourceBranch, 4096) ||
		!safeProviderText(mergeRequest.TargetBranch, 4096) || !providerURLMatchesOrigin(mergeRequest.WebURL, p.host.DisplayOrigin) ||
		mergeRequest.SHA != "" && !isProviderSHA(mergeRequest.SHA) ||
		mergeRequest.MergeCommitSHA != "" && !isProviderSHA(mergeRequest.MergeCommitSHA) ||
		mergeRequest.SquashCommitSHA != "" && !isProviderSHA(mergeRequest.SquashCommitSHA) {
		return model.ChangeRequestSummary{}, fmt.Errorf("GitLab merge request fields are incomplete")
	}
	identity := model.ChangeRequestIdentity{
		Provider: p.Kind(), HostID: p.host.Key, TargetRepository: &target,
		ProviderObjectID: fmt.Sprintf("merge_request:%d", mergeRequest.IID),
	}
	content := model.ChangeRequestContentVersion{
		NativeVersion: mergeRequest.SHA, HeadSHA: mergeRequest.SHA,
	}
	content.Key = providerContentKey(p.Kind(), identity.ProviderObjectID, mergeRequest.SHA, "", "", mergeRequest.SHA, "")
	summary := model.ChangeRequestSummary{
		Identity: identity, Content: content, Kind: model.ChangeRequestMergeRequest,
		DisplayNumber:  strconv.FormatInt(mergeRequest.IID, 10),
		LifecycleState: lifecycleFromProvider(mergeRequest.State, mergeRequest.State == "merged"),
		Draft:          mergeRequest.Draft, Title: mergeRequest.Title, WebURL: mergeRequest.WebURL,
		SourceRepository: source, TargetRef: mergeRequest.TargetBranch,
		MergeCommitSHA: mergeRequest.MergeCommitSHA, SquashCommitSHA: mergeRequest.SquashCommitSHA,
		Completeness: metadataOnlyCompleteness(),
	}
	if source != nil {
		summary.SourceRef = mergeRequest.SourceBranch
	}
	return summary, nil
}

func fetchGitLabPages[T any](
	ctx context.Context,
	client *HTTPClient,
	operation Operation,
	origin, path string,
) ([]T, ResultMetadata, error) {
	items := make([]T, 0)
	metadata := ResultMetadata{Assessment: model.ExactGitEvidence()}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	for page := 1; page <= gitLabMaxPages; page++ {
		requestURL := fmt.Sprintf("%s%s%sper_page=%d&page=%d", origin, path, separator, gitLabPageSize, page)
		result, err := client.Do(ctx, operation, http.MethodGet, requestURL, nil)
		metadata = aggregateResultMetadata(metadata, result.Metadata)
		if err != nil {
			return nil, metadata, err
		}
		var current []T
		if err := decodeProviderJSON(result.Body, &current); err != nil {
			return nil, metadata, providerError(operation, ErrorInvalidResponse, err)
		}
		items = append(items, current...)
		if len(current) < gitLabPageSize {
			metadata.ItemCount = len(items)
			return items, metadata, nil
		}
	}
	return nil, metadata, providerError(operation, ErrorOverflow, nil)
}

func gitLabFiles(
	objectID string,
	diffs []gitLabDiff,
	commits []gitLabCommit,
) ([]model.GitFileChange, []SnapshotContent, model.ChangeRequestCompleteness, string, error) {
	sort.SliceStable(diffs, func(i, j int) bool {
		if diffs[i].NewPath != diffs[j].NewPath {
			return diffs[i].NewPath < diffs[j].NewPath
		}
		return diffs[i].OldPath < diffs[j].OldPath
	})
	files := make([]model.GitFileChange, 0, len(diffs))
	contents := make([]SnapshotContent, 0, len(diffs))
	patchAssessment := model.ExactGitEvidence()
	modeAssessment := model.ExactGitEvidence()
	manifestParts := make([]string, 0, len(diffs)*6+len(commits))
	for ordinal, diff := range diffs {
		if !safeProviderPath(diff.NewPath) || !safeProviderPath(diff.OldPath) {
			return nil, nil, model.ChangeRequestCompleteness{}, "", fmt.Errorf("unsafe GitLab diff path")
		}
		status := model.GitFileModified
		switch {
		case diff.NewFile:
			status = model.GitFileAdded
		case diff.DeletedFile:
			status = model.GitFileDeleted
		case diff.RenamedFile:
			status = model.GitFileRenamed
		}
		filePatch := model.ExactGitEvidence()
		if diff.TooLarge || diff.Collapsed || diff.Diff == "" {
			reason := model.ReasonChangeRequestPartial
			if diff.TooLarge || diff.Collapsed {
				reason = model.ReasonChangeRequestOverflow
			}
			filePatch = model.NonExactGitEvidence(model.GitEvidenceUnavailable, reason)
			patchAssessment = filePatch
		}
		if diff.AMode == "" || diff.BMode == "" {
			modeAssessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestPartial)
		}
		fileKey := providerFileKey(model.ChangeProviderGitLab, objectID, diff.OldPath, diff.NewPath)
		oldDisplayPath := ""
		if status == model.GitFileRenamed {
			oldDisplayPath = diff.OldPath
		}
		files = append(files, model.GitFileChange{
			Ordinal: ordinal, Key: fileKey, Layer: model.GitFileLayerHosted,
			DisplayPath: diff.NewPath, OldDisplayPath: oldDisplayPath,
			PathEncoding: model.GitPathUTF8, Status: status,
			OldMode: diff.AMode, NewMode: diff.BMode,
			StatusAssessment: model.ExactGitEvidence(), PatchAssessment: filePatch,
			Evidence: []model.GitEvidenceLink{},
		})
		if filePatch.State == model.GitEvidenceExact {
			contents = append(contents, SnapshotContent{
				FileKey: fileKey, Purpose: SnapshotContentPatch, Content: []byte(diff.Diff),
			})
		}
		manifestParts = append(manifestParts, diff.OldPath, diff.NewPath, string(status), diff.AMode, diff.BMode, diff.Diff)
	}
	for _, commit := range commits {
		manifestParts = append(manifestParts, commit.ID)
	}
	manifestParts = append(manifestParts,
		string(patchAssessment.State), string(patchAssessment.ReasonCode),
		string(modeAssessment.State), string(modeAssessment.ReasonCode),
	)
	manifest := providerOpaqueKey("manifest", manifestParts...)
	completeness := model.ChangeRequestCompleteness{
		Metadata: model.ExactGitEvidence(), FileSet: model.ExactGitEvidence(),
		Patches: patchAssessment, Modes: modeAssessment, Commits: model.ExactGitEvidence(),
	}
	return files, contents, completeness, manifest, nil
}

func gitLabCommits(commits []gitLabCommit) ([]model.GitCandidateCommit, error) {
	result := make([]model.GitCandidateCommit, 0, len(commits))
	for ordinal, commit := range commits {
		if !isProviderSHA(commit.ID) || !safeProviderText(commit.Title, 8192) {
			return nil, fmt.Errorf("invalid GitLab commit")
		}
		result = append(result, model.GitCandidateCommit{
			Ordinal: ordinal, SHA: commit.ID, Subject: commit.Title, AuthorName: commit.AuthorName,
			AuthoredAt: parseProviderTime(commit.AuthoredDate), CommittedAt: parseProviderTime(commit.CommittedDate),
			Relation: model.GitCommitChangeMembership, Assessment: model.ExactGitEvidence(),
			Evidence: []model.GitEvidenceLink{},
		})
	}
	return result, nil
}

func gitLabProjectID(repository model.HostedRepositoryIdentity) (int64, bool) {
	if !strings.HasPrefix(repository.ImmutableID, "project:") {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(repository.ImmutableID, "project:"), 10, 64)
	return id, err == nil && id > 0
}

func parseProviderNumber(objectID, prefix string) (int64, bool) {
	if !strings.HasPrefix(objectID, prefix) {
		return 0, false
	}
	value, err := strconv.ParseInt(strings.TrimPrefix(objectID, prefix), 10, 64)
	return value, err == nil && value > 0
}

func isProviderSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

type gitLabProject struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
}

type gitLabDiffRefs struct {
	BaseSHA  string `json:"base_sha"`
	HeadSHA  string `json:"head_sha"`
	StartSHA string `json:"start_sha"`
}

type gitLabMergeRequest struct {
	ID              int64          `json:"id"`
	IID             int64          `json:"iid"`
	ProjectID       int64          `json:"project_id"`
	SourceProjectID int64          `json:"source_project_id"`
	TargetProjectID int64          `json:"target_project_id"`
	Title           string         `json:"title"`
	State           string         `json:"state"`
	Draft           bool           `json:"draft"`
	WebURL          string         `json:"web_url"`
	SHA             string         `json:"sha"`
	SourceBranch    string         `json:"source_branch"`
	TargetBranch    string         `json:"target_branch"`
	MergeCommitSHA  string         `json:"merge_commit_sha"`
	SquashCommitSHA string         `json:"squash_commit_sha"`
	UpdatedAt       string         `json:"updated_at"`
	DiffRefs        gitLabDiffRefs `json:"diff_refs"`
}

type gitLabDiff struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	AMode       string `json:"a_mode"`
	BMode       string `json:"b_mode"`
	Diff        string `json:"diff"`
	NewFile     bool   `json:"new_file"`
	RenamedFile bool   `json:"renamed_file"`
	DeletedFile bool   `json:"deleted_file"`
	Collapsed   bool   `json:"collapsed"`
	TooLarge    bool   `json:"too_large"`
}

type gitLabCommit struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	AuthorName    string `json:"author_name"`
	AuthoredDate  string `json:"authored_date"`
	CommittedDate string `json:"committed_date"`
}
