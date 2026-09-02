package changehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/changehost/openapi"
	"github.com/bbsteel/session-insight/internal/model"
)

// openapi_provider.go: the profile-driven runtime provider (design §11.2).
// It executes an already-activated, immutable ProviderProfile — no interface
// discovery and no field guessing happens on this path.

// DriftReporter records one schema-drift observation for a profile. It is
// invoked at most once per provider operation, with the stable failure code;
// the wiring layer marks the profile degraded.
type DriftReporter func(failureCode string)

// OpenAPIProvider is the generic execution engine for verified OpenAPI host
// profiles.
type OpenAPIProvider struct {
	host    HostIdentity
	client  *HTTPClient
	profile openapi.Profile
	drift   DriftReporter
	// operationOutputs holds documents fetched earlier in the current
	// top-level provider call so operation.* parameter bindings can resolve.
	// It is reset at the start of every call and never shared across calls.
	operationOutputs map[openapi.OperationID]any
}

// NewOpenAPIProvider binds one validated profile to its approved host and
// origin-scoped client. The profile must belong to this host and pass the
// full structural contract.
func NewOpenAPIProvider(host HostIdentity, client *HTTPClient, profile openapi.Profile, drift DriftReporter) (*OpenAPIProvider, error) {
	if client == nil || host.Provider != model.ChangeProviderOpenAPI ||
		profile.HostID != host.Key || profile.Adapter != openapi.AdapterKind ||
		!sameHostIdentity(host, client.approved.Identity()) {
		return nil, ErrProviderContract
	}
	if issues := openapi.ValidateProfile(profile); !issues.OK() {
		return nil, fmt.Errorf("%w: %s", ErrProviderContract, issues.Error())
	}
	provider := &OpenAPIProvider{host: host, client: client, profile: profile, drift: drift}
	if errs := ValidateProvider(provider); len(errs) != 0 {
		return nil, errs
	}
	return provider, nil
}

func (p *OpenAPIProvider) Kind() model.ChangeProviderKind { return model.ChangeProviderOpenAPI }
func (p *OpenAPIProvider) Host() HostIdentity             { return p.host }

// ProfileID identifies the immutable mapping revision this provider executes.
func (p *OpenAPIProvider) ProfileID() string { return p.profile.ProfileID }

// ProfileRevision identifies the mapping revision for snapshot provenance.
func (p *OpenAPIProvider) ProfileRevision() int { return p.profile.ProfileRevision }

// Capabilities projects the profile's runtime declaration — the single
// source of truth for what this host can do.
func (p *OpenAPIProvider) Capabilities() ProviderCapabilities {
	return OpenAPIProfileCapabilities(p.profile)
}

// OpenAPIProfileCapabilities projects a profile's capability declaration
// without constructing a provider (status DTOs use it for openapi hosts).
func OpenAPIProfileCapabilities(profile openapi.Profile) ProviderCapabilities {
	operations := map[CapabilityID]CapabilityDeclaration{}
	supported := CapabilityDeclaration{State: CapabilitySupported}
	unsupportedEndpoint := CapabilityDeclaration{State: CapabilityUnsupported, ReasonCode: CapabilityReasonEndpointUnsupported}
	unsupportedProvider := CapabilityDeclaration{State: CapabilityUnsupported, ReasonCode: CapabilityReasonProviderUnsupported}

	operations[CapabilityParseReference] = supported
	operations[CapabilityParseRemote] = unsupportedEndpoint
	if profile.Operations.ResolveRepository != nil {
		operations[CapabilityResolveRepository] = supported
	} else {
		operations[CapabilityResolveRepository] = unsupportedEndpoint
	}
	operations[CapabilityResolveChange] = supported
	operations[CapabilityDiscoverHead] = unsupportedProvider
	operations[CapabilityDiscoverCommit] = unsupportedProvider
	operations[CapabilitySnapshotMetadata] = supported
	operations[CapabilitySnapshotFileSet] = capabilityFor(profile.Capabilities.FileSet, unsupportedEndpoint)
	operations[CapabilitySnapshotPatches] = capabilityFor(profile.Capabilities.Patches, unsupportedEndpoint)
	operations[CapabilitySnapshotModes] = capabilityFor(profile.Capabilities.Modes, unsupportedEndpoint)
	operations[CapabilitySnapshotCommits] = capabilityFor(profile.Capabilities.Commits, unsupportedEndpoint)

	return ProviderCapabilities{
		Operations:          operations,
		HostModes:           []HostMode{HostModeSelfHosted},
		AuthenticationModes: []AuthenticationMode{AuthTokenEnvironment, AuthOSKeyring},
		Limits: ProviderLimits{
			MaximumFiles:         profile.Limits.MaximumFiles,
			MaximumCommits:       profile.Limits.MaximumCommits,
			MaximumPages:         profile.Limits.MaximumPages,
			MaximumResponseBytes: profile.Limits.MaximumResponseBytes,
			ReportsOverflow:      true,
		},
	}
}

