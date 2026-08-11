package changehost

import (
	"context"
	"errors"
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
	gitHubAPIOrigin      = "https://api.github.com"
	gitHubAPIVersion     = "2026-03-10"
	gitHubPageSize       = 100
	gitHubMaximumFiles   = 3000
	gitHubMaximumCommits = 250
)

type GitHubProvider struct {
	host   HostIdentity
	client *HTTPClient
}

func NewGitHubProvider(host HostIdentity, client *HTTPClient) (*GitHubProvider, error) {
	if client == nil || host.Provider != model.ChangeProviderGitHub ||
		!sameHostIdentity(host, client.approved.Identity()) || !sameHostIdentity(host, PublicGitHubHost()) {
		return nil, ErrProviderContract
	}
	provider := &GitHubProvider{host: host, client: client}
	if errs := ValidateProvider(provider); len(errs) != 0 {
		return nil, errs
	}
	return provider, nil
}

func (p *GitHubProvider) Kind() model.ChangeProviderKind { return model.ChangeProviderGitHub }
func (p *GitHubProvider) Host() HostIdentity             { return p.host }
func (p *GitHubProvider) Capabilities() ProviderCapabilities {
	operations := make(map[CapabilityID]CapabilityDeclaration, len(CapabilityIDs()))
	for _, id := range CapabilityIDs() {
		operations[id] = CapabilityDeclaration{State: CapabilitySupported}
	}
	return ProviderCapabilities{
		Operations: operations, HostModes: []HostMode{HostModePublicSaaS},
		AuthenticationModes: []AuthenticationMode{AuthAnonymous, AuthTokenEnvironment, AuthOSKeyring, AuthProviderCLI},
		Limits: ProviderLimits{
			MaximumFiles: gitHubMaximumFiles, MaximumCommits: gitHubMaximumCommits,
			MaximumPages: 31, MaximumResponseBytes: maximumResponseBytes, ReportsOverflow: true,
		},
	}
}

func (p *GitHubProvider) ParseReference(raw string) (model.ChangeRequestReference, bool) {
	return (GitHubParser{}).ParseReference(raw)
}

func (p *GitHubProvider) ParseRemote(raw string) (model.HostedRepositoryReference, bool) {
	return (GitHubParser{}).ParseRemote(raw)
}

func (p *GitHubProvider) ResolveRepository(ctx context.Context, ref model.HostedRepositoryReference) (RepositoryResult, error) {
	if ref.Provider != p.Kind() || ref.DisplayOrigin != p.host.DisplayOrigin {
		return RepositoryResult{}, providerError(OperationResolveRepository, ErrorInvalidResponse, nil)
	}
	repository, metadata, err := p.fetchRepository(ctx, OperationResolveRepository, ref.Slug)
	if err != nil {
		return RepositoryResult{}, err
	}
	return RepositoryResult{Repository: repository, Metadata: metadata}, nil
}

func (p *GitHubProvider) Resolve(ctx context.Context, ref model.ChangeRequestReference) (ResolveResult, error) {
	if ref.Provider != p.Kind() || ref.DisplayOrigin != p.host.DisplayOrigin {
		return ResolveResult{}, providerError(OperationResolveChange, ErrorInvalidResponse, nil)
	}
	repository, repositoryMetadata, err := p.fetchRepository(ctx, OperationResolveChange, ref.TargetRepositorySlug)
	if err != nil {
		return ResolveResult{}, err
	}
	number, err := strconv.ParseInt(ref.DisplayNumber, 10, 64)
	if err != nil || number < 1 {
		return ResolveResult{}, providerError(OperationResolveChange, ErrorInvalidResponse, err)
	}
	pull, pullMetadata, _, err := p.fetchPull(ctx, OperationResolveChange, repository, number, "application/vnd.github+json")
	if err != nil {
		return ResolveResult{}, err
	}
	summary, err := p.summary(pull, repository)
	if err != nil {
		return ResolveResult{}, providerError(OperationResolveChange, ErrorInvalidResponse, err)
	}
	return ResolveResult{Change: summary, Metadata: aggregateResultMetadata(repositoryMetadata, pullMetadata)}, nil
}