// OpenAPIUnsupportedCapabilities is the explicit all-unsupported projection
// for an openapi host with no usable active profile — never an empty map.
func OpenAPIUnsupportedCapabilities() ProviderCapabilities {
	operations := map[CapabilityID]CapabilityDeclaration{}
	for _, id := range CapabilityIDs() {
		operations[id] = CapabilityDeclaration{State: CapabilityUnsupported, ReasonCode: CapabilityReasonEndpointUnsupported}
	}
	return ProviderCapabilities{
		Operations:          operations,
		HostModes:           []HostMode{HostModeSelfHosted},
		AuthenticationModes: []AuthenticationMode{AuthTokenEnvironment, AuthOSKeyring},
	}
}

func capabilityFor(state openapi.CapabilityState, unsupported CapabilityDeclaration) CapabilityDeclaration {
	if state == openapi.CapabilitySupported {
		return CapabilityDeclaration{State: CapabilitySupported}
	}
	return unsupported
}

func (p *OpenAPIProvider) ParseReference(raw string) (model.ChangeRequestReference, bool) {
	return openapi.MatchReferenceTemplate(p.profile.Reference, p.host.Key, raw)
}

// ParseRemote is unsupported: the profile contract has no git-remote
// template. Capability declarations say the same.
func (p *OpenAPIProvider) ParseRemote(string) (model.HostedRepositoryReference, bool) {
	return model.HostedRepositoryReference{}, false
}

// Resolve fetches and validates the change detail for one reference.
func (p *OpenAPIProvider) Resolve(ctx context.Context, ref model.ChangeRequestReference) (ResolveResult, error) {
	if ref.Provider != p.Kind() || ref.HostID != p.host.Key || ref.DisplayOrigin != p.host.DisplayOrigin {
		return ResolveResult{}, providerError(OperationResolveChange, ErrorInvalidResponse, nil)
	}
	p.operationOutputs = map[openapi.OperationID]any{}
	detail, metadata, err := p.fetchChangeDetail(ctx, OperationResolveChange, ref.TargetRepositorySlug, ref.DisplayNumber)
	if err != nil {
		return ResolveResult{}, err
	}
	p.operationOutputs[openapi.OperationResolveChange] = detail
	summary, err := p.summaryFromDetail(detail, ref)
	if err != nil {
		p.reportDrift()
		return ResolveResult{}, err
	}
	return ResolveResult{Change: summary, Metadata: metadata}, nil
}

// ResolveRepository executes the optional resolve_repository operation.
func (p *OpenAPIProvider) ResolveRepository(ctx context.Context, ref model.HostedRepositoryReference) (RepositoryResult, error) {
	operation := p.profile.Operations.ResolveRepository
	if operation == nil {
		return RepositoryResult{}, providerError(OperationResolveRepository, ErrorUnsupported, nil)
	}
	if ref.Provider != p.Kind() || ref.HostID != "" && ref.HostID != p.host.Key || ref.DisplayOrigin != p.host.DisplayOrigin {
		return RepositoryResult{}, providerError(OperationResolveRepository, ErrorInvalidResponse, nil)
	}
	document, metadata, err := p.executeObjectOperation(ctx, OperationResolveRepository, operation, ref.Slug, "")
	if err != nil {
		return RepositoryResult{}, err
	}
	repository, err := p.repositoryFromDocument(document, ref.Slug)
	if err != nil {
		p.reportDrift()
		return RepositoryResult{}, err
	}
	return RepositoryResult{Repository: repository, Metadata: metadata}, nil
}

func (p *OpenAPIProvider) DiscoverForHead(context.Context, model.HostedRepositoryIdentity, model.HostedRepositoryIdentity, string, string) (DiscoveryResult, error) {
	return DiscoveryResult{}, providerError(OperationDiscoverHead, ErrorUnsupported, nil)
}