func (p *GitHubProvider) DiscoverForHead(
	ctx context.Context,
	sourceRepo, targetRepo model.HostedRepositoryIdentity,
	branch, headSHA string,
) (DiscoveryResult, error) {
	if !p.validRepository(sourceRepo) || !p.validRepository(targetRepo) || !safeProviderText(branch, 4096) {
		return DiscoveryResult{}, providerError(OperationDiscoverHead, ErrorInvalidResponse, nil)
	}
	owner := strings.Split(sourceRepo.Slug, "/")[0]
	path := fmt.Sprintf("/repos/%s/pulls?state=all&head=%s", escapedGitHubSlug(targetRepo.Slug), url.QueryEscape(owner+":"+branch))
	pulls, metadata, _, err := p.fetchPullPages(ctx, OperationDiscoverHead, path, 1000)
	if err != nil {
		return DiscoveryResult{}, err
	}
	candidates := make([]DiscoveryCandidate, 0)
	for _, pull := range pulls {
		if pull.Head.Repo == nil || githubRepositoryIdentity(p.host.Key, *pull.Head.Repo).ImmutableID != sourceRepo.ImmutableID ||
			headSHA != "" && pull.Head.SHA != headSHA {
			continue
		}
		summary, err := p.summary(pull, targetRepo)
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

func (p *GitHubProvider) DiscoverForCommit(
	ctx context.Context,
	repository model.HostedRepositoryIdentity,
	sha string,
) (DiscoveryResult, error) {
	if !p.validRepository(repository) || !isProviderSHA(sha) {
		return DiscoveryResult{}, providerError(OperationDiscoverCommit, ErrorInvalidResponse, nil)
	}
	path := fmt.Sprintf("/repos/%s/commits/%s/pulls", escapedGitHubSlug(repository.Slug), sha)
	pulls, metadata, _, err := p.fetchPullPages(ctx, OperationDiscoverCommit, path, 1000)
	if err != nil {
		return DiscoveryResult{}, err
	}
	candidates := make([]DiscoveryCandidate, 0, len(pulls))
	for _, pull := range pulls {
		if pull.Base.Repo == nil || githubRepositoryIdentity(p.host.Key, *pull.Base.Repo).ImmutableID != repository.ImmutableID {
			continue
		}
		summary, err := p.summary(pull, repository)
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

func (p *GitHubProvider) GetSnapshot(
	ctx context.Context,
	identity model.ChangeRequestIdentity,
	requestedVersion model.ContentVersionKey,
) (SnapshotResult, error) {
	if validation := model.ValidateChangeRequestIdentity(identity); !validation.OK() || identity.Provider != p.Kind() || identity.HostID != p.host.Key {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, nil)
	}
	number, ok := parseProviderNumber(identity.ProviderObjectID, "pull:")
	if !ok {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, nil)
	}
	target, targetMetadata, err := p.fetchRepositoryIdentity(ctx, OperationGetSnapshot, *identity.TargetRepository)
	if err != nil {
		return SnapshotResult{}, err
	}
	if target.ImmutableID != identity.TargetRepository.ImmutableID {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, nil)
	}
	initial, initialMetadata, etag, err := p.fetchPull(ctx, OperationGetSnapshot, target, number, "application/vnd.github+json")
	if err != nil {
		return SnapshotResult{}, err
	}
	if initial.Base.Repo == nil || githubRepositoryIdentity(p.host.Key, *initial.Base.Repo).ImmutableID != target.ImmutableID ||
		!isProviderSHA(initial.Base.SHA) || !isProviderSHA(initial.Head.SHA) {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, nil)
	}
	files, filesMetadata, fileOverflow, err := p.fetchFiles(ctx, target, number)
	if err != nil {
		return SnapshotResult{}, err
	}
	rawDiff, rawMetadata, rawAvailable, err := p.fetchRawDiff(ctx, target, number)
	if err != nil {
		return SnapshotResult{}, err
	}
	commits, commitsMetadata, commitOverflow, err := p.fetchCommits(ctx, target, number)
	if err != nil {
		return SnapshotResult{}, err
	}
	providerFiles, contents, completeness, manifest, err := githubFiles(identity.ProviderObjectID, files, rawDiff, rawAvailable, fileOverflow, commitOverflow, commits)
	if err != nil {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, err)
	}
	providerCommits, err := githubCommits(commits)
	if err != nil {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, err)
	}
	content := model.ChangeRequestContentVersion{
		BaseRefSHA: initial.Base.SHA, DiffBaseSHA: initial.Base.SHA,
		HeadSHA: initial.Head.SHA, FileManifestDigest: manifest,
	}
	content.Key = providerContentKey(p.Kind(), identity.ProviderObjectID, "", content.BaseRefSHA, content.DiffBaseSHA, content.HeadSHA, manifest)
	if requestedVersion != "" && requestedVersion != content.Key {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorNotFound, nil)
	}
	finalState, finalMetadata, _, err := p.fetchPull(ctx, OperationGetSnapshot, target, number, "application/vnd.github+json")
	if err != nil {
		return SnapshotResult{}, err
	}
	if initial.Base.SHA != finalState.Base.SHA || initial.Head.SHA != finalState.Head.SHA {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorCaptureRaced, nil)
	}
	summary, err := p.summary(initial, target)
	if err != nil {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, err)
	}
	now := time.Now().UTC()
	snapshotIdentity := identity
	snapshotIdentity.TargetRepository = &target
	snapshot := model.ChangeRequestSnapshot{
		SnapshotID: providerOpaqueKey("github-snapshot", string(content.Key)),
		Identity:   snapshotIdentity, Content: content,
		MetadataRevision: providerMetadataRevision(
			initial.UpdatedAt, initial.State, initial.Title, strconv.FormatBool(initial.Draft), initial.MergeCommitSHA,
		),
		Kind: summary.Kind, DisplayNumber: summary.DisplayNumber,
		LifecycleState: summary.LifecycleState, Draft: summary.Draft,
		Title: summary.Title, WebURL: summary.WebURL,
		SourceRepository: summary.SourceRepository, SourceRef: summary.SourceRef, TargetRef: summary.TargetRef,
		MergeCommitSHA: summary.MergeCommitSHA, SquashCommitSHA: summary.SquashCommitSHA,
		Files: providerFiles, Commits: providerCommits, Completeness: completeness,
		ETag: etag, FetchedAt: now,
	}
	metadata := aggregateResultMetadata(
		targetMetadata, initialMetadata, filesMetadata, rawMetadata, commitsMetadata, finalMetadata,
	)
	metadata.ItemCount = len(providerFiles) + len(providerCommits)
	for _, assessment := range []model.GitEvidenceAssessment{
		completeness.FileSet, completeness.Patches, completeness.Modes, completeness.Commits,
	} {
		if assessment.State != model.GitEvidenceExact {
			metadata.Assessment = assessment
			break
		}
	}
	result := SnapshotResult{Snapshot: snapshot, Contents: contents, Metadata: metadata}
	if errs := ValidateSnapshotResult(result); len(errs) != 0 {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, errs)
	}
	return result, nil
}

func (p *GitHubProvider) validRepository(repository model.HostedRepositoryIdentity) bool {
	return repository.HostID == p.host.Key && strings.Count(repository.Slug, "/") == 1 && safeProviderText(repository.ImmutableID, 4096)
}

func (p *GitHubProvider) fetchRepository(ctx context.Context, operation Operation, slug string) (model.HostedRepositoryIdentity, ResultMetadata, error) {
	if strings.Count(slug, "/") != 1 {
		return model.HostedRepositoryIdentity{}, ResultMetadata{}, providerError(operation, ErrorInvalidResponse, nil)
	}
	result, err := p.githubGET(ctx, operation, "/repos/"+escapedGitHubSlug(slug), "application/vnd.github+json")
	if err != nil {
		return model.HostedRepositoryIdentity{}, result.Metadata, err
	}
	var repository githubRepository
	if err := decodeProviderJSON(result.Body, &repository); err != nil {
		return model.HostedRepositoryIdentity{}, result.Metadata, providerError(operation, ErrorInvalidResponse, err)
	}
	identity, err := p.repositoryIdentity(repository)
	if err != nil {
		return model.HostedRepositoryIdentity{}, result.Metadata, providerError(operation, ErrorInvalidResponse, err)
	}
	result.Metadata.ItemCount = 1
	return identity, result.Metadata, nil
}