func (p *OpenAPIProvider) DiscoverForCommit(context.Context, model.HostedRepositoryIdentity, string) (DiscoveryResult, error) {
	return DiscoveryResult{}, providerError(OperationDiscoverCommit, ErrorUnsupported, nil)
}

// GetSnapshot captures detail, files, commits, and patches, then re-reads the
// detail to prove the content anchor stayed stable during capture.
func (p *OpenAPIProvider) GetSnapshot(
	ctx context.Context,
	identity model.ChangeRequestIdentity,
	requestedVersion model.ContentVersionKey,
) (SnapshotResult, error) {
	if validation := model.ValidateChangeRequestIdentity(identity); !validation.OK() ||
		identity.Provider != p.Kind() || identity.HostID != p.host.Key || identity.TargetRepository == nil {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, nil)
	}
	number, ok := openAPIDisplayNumber(identity.ProviderObjectID)
	if !ok {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, nil)
	}
	repositorySlug := identity.TargetRepository.Slug
	metadataItems := []ResultMetadata{}

	p.operationOutputs = map[openapi.OperationID]any{}
	initial, initialMetadata, err := p.fetchChangeDetail(ctx, OperationGetSnapshot, repositorySlug, number)
	if err != nil {
		return SnapshotResult{}, err
	}
	p.operationOutputs[openapi.OperationResolveChange] = initial
	metadataItems = append(metadataItems, initialMetadata)
	reference := model.ChangeRequestReference{
		Provider: p.Kind(), HostID: p.host.Key, DisplayOrigin: p.host.DisplayOrigin,
		TargetRepositorySlug: repositorySlug, DisplayNumber: number,
	}
	normalizedPath, ok := openapi.ExpandReferenceParameters(
		p.profile.Reference.PathTemplate, p.profile.Reference.RepositoryParameter,
		p.profile.Reference.NumberParameter, repositorySlug, number)
	if !ok {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, nil)
	}
	reference.NormalizedURL = p.profile.Reference.Origin + normalizedPath
	summary, err := p.summaryFromDetail(initial, reference)
	if err != nil {
		p.reportDrift()
		return SnapshotResult{}, err
	}
	anchor, err := p.contentAnchor(initial)
	if err != nil {
		p.reportDrift()
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, err)
	}

	files, contents, fileManifest, fileSet, patches, modes, filesMetadata, err := p.collectFiles(ctx, repositorySlug, number)
	if err != nil {
		p.reportDriftFor(err)
		return SnapshotResult{}, err
	}
	metadataItems = append(metadataItems, filesMetadata)
	commits, commitCompleteness, commitsMetadata, err := p.collectCommits(ctx, repositorySlug, number)
	if err != nil {
		p.reportDriftFor(err)
		return SnapshotResult{}, err
	}
	metadataItems = append(metadataItems, commitsMetadata)

	content := model.ChangeRequestContentVersion{
		NativeVersion: anchor.nativeVersion, HeadSHA: anchor.headSHA,
		BaseRefSHA: anchor.baseSHA, DiffBaseSHA: anchor.diffBaseSHA,
		FileManifestDigest: fileManifest,
	}
	// The content version contract requires either a native version or the
	// full base/diff-base/head/manifest quadruple. A head-sha anchor is the
	// platform's native content revision, so it mints the native version.
	if content.NativeVersion == "" && anchor.headSHA != "" {
		content.NativeVersion = anchor.headSHA
	}
	content.Key = providerContentKey(p.Kind(), identity.ProviderObjectID, content.NativeVersion, content.BaseRefSHA, content.DiffBaseSHA, content.HeadSHA, fileManifest)
	if requestedVersion != "" && requestedVersion != content.Key {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorNotFound, nil)
	}

	final, finalMetadata, err := p.fetchChangeDetail(ctx, OperationGetSnapshot, repositorySlug, number)
	if err != nil {
		return SnapshotResult{}, err
	}
	metadataItems = append(metadataItems, finalMetadata)
	finalAnchor, err := p.contentAnchor(final)
	if err != nil {
		p.reportDrift()
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, err)
	}
	if finalAnchor != anchor {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorCaptureRaced, nil)
	}

	completeness := model.ChangeRequestCompleteness{
		Metadata: model.ExactGitEvidence(),
		FileSet:  fileSet, Patches: patches, Modes: modes, Commits: commitCompleteness,
	}
	now := time.Now().UTC()
	snapshot := model.ChangeRequestSnapshot{
		SnapshotID: providerOpaqueKey("openapi-snapshot", string(content.Key)),
		Identity:   identity, Content: content,
		MetadataRevision: providerMetadataRevision(summary.Title, string(summary.LifecycleState), anchor.nativeVersion+anchor.headSHA),
		Kind:             summary.Kind, DisplayNumber: summary.DisplayNumber,
		LifecycleState: summary.LifecycleState, Draft: summary.Draft,
		Title: summary.Title, WebURL: summary.WebURL,
		SourceRepository: summary.SourceRepository, SourceRef: summary.SourceRef, TargetRef: summary.TargetRef,
		MergeCommitSHA: summary.MergeCommitSHA, SquashCommitSHA: summary.SquashCommitSHA,
		Files: files, Commits: commits, Completeness: completeness, FetchedAt: now,
	}
	metadata := aggregateResultMetadata(metadataItems...)
	metadata.ItemCount = len(files) + len(commits)
	result := SnapshotResult{Snapshot: snapshot, Contents: contents, Metadata: metadata}
	if errs := ValidateSnapshotResult(result); len(errs) != 0 {
		return SnapshotResult{}, providerError(OperationGetSnapshot, ErrorInvalidResponse, errs)
	}
	return result, nil
}