func (p *GitHubProvider) fetchRepositoryIdentity(
	ctx context.Context,
	operation Operation,
	identity model.HostedRepositoryIdentity,
) (model.HostedRepositoryIdentity, ResultMetadata, error) {
	if strings.HasPrefix(identity.ImmutableID, "repository:") {
		id, err := strconv.ParseInt(strings.TrimPrefix(identity.ImmutableID, "repository:"), 10, 64)
		if err != nil || id < 1 {
			return model.HostedRepositoryIdentity{}, ResultMetadata{}, providerError(operation, ErrorInvalidResponse, err)
		}
		result, err := p.githubGET(ctx, operation, fmt.Sprintf("/repositories/%d", id), "application/vnd.github+json")
		if err != nil {
			return model.HostedRepositoryIdentity{}, result.Metadata, err
		}
		var repository githubRepository
		if err := decodeProviderJSON(result.Body, &repository); err != nil {
			return model.HostedRepositoryIdentity{}, result.Metadata, providerError(operation, ErrorInvalidResponse, err)
		}
		resolved, err := p.repositoryIdentity(repository)
		if err != nil || resolved.ImmutableID != identity.ImmutableID {
			return model.HostedRepositoryIdentity{}, result.Metadata, providerError(operation, ErrorInvalidResponse, err)
		}
		result.Metadata.ItemCount = 1
		return resolved, result.Metadata, nil
	}
	return p.fetchRepository(ctx, operation, identity.Slug)
}

func (p *GitHubProvider) fetchPull(
	ctx context.Context,
	operation Operation,
	repository model.HostedRepositoryIdentity,
	number int64,
	accept string,
) (githubPull, ResultMetadata, string, error) {
	if !p.validRepository(repository) || number < 1 {
		return githubPull{}, ResultMetadata{}, "", providerError(operation, ErrorInvalidResponse, nil)
	}
	path := fmt.Sprintf("/repos/%s/pulls/%d", escapedGitHubSlug(repository.Slug), number)
	result, err := p.githubGET(ctx, operation, path, accept)
	if err != nil {
		return githubPull{}, result.Metadata, "", err
	}
	var pull githubPull
	if err := decodeProviderJSON(result.Body, &pull); err != nil {
		return githubPull{}, result.Metadata, "", providerError(operation, ErrorInvalidResponse, err)
	}
	result.Metadata.ItemCount = 1
	return pull, result.Metadata, result.ETag, nil
}

func (p *GitHubProvider) fetchPullPages(ctx context.Context, operation Operation, path string, maximum int) ([]githubPull, ResultMetadata, bool, error) {
	return fetchGitHubPages[githubPull](ctx, p, operation, path, maximum)
}

func (p *GitHubProvider) fetchFiles(ctx context.Context, repository model.HostedRepositoryIdentity, number int64) ([]githubFile, ResultMetadata, bool, error) {
	path := fmt.Sprintf("/repos/%s/pulls/%d/files", escapedGitHubSlug(repository.Slug), number)
	return fetchGitHubPages[githubFile](ctx, p, OperationGetSnapshot, path, gitHubMaximumFiles)
}

func (p *GitHubProvider) fetchCommits(ctx context.Context, repository model.HostedRepositoryIdentity, number int64) ([]githubCommit, ResultMetadata, bool, error) {
	path := fmt.Sprintf("/repos/%s/pulls/%d/commits", escapedGitHubSlug(repository.Slug), number)
	return fetchGitHubPages[githubCommit](ctx, p, OperationGetSnapshot, path, gitHubMaximumCommits)
}

func (p *GitHubProvider) fetchRawDiff(ctx context.Context, repository model.HostedRepositoryIdentity, number int64) ([]byte, ResultMetadata, bool, error) {
	path := fmt.Sprintf("/repos/%s/pulls/%d", escapedGitHubSlug(repository.Slug), number)
	result, err := p.githubGET(ctx, OperationGetSnapshot, path, "application/vnd.github.diff")
	if err == nil {
		return result.Body, result.Metadata, true, nil
	}
	var providerErr *Error
	if errors.As(err, &providerErr) && providerErr.Code == ErrorOverflow {
		result.Metadata.Assessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestOverflow)
		return nil, result.Metadata, false, nil
	}
	return nil, result.Metadata, false, err
}

func (p *GitHubProvider) githubGET(ctx context.Context, operation Operation, path, accept string) (HTTPResult, error) {
	headers := make(http.Header)
	headers.Set("Accept", accept)
	headers.Set("X-GitHub-Api-Version", gitHubAPIVersion)
	return p.client.Do(ctx, operation, http.MethodGet, gitHubAPIOrigin+path, headers)
}

func (p *GitHubProvider) repositoryIdentity(repository githubRepository) (model.HostedRepositoryIdentity, error) {
	identity := githubRepositoryIdentity(p.host.Key, repository)
	if !safeProviderText(identity.ImmutableID, 4096) || strings.Count(identity.Slug, "/") != 1 || !safeProviderPath(identity.Slug) {
		return model.HostedRepositoryIdentity{}, fmt.Errorf("invalid GitHub repository")
	}
	return identity, nil
}

func githubRepositoryIdentity(hostID string, repository githubRepository) model.HostedRepositoryIdentity {
	immutable := ""
	if repository.ID > 0 {
		immutable = fmt.Sprintf("repository:%d", repository.ID)
	} else {
		immutable = repository.NodeID
	}
	return model.HostedRepositoryIdentity{HostID: hostID, ImmutableID: immutable, Slug: repository.FullName}
}

func (p *GitHubProvider) summary(pull githubPull, target model.HostedRepositoryIdentity) (model.ChangeRequestSummary, error) {
	if pull.Number < 1 || !safeProviderText(pull.Title, 8192) || !safeProviderText(pull.HTMLURL, 8192) ||
		!providerURLMatchesOrigin(pull.HTMLURL, p.host.DisplayOrigin) || !safeProviderText(pull.Head.Ref, 4096) ||
		!safeProviderText(pull.Base.Ref, 4096) || !isProviderSHA(pull.Head.SHA) || !isProviderSHA(pull.Base.SHA) ||
		pull.Base.Repo == nil || githubRepositoryIdentity(p.host.Key, *pull.Base.Repo).ImmutableID != target.ImmutableID {
		return model.ChangeRequestSummary{}, fmt.Errorf("GitHub pull request fields are incomplete")
	}
	identity := model.ChangeRequestIdentity{
		Provider: p.Kind(), HostID: p.host.Key, TargetRepository: &target,
		ProviderObjectID: fmt.Sprintf("pull:%d", pull.Number),
	}
	content := model.ChangeRequestContentVersion{NativeVersion: pull.Head.SHA, HeadSHA: pull.Head.SHA}
	content.Key = providerContentKey(p.Kind(), identity.ProviderObjectID, pull.Head.SHA, "", "", pull.Head.SHA, "")
	var source *model.HostedRepositoryIdentity
	if pull.Head.Repo != nil {
		resolved, err := p.repositoryIdentity(*pull.Head.Repo)
		if err != nil {
			return model.ChangeRequestSummary{}, err
		}
		source = &resolved
	}
	mergeCommit := ""
	if (pull.Merged || pull.MergedAt != "") && pull.MergeCommitSHA != "" {
		if !isProviderSHA(pull.MergeCommitSHA) {
			return model.ChangeRequestSummary{}, fmt.Errorf("invalid GitHub merge SHA")
		}
		mergeCommit = pull.MergeCommitSHA
	}
	summary := model.ChangeRequestSummary{
		Identity: identity, Content: content, Kind: model.ChangeRequestPullRequest,
		DisplayNumber:  strconv.FormatInt(pull.Number, 10),
		LifecycleState: lifecycleFromProvider(pull.State, pull.Merged || pull.MergedAt != ""),
		Draft:          pull.Draft, Title: pull.Title, WebURL: pull.HTMLURL,
		SourceRepository: source, TargetRef: pull.Base.Ref, MergeCommitSHA: mergeCommit,
		Completeness: metadataOnlyCompleteness(),
	}
	if source != nil {
		summary.SourceRef = pull.Head.Ref
	}
	return summary, nil
}