func (p *OpenAPIProvider) reportDrift() {
	if p.drift != nil {
		p.drift(string(openapi.IssueSchemaDrift))
	}
}

// reportDriftFor degrades the profile only when the failure proves a
// response-shape problem (missing field, type change, malformed body,
// unmappable value). Transport, authentication, rate-limit, and not-found
// failures are transient or environmental — they fail the current operation
// but must never degrade the profile mapping.
func (p *OpenAPIProvider) reportDriftFor(err error) {
	var selectorErr *openapi.SelectorError
	if errors.As(err, &selectorErr) {
		p.reportDrift()
		return
	}
	var providerErr *Error
	if errors.As(err, &providerErr) {
		if providerErr.Code == ErrorInvalidResponse {
			p.reportDrift()
		}
		return
	}
	// Non-structured failures here are shape/assembly errors (invalid path,
	// unmappable status, anchor loss) — drift class by construction.
	p.reportDrift()
}

// openAPIDisplayNumber recovers the display locator embedded in the provider
// object ID by Resolve (plain numeric, or the composite form when the
// platform's object id differs from the display number).
func openAPIDisplayNumber(providerObjectID string) (string, bool) {
	if providerObjectID == "" {
		return "", false
	}
	if strings.HasPrefix(providerObjectID, "obj-") {
		if idx := strings.LastIndex(providerObjectID, "-num-"); idx >= 0 {
			number := providerObjectID[idx+len("-num-"):]
			return number, number != ""
		}
		return "", false
	}
	return providerObjectID, true
}

// fetchChangeDetail executes resolve_change and applies its item pointer.
func (p *OpenAPIProvider) fetchChangeDetail(ctx context.Context, operation Operation, repository, number string) (any, ResultMetadata, error) {
	document, metadata, err := p.executeObjectOperation(ctx, operation, p.profile.Operations.ResolveChange, repository, number)
	return document, metadata, err
}

// executeObjectOperation runs one single-object profile operation.
func (p *OpenAPIProvider) executeObjectOperation(ctx context.Context, operation Operation, profileOperation *openapi.Operation, repository, number string) (any, ResultMetadata, error) {
	if profileOperation == nil {
		return nil, ResultMetadata{}, providerError(operation, ErrorUnsupported, nil)
	}
	requestURL, err := p.operationURL(profileOperation, repository, number, nil)
	if err != nil {
		return nil, ResultMetadata{}, providerError(operation, ErrorInvalidResponse, err)
	}
	result, err := p.client.DoWithProfileHeaders(ctx, operation, profileOperation.Method, requestURL, nil, profileOperation.Headers)
	if err != nil {
		return nil, result.Metadata, err
	}
	document, err := decodeOpenAPIJSON(result.Body)
	if err != nil {
		return nil, result.Metadata, providerError(operation, ErrorInvalidResponse, err)
	}
	if profileOperation.Response.ItemPointer != "" {
		document, err = openapi.EvalPointer(document, profileOperation.Response.ItemPointer)
		if err != nil {
			return nil, result.Metadata, providerError(operation, ErrorInvalidResponse, err)
		}
	}
	result.Metadata.ItemCount = 1
	return document, result.Metadata, nil
}