func fetchGitHubPages[T any](
	ctx context.Context,
	provider *GitHubProvider,
	operation Operation,
	path string,
	maximum int,
) ([]T, ResultMetadata, bool, error) {
	items := make([]T, 0)
	metadata := ResultMetadata{Assessment: model.ExactGitEvidence()}
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	for page := 1; ; page++ {
		requestPath := fmt.Sprintf("%s%sper_page=%d&page=%d", path, separator, gitHubPageSize, page)
		result, err := provider.githubGET(ctx, operation, requestPath, "application/vnd.github+json")
		metadata = aggregateResultMetadata(metadata, result.Metadata)
		if err != nil {
			return nil, metadata, false, err
		}
		var current []T
		if err := decodeProviderJSON(result.Body, &current); err != nil {
			return nil, metadata, false, providerError(operation, ErrorInvalidResponse, err)
		}
		remaining := maximum - len(items)
		if len(current) > remaining {
			current = current[:remaining]
		}
		items = append(items, current...)
		if len(current) < gitHubPageSize && len(items) < maximum {
			metadata.ItemCount = len(items)
			return items, metadata, false, nil
		}
		if len(items) >= maximum {
			metadata.ItemCount = len(items)
			metadata.Assessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestOverflow)
			return items, metadata, true, nil
		}
	}
}

func githubFiles(
	objectID string,
	providerFiles []githubFile,
	rawDiff []byte,
	rawAvailable, fileOverflow, commitOverflow bool,
	commits []githubCommit,
) ([]model.GitFileChange, []SnapshotContent, model.ChangeRequestCompleteness, string, error) {
	sections := []githubDiffSection{}
	if rawAvailable {
		parsed, err := parseGitHubDiff(rawDiff)
		if err == nil {
			sections = parsed
		} else {
			rawAvailable = false
		}
	}
	sectionMap := githubDiffSectionsByPath(sections)
	sort.SliceStable(providerFiles, func(i, j int) bool {
		if providerFiles[i].Filename != providerFiles[j].Filename {
			return providerFiles[i].Filename < providerFiles[j].Filename
		}
		return providerFiles[i].PreviousFilename < providerFiles[j].PreviousFilename
	})
	files := make([]model.GitFileChange, 0, len(providerFiles))
	contents := make([]SnapshotContent, 0, len(providerFiles))
	patchAssessment := model.ExactGitEvidence()
	modeAssessment := model.ExactGitEvidence()
	fileSetAssessment := model.ExactGitEvidence()
	commitAssessment := model.ExactGitEvidence()
	manifestParts := make([]string, 0, len(providerFiles)*8+len(commits)+4)
	if fileOverflow {
		fileSetAssessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestOverflow)
		patchAssessment = fileSetAssessment
		modeAssessment = fileSetAssessment
	}
	if commitOverflow {
		commitAssessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestOverflow)
	}
	for ordinal, captured := range providerFiles {
		if !safeProviderPath(captured.Filename) || captured.PreviousFilename != "" && !safeProviderPath(captured.PreviousFilename) {
			return nil, nil, model.ChangeRequestCompleteness{}, "", fmt.Errorf("unsafe GitHub file path")
		}
		status, err := githubFileStatus(captured.Status)
		if err != nil {
			return nil, nil, model.ChangeRequestCompleteness{}, "", err
		}
		oldPath, newPath := captured.Filename, captured.Filename
		matchOld, matchNew := captured.Filename, captured.Filename
		oldDisplayPath := ""
		switch status {
		case model.GitFileAdded:
			oldPath, matchOld = "", ""
		case model.GitFileDeleted:
			newPath, matchNew = "", ""
		case model.GitFileRenamed, model.GitFileCopied:
			oldPath, matchOld = captured.PreviousFilename, captured.PreviousFilename
			oldDisplayPath = captured.PreviousFilename
		}
		section, hasSection := sectionMap[matchOld+"\x00"+matchNew]
		patch := []byte(captured.Patch)
		if hasSection {
			patch = section.Patch
		}
		filePatch := model.ExactGitEvidence()
		if len(patch) == 0 {
			filePatch = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestPartial)
			if patchAssessment.State == model.GitEvidenceExact {
				patchAssessment = filePatch
			}
		}
		oldMode, newMode := section.OldMode, section.NewMode
		if !hasSection || oldMode == "" || newMode == "" {
			if modeAssessment.State == model.GitEvidenceExact {
				modeAssessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestPartial)
			}
		}
		fileKey := providerFileKey(model.ChangeProviderGitHub, objectID, oldPath, newPath)
		additions, deletions := captured.Additions, captured.Deletions
		files = append(files, model.GitFileChange{
			Ordinal: ordinal, Key: fileKey, Layer: model.GitFileLayerHosted,
			DisplayPath: captured.Filename, OldDisplayPath: oldDisplayPath,
			PathEncoding: model.GitPathUTF8, Status: status,
			OldMode: oldMode, NewMode: newMode,
			Additions: &additions, Deletions: &deletions,
			StatusAssessment: model.ExactGitEvidence(), PatchAssessment: filePatch,
			Evidence: []model.GitEvidenceLink{},
		})
		if filePatch.State == model.GitEvidenceExact {
			contents = append(contents, SnapshotContent{FileKey: fileKey, Purpose: SnapshotContentPatch, Content: patch})
		}
		manifestParts = append(manifestParts,
			oldPath, newPath, string(status), oldMode, newMode,
			strconv.Itoa(captured.Additions), strconv.Itoa(captured.Deletions), string(patch),
		)
	}
	for _, commit := range commits {
		manifestParts = append(manifestParts, commit.SHA)
	}
	manifestParts = append(manifestParts,
		string(fileSetAssessment.State), string(fileSetAssessment.ReasonCode),
		string(patchAssessment.State), string(patchAssessment.ReasonCode),
		string(modeAssessment.State), string(modeAssessment.ReasonCode),
		string(commitAssessment.State), string(commitAssessment.ReasonCode),
	)
	manifest := providerOpaqueKey("manifest", manifestParts...)
	return files, contents, model.ChangeRequestCompleteness{
		Metadata: model.ExactGitEvidence(), FileSet: fileSetAssessment,
		Patches: patchAssessment, Modes: modeAssessment, Commits: commitAssessment,
	}, manifest, nil
}

func githubCommits(commits []githubCommit) ([]model.GitCandidateCommit, error) {
	result := make([]model.GitCandidateCommit, 0, len(commits))
	for ordinal, commit := range commits {
		subject := strings.SplitN(commit.Commit.Message, "\n", 2)[0]
		if !isProviderSHA(commit.SHA) || !safeProviderText(subject, 8192) {
			return nil, fmt.Errorf("invalid GitHub commit")
		}
		result = append(result, model.GitCandidateCommit{
			Ordinal: ordinal, SHA: commit.SHA, Subject: subject, AuthorName: commit.Commit.Author.Name,
			AuthoredAt: parseProviderTime(commit.Commit.Author.Date), CommittedAt: parseProviderTime(commit.Commit.Committer.Date),
			Relation: model.GitCommitChangeMembership, Assessment: model.ExactGitEvidence(),
			Evidence: []model.GitEvidenceLink{},
		})
	}
	return result, nil
}

func githubFileStatus(status string) (model.GitFileStatus, error) {
	switch status {
	case "added":
		return model.GitFileAdded, nil
	case "removed":
		return model.GitFileDeleted, nil
	case "modified", "changed":
		return model.GitFileModified, nil
	case "renamed":
		return model.GitFileRenamed, nil
	case "copied":
		return model.GitFileCopied, nil
	default:
		return "", fmt.Errorf("unknown GitHub file status %q", status)
	}
}

func escapedGitHubSlug(slug string) string {
	parts := strings.Split(slug, "/")
	if len(parts) != 2 {
		return ""
	}
	return url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
}

type githubRepository struct {
	ID       int64  `json:"id"`
	NodeID   string `json:"node_id"`
	FullName string `json:"full_name"`
}

type githubPullBranch struct {
	Ref  string            `json:"ref"`
	SHA  string            `json:"sha"`
	Repo *githubRepository `json:"repo"`
}

type githubPull struct {
	Number         int64            `json:"number"`
	Title          string           `json:"title"`
	State          string           `json:"state"`
	Draft          bool             `json:"draft"`
	Merged         bool             `json:"merged"`
	MergedAt       string           `json:"merged_at"`
	HTMLURL        string           `json:"html_url"`
	UpdatedAt      string           `json:"updated_at"`
	MergeCommitSHA string           `json:"merge_commit_sha"`
	Head           githubPullBranch `json:"head"`
	Base           githubPullBranch `json:"base"`
}

type githubFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Patch            string `json:"patch"`
}

type githubCommitIdentity struct {
	Name string `json:"name"`
	Date string `json:"date"`
}

type githubCommitDetail struct {
	Message   string               `json:"message"`
	Author    githubCommitIdentity `json:"author"`
	Committer githubCommitIdentity `json:"committer"`
}

type githubCommit struct {
	SHA    string             `json:"sha"`
	Commit githubCommitDetail `json:"commit"`
}