// operationURL builds a bounded URL from the operation template. Query
// parameters come only from literal bindings and the paginator; no caller
// input reaches the URL raw. operation.* bindings resolve from documents
// fetched earlier in the same provider call.
func (p *OpenAPIProvider) operationURL(operation *openapi.Operation, repository, number string, query url.Values) (string, error) {
	resolveOutput := func(operationID, field string) (string, bool) {
		document, ok := p.operationOutputs[openapi.OperationID(operationID)]
		if !ok {
			return "", false
		}
		sourceOperation := p.profile.Operations.ForID(openapi.OperationID(operationID))
		if sourceOperation == nil {
			return "", false
		}
		selector, ok := sourceOperation.Response.Fields[field]
		if !ok {
			return "", false
		}
		value, err := openapi.EvalSelector(document, selector)
		if err != nil {
			return "", false
		}
		return value, true
	}
	expanded, ok := openapi.ExpandOperationPath(operation.PathTemplate, operation.Parameters, repository, number, resolveOutput)
	if !ok {
		return "", errors.New("operation path template could not be expanded")
	}
	values := url.Values{}
	for key, value := range query {
		values[key] = append([]string(nil), value...)
	}
	for name, binding := range operation.Parameters {
		if literal, ok := strings.CutPrefix(binding, "literal:"); ok {
			values.Set(name, literal)
		}
	}
	requestURL := operation.Origin + expanded
	if encoded := values.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}
	return requestURL, nil
}

func decodeOpenAPIJSON(body []byte) (any, error) {
	var document any
	if err := json.Unmarshal(body, &document); err != nil {
		return nil, err
	}
	return document, nil
}

// contentAnchor reads the profile-declared stable content anchor from a
// detail document. A missing or malformed anchor is a drift signal and
// blocks publication.
type openAPIContentAnchor struct {
	headSHA       string
	baseSHA       string
	diffBaseSHA   string
	nativeVersion string
}

func (p *OpenAPIProvider) contentAnchor(document any) (openAPIContentAnchor, error) {
	anchor := openAPIContentAnchor{}
	fields := p.profile.Operations.ResolveChange.Response.Fields
	read := func(name string) string {
		selector, ok := fields[name]
		if !ok {
			return ""
		}
		value, err := openapi.EvalSelector(document, selector)
		if err != nil {
			return ""
		}
		return value
	}
	switch p.profile.Capabilities.ContentAnchor {
	case "head_sha":
		anchor.headSHA = read("head_sha")
		if anchor.headSHA == "" {
			return anchor, errors.New("content anchor head_sha missing from detail response")
		}
	case "native_version":
		anchor.nativeVersion = read("native_version")
		if anchor.nativeVersion == "" {
			return anchor, errors.New("content anchor native_version missing from detail response")
		}
		if len(anchor.nativeVersion) > 128 {
			anchor.nativeVersion = "sha256:" + sha256Hex(anchor.nativeVersion)
		}
	case "diff_version":
		// The diff payload is unbounded; persist its digest as the native
		// version so storage and capture-race comparison stay constant-size.
		anchor.nativeVersion = "sha256:" + sha256Hex(read("diff_text"))
		if anchor.nativeVersion == "sha256:"+sha256Hex("") {
			return anchor, errors.New("content anchor diff_version missing from detail response")
		}
	default:
		return anchor, errors.New("profile declares no stable content anchor")
	}
	anchor.baseSHA = read("base_sha")
	anchor.diffBaseSHA = read("diff_base_sha")
	return anchor, nil
}

// OpenAPIProfileParser parses change URLs for one activated profile. It is
// registered with the Registry as a host-bound parser; matching fails closed
// to other profiles or the generic parser.
type OpenAPIProfileParser struct {
	Profile openapi.Profile
}

func (p OpenAPIProfileParser) Kind() model.ChangeProviderKind { return model.ChangeProviderOpenAPI }

func (p OpenAPIProfileParser) ParseReference(raw string) (model.ChangeRequestReference, bool) {
	return openapi.MatchReferenceTemplate(p.Profile.Reference, p.Profile.HostID, raw)
}

// ParseRemote stays unsupported, matching the provider capability
// declaration.
func (p OpenAPIProfileParser) ParseRemote(string) (model.HostedRepositoryReference, bool) {
	return model.HostedRepositoryReference{}, false
}
