package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/gitevidence"
	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/render"
)

type gitEvidenceRuntime struct {
	resolver   *gitevidence.Resolver
	capturer   *gitevidence.Capturer
	discoverer *gitevidence.CandidateCommitDiscoverer
}

func newGitEvidenceRuntime() (*gitEvidenceRuntime, error) {
	runner, err := gitevidence.NewRunner(gitevidence.DefaultConfig())
	if err != nil {
		return nil, err
	}
	resolver, err := gitevidence.NewResolver(runner)
	if err != nil {
		return nil, err
	}
	capturer, err := gitevidence.NewCapturer(runner, gitevidence.CaptureConfig{Limits: gitevidence.DefaultCaptureLimits()})
	if err != nil {
		return nil, err
	}
	discoverer, err := gitevidence.NewCandidateCommitDiscoverer(runner, gitevidence.DefaultCandidateCommitLimits())
	if err != nil {
		return nil, err
	}
	return &gitEvidenceRuntime{resolver: resolver, capturer: capturer, discoverer: discoverer}, nil
}

func (ix *Indexer) indexGitEvidence(
	ctx context.Context,
	snapshotReader reader.AuthoritativeIndexSnapshotReader,
	session model.Session,
	envelope *model.IndexSnapshotEnvelope,
) error {
	if ix.git == nil || snapshotReader == nil || envelope == nil || envelope.OriginGit == nil {
		return nil
	}
	path := envelope.OriginGit.WorktreePath
	if path.Assessment.State != model.GitEvidenceExact || path.Value == "" {
		return nil
	}
	resolution, err := ix.git.resolver.Resolve(ctx, path.Value)
	if err != nil || resolution.Repository == nil || resolution.Assessment.State != model.GitEvidenceExact {
		// A missing or moved historical worktree must not fail ordinary Session
		// indexing or cause today's repository state to be attributed to history.
		return nil
	}
	repository := resolution.Repository
	entryKey := sessionRepositoryEntryKey(session.AgentType, session.ID, repository.WorktreeID)
	now := time.Now().UTC()
	revision := session.UpdatedAt.UnixNano()
	if revision < 1 {
		revision = 1
	}

	evidence := model.SessionGitEvidence{
		RootAgentType: session.AgentType, RootSessionID: session.ID,
		RepositoryEntryKey: entryKey, Revision: revision,
		Assessment: model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonBaselineNotCaptured),
		Repository: sessionRepositoryBinding(repository, entryKey, envelope.OriginGit), Origin: envelope.OriginGit,
		Files: []model.GitFileChange{}, CandidateCommits: []model.GitCandidateCommit{},
		ChangeRequests: []model.SessionChangeRequestLink{}, Authority: model.GitAuthorityNone,
		GeneratedAt: now,
	}
	storedEnvelope, stored, err := ix.db.SessionGitEvidenceEnvelope(session.AgentType, session.ID)
	if err != nil {
		return err
	}
	storedEntry := false
	storedSourceRevision := ""
	if stored {
		for _, candidate := range storedEnvelope.Repositories {
			if candidate.RepositoryEntryKey != entryKey {
				continue
			}
			evidence = candidate
			storedEntry = true
			storedSourceRevision = gitOriginSourceRevision(candidate.Origin)
			evidence.Revision = revision
			evidence.Repository = sessionRepositoryBinding(repository, entryKey, envelope.OriginGit)
			evidence.Origin = envelope.OriginGit
			evidence.GeneratedAt = now
			break
		}
	}
	if storedEntry && evidence.Authority != model.GitAuthorityHostedChange && storedSourceRevision != envelope.SourceRevision {
		// A newer transcript revision invalidates the previous local final before
		// any potentially slow filesystem capture begins. Keep the immutable
		// baseline, but never serve the old file set under the new revision.
		evidence.Final = nil
		evidence.Files = []model.GitFileChange{}
		evidence.CandidateCommits = []model.GitCandidateCommit{}
		evidence.Authority = model.GitAuthorityNone
		evidence.AuthoritySelection = nil
		evidence.Stale = false
		evidence.Assessment = model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonBaselineNotCaptured)
		if evidence.Baseline != nil {
			evidence.Assessment = model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonFinalNotCaptured)
		}
	}

	// Create or refresh the private binding before immutable snapshots attach
	// to it. Existing hosted authority and its exact fixed files are preserved.
	if err := ix.db.ReplaceSessionGitEvidence(evidence); err != nil {
		return err
	}
	bindingID, ok, err := ix.db.SessionRepositoryBindingID(session.AgentType, session.ID, entryKey)
	if err != nil || !ok {
		if err != nil {
			return err
		}
		return errors.New("local Git binding was not persisted")
	}

	baselineRecord, hasBaseline, err := ix.db.LatestLocalGitSnapshot(bindingID, model.GitSnapshotBaseline)
	if err != nil {
		return err
	}
	if hasBaseline {
		evidence.Baseline = snapshotSummaryCopy(baselineRecord.Summary)
	}

	exactLive := envelope.Finalization.State == model.SessionLive &&
		envelope.Finalization.Assessment.Precision == model.SessionEvidenceExact
	exactFinal := envelope.Finalization.State == model.SessionFinalized &&
		envelope.Finalization.Assessment.Precision == model.SessionEvidenceExact

	var captureKind model.GitSnapshotKind
	switch {
	case !hasBaseline && exactLive:
		captureKind = model.GitSnapshotBaseline
	case hasBaseline && exactLive:
		captureKind = model.GitSnapshotCheckpoint
	case hasBaseline && exactFinal:
		captureKind = model.GitSnapshotFinal
	}
	if captureKind == "" {
		if evidence.Authority != model.GitAuthorityHostedChange {
			evidence.Assessment = model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonBaselineNotCaptured)
			if hasBaseline {
				evidence.Assessment = model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonFinalNotCaptured)
			}
			evidence.Authority = model.GitAuthorityNone
			evidence.AuthoritySelection = nil
			evidence.Files = []model.GitFileChange{}
			evidence.CandidateCommits = []model.GitCandidateCommit{}
		}
		evidence.Provisional = exactLive
		return ix.db.ReplaceSessionGitEvidence(evidence)
	}

	// Reuse an already published final for the same authoritative source
	// revision. Checkpoints are intentionally refreshed on each source update.
	if captureKind == model.GitSnapshotFinal {
		finalRecord, found, readErr := ix.db.LatestLocalGitSnapshot(bindingID, model.GitSnapshotFinal)
		if readErr != nil {
			return readErr
		}
		if found && finalRecord.Summary.SourceRevision == envelope.SourceRevision {
			return ix.deriveLocalGitEvidence(ctx, session, envelope, evidence, repository, baselineRecord, finalRecord)
		}
	}

	captured, captureErr := ix.git.capturer.Capture(ctx, *repository, gitevidence.CaptureRequest{
		Kind: captureKind, SourceRevision: envelope.SourceRevision,
	})
	if captureErr != nil || captured.Snapshot == nil {
		if evidence.Authority != model.GitAuthorityHostedChange {
			evidence.Assessment = captured.Assessment
			evidence.Authority = model.GitAuthorityNone
			evidence.AuthoritySelection = nil
		}
		return ix.db.ReplaceSessionGitEvidence(evidence)
	}
	if err := recheckGitSource(ctx, snapshotReader, session, envelope); err != nil {
		return err
	}
	if err := ix.db.StoreLocalGitSnapshot(localSnapshotWrite(bindingID, captured.Snapshot)); err != nil {
		return err
	}

	switch captureKind {
	case model.GitSnapshotBaseline:
		evidence.Baseline = snapshotSummary(captured.Snapshot)
		if evidence.Authority != model.GitAuthorityHostedChange {
			evidence.Assessment = model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonFinalNotCaptured)
			evidence.Authority = model.GitAuthorityNone
			evidence.AuthoritySelection = nil
		}
		evidence.Provisional = true
		return ix.db.ReplaceSessionGitEvidence(evidence)
	case model.GitSnapshotCheckpoint:
		if evidence.Authority != model.GitAuthorityHostedChange {
			evidence.Assessment = model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonSessionStillLive)
			evidence.Authority = model.GitAuthorityNone
			evidence.AuthoritySelection = nil
			evidence.Files = []model.GitFileChange{}
			evidence.CandidateCommits = []model.GitCandidateCommit{}
		}
		evidence.Provisional = true
		return ix.db.ReplaceSessionGitEvidence(evidence)
	case model.GitSnapshotFinal:
		return ix.deriveLocalGitEvidence(ctx, session, envelope, evidence, repository, baselineRecord, snapshotRecord(captured.Snapshot))
	default:
		return nil
	}
}

func recheckGitSource(ctx context.Context, source reader.AuthoritativeIndexSnapshotReader, session model.Session, before *model.IndexSnapshotEnvelope) error {
	after, err := source.ReadIndexSnapshotEnvelope(ctx, session)
	if err != nil {
		return err
	}
	if validation := model.ValidateIndexSnapshotEnvelope(after); !validation.OK() {
		return errors.New("authoritative Git source recheck was invalid")
	}
	if after.SourceRevision != before.SourceRevision || after.SourceFingerprint != before.SourceFingerprint {
		return errors.New("authoritative Git source changed during capture")
	}
	return nil
}

func (ix *Indexer) deriveLocalGitEvidence(
	ctx context.Context,
	session model.Session,
	envelope *model.IndexSnapshotEnvelope,
	evidence model.SessionGitEvidence,
	repository *gitevidence.Repository,
	baselineRecord, finalRecord db.LocalGitSnapshotRecord,
) error {
	baseline := snapshotFromRecord(baselineRecord, repository.WorktreeID)
	final := snapshotFromRecord(finalRecord, repository.WorktreeID)
	diff, err := gitevidence.DiffSnapshots(baseline, final)
	if err != nil {
		return err
	}
	evidence.Baseline = snapshotSummaryCopy(baselineRecord.Summary)
	evidence.Final = snapshotSummaryCopy(finalRecord.Summary)
	evidence.Provisional = false
	if evidence.Authority == model.GitAuthorityHostedChange {
		// Local snapshots remain useful private observations, but a user-selected
		// fixed hosted change owns the public file set until explicitly changed.
		return ix.db.ReplaceSessionGitEvidence(evidence)
	}

	renderedPatches := gitevidence.RenderPatches(baseline, final, diff, gitevidence.DefaultPatchLimits())
	patchesByChange := make(map[string]gitevidence.RenderedPatch, len(renderedPatches))
	for _, patch := range renderedPatches {
		patchesByChange[patch.ChangeKey] = patch
	}
	files := make([]model.GitFileChange, 0, len(diff.Files))
	patchContent := make(map[string][]byte, len(diff.Files))
	changedPaths := make([]gitevidence.CandidateCommitPath, 0, len(diff.Files))
	for _, change := range diff.Files {
		key := snapshotPathKey(change.Key, change.PathBytes)
		pathBytesB64 := ""
		if change.PathEncoding == model.GitPathBytesB64 {
			pathBytesB64 = base64.StdEncoding.EncodeToString(change.PathBytes)
		}
		oldMode, newMode := "", ""
		binary, submodule := false, false
		if change.Before != nil {
			oldMode = change.Before.Mode
			binary = binary || change.Before.Binary
			submodule = submodule || change.Before.Kind == gitevidence.FileGitlink
		}
		if change.After != nil {
			newMode = change.After.Mode
			binary = binary || change.After.Binary
			submodule = submodule || change.After.Kind == gitevidence.FileGitlink
		}
		patch := patchesByChange[change.Key]
		patchAssessment := patch.Assessment
		if patchAssessment.State == "" {
			patchAssessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonSnapshotObjectMissing)
		}
		if patchAssessment.State == model.GitEvidenceExact {
			patchContent[key] = append([]byte(nil), patch.Content...)
		}
		var additions, deletions *int
		if patchAssessment.State == model.GitEvidenceExact {
			additions = intPointer(patch.Additions)
			deletions = intPointer(patch.Deletions)
		}
		files = append(files, model.GitFileChange{
			Ordinal: len(files), Key: key, Layer: model.GitFileLayerWorktree,
			DisplayPath: change.DisplayPath, PathBytesB64: pathBytesB64,
			PathEncoding: change.PathEncoding, Status: change.Status,
			OldMode: oldMode, NewMode: newMode, Binary: binary, Submodule: submodule,
			Additions: additions, Deletions: deletions,
			StatusAssessment: change.StatusAssessment, PatchAssessment: patchAssessment,
			Evidence: []model.GitEvidenceLink{},
		})
		if utf8.Valid(change.PathBytes) {
			changedPaths = append(changedPaths, gitevidence.CandidateCommitPath{Key: key, Path: string(change.PathBytes)})
		}
	}
	ix.attachRootMutationEvidence(session, envelope, repository, files)
	evidence.Files = files
	evidence.CandidateCommits = []model.GitCandidateCommit{}
	commitResult, commitErr := ix.git.discoverer.Discover(ctx, repository, gitevidence.CandidateCommitInput{
		Binding: evidence.Repository, OriginSHA: exactOriginSHA(evidence.Origin),
		BaselineSHA: baseline.HeadSHA, FinalSHA: final.HeadSHA, ChangedPaths: changedPaths,
		WindowStart: baseline.CaptureStartedAt, WindowEnd: final.CaptureCompletedAt,
	})
	if commitErr == nil {
		evidence.CandidateCommits = commitResult.Commits
	}
	evidence.Assessment = diff.Assessment
	if evidence.Assessment.State == model.GitEvidenceExact {
		for _, file := range files {
			if file.PatchAssessment.State != model.GitEvidenceExact {
				evidence.Assessment = model.NonExactGitEvidence(
					model.GitEvidenceEstimated, file.PatchAssessment.ReasonCode,
				)
				break
			}
		}
	}
	evidence.Authority = model.GitAuthorityLocalInterval
	evidence.AuthoritySelection = nil

	// First publish the new file identity with patches gated unavailable. Blob
	// replacement then cannot expose either an old or a half-published patch.
	staged := evidence
	staged.Files = append([]model.GitFileChange(nil), evidence.Files...)
	for index := range staged.Files {
		if staged.Files[index].PatchAssessment.State == model.GitEvidenceExact {
			staged.Files[index].PatchAssessment = model.NonExactGitEvidence(
				model.GitEvidenceUnavailable, model.ReasonSnapshotObjectMissing,
			)
			staged.Files[index].Additions = nil
			staged.Files[index].Deletions = nil
		}
	}
	if staged.Assessment.State == model.GitEvidenceExact && len(patchContent) != 0 {
		staged.Assessment = model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonSnapshotObjectMissing)
	}
	if err := ix.db.ReplaceSessionGitEvidence(staged); err != nil {
		return err
	}
	if err := ix.db.ReplaceSessionGitPatchContent(
		evidence.RootAgentType, evidence.RootSessionID, evidence.RepositoryEntryKey,
		patchContent, db.DefaultSourceContentQuota,
	); err != nil {
		return err
	}
	return ix.db.ReplaceSessionGitEvidence(evidence)
}

func intPointer(value int) *int {
	copy := value
	return &copy
}

func (ix *Indexer) attachRootMutationEvidence(
	session model.Session,
	envelope *model.IndexSnapshotEnvelope,
	repository *gitevidence.Repository,
	files []model.GitFileChange,
) {
	if envelope == nil || envelope.OriginGit == nil || repository == nil || len(files) == 0 {
		return
	}
	mutationRoot := envelope.OriginGit.WorktreePath.Value
	relativeRoot, err := filepath.Rel(repository.WorktreeRoot, mutationRoot)
	if err != nil || filepath.IsAbs(relativeRoot) || relativeRoot == ".." ||
		strings.HasPrefix(relativeRoot, ".."+string(filepath.Separator)) {
		return
	}
	prefix := ""
	if relativeRoot != "." {
		prefix = filepath.ToSlash(relativeRoot)
	}
	result, err := gitevidence.BuildFileMutationEvidence(envelope.RenderEvents, gitevidence.MutationSource{
		RootAgentType: session.AgentType, RootSessionID: session.ID,
		SourceRevision: envelope.SourceRevision,
		DefaultAttribution: gitevidence.MutationAttribution{
			SourceAgentType: session.AgentType, SourceSessionID: session.ID,
		},
	})
	if err != nil {
		return
	}

	mutations := make([]gitevidence.FileMutationEvidence, 0, len(result.Mutations))
	anchors := make([]gitevidence.MutationPositionAnchor, 0, len(result.Mutations))
	for _, mutation := range result.Mutations {
		// The current replay position cache emits depth-zero tool anchors. Child
		// transcript positions need their own source Session revision and are not
		// guessed from the root layout.
		if mutation.Depth != 0 || mutation.SourceAgentType != session.AgentType || mutation.SourceSessionID != session.ID {
			continue
		}
		mutation.Path = path.Join(prefix, mutation.Path)
		if mutation.PreviousPath != "" {
			mutation.PreviousPath = path.Join(prefix, mutation.PreviousPath)
		}
		turn := mutation.TurnIndex
		mutations = append(mutations, mutation)
		anchors = append(anchors, gitevidence.MutationPositionAnchor{
			Path:          mutation.Path,
			RootAgentType: mutation.RootAgentType, RootSessionID: mutation.RootSessionID,
			SourceAgentType: mutation.SourceAgentType, SourceSessionID: mutation.SourceSessionID,
			BackingAgentType: mutation.BackingAgentType, BackingSessionID: mutation.BackingSessionID,
			InvocationID: mutation.InvocationID, EventID: mutation.EventID, ToolCallID: mutation.ToolCallID,
			TurnIndex: &turn, RecordedAt: mutation.RecordedAt,
		})
	}
	positions := gitevidence.MutationPositionSet{
		SourceRevision:    envelope.SourceRevision,
		PositionsRevision: render.PositionsRevision(session, render.Options{}),
		Anchors:           anchors,
	}
	for fileIndex := range files {
		if files[fileIndex].PathEncoding != model.GitPathUTF8 {
			continue
		}
		for _, mutation := range mutations {
			resolution := gitevidence.ResolveMutationEvidenceLink(mutation, files[fileIndex].DisplayPath, positions)
			if resolution.Link != nil {
				files[fileIndex].Evidence = append(files[fileIndex].Evidence, *resolution.Link)
			}
		}
	}
}

func sessionRepositoryEntryKey(agentType, sessionID, worktreeID string) string {
	digest := sha256.Sum256([]byte("repository-entry\x00" + agentType + "\x00" + sessionID + "\x00" + worktreeID))
	return "repository-" + hex.EncodeToString(digest[:])
}

func sessionRepositoryBinding(repository *gitevidence.Repository, entryKey string, origin *model.SessionGitOrigin) model.GitRepositoryBinding {
	binding := repository.Binding(entryKey)
	// Branch and HEAD in the public binding describe the Session's recorded
	// origin, never whatever happens to be checked out when history is indexed.
	binding.Branch = ""
	binding.HeadSHA = ""
	if origin != nil {
		if origin.Branch.Assessment.State == model.GitEvidenceExact {
			binding.Branch = origin.Branch.Value
		}
		if origin.HeadSHA.Assessment.State == model.GitEvidenceExact {
			binding.HeadSHA = origin.HeadSHA.Value
		}
	}
	return binding
}

func localSnapshotWrite(bindingID string, snapshot *gitevidence.Snapshot) db.LocalGitSnapshotWrite {
	contents := make(map[string][]byte, len(snapshot.Contents))
	for _, content := range snapshot.Contents {
		contents[content.SHA256] = content.Bytes
	}
	files := make([]db.LocalGitSnapshotFileWrite, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		content, retain := contents[file.ContentRef]
		contentBytes := file.Size
		if retain {
			contentBytes = int64(len(content))
		}
		files = append(files, db.LocalGitSnapshotFileWrite{
			PathKey: snapshotPathKey(file.Key, file.PathBytes), Ordinal: file.Ordinal,
			RawPath: file.PathBytes, DisplayPath: file.DisplayPath, PathEncoding: file.PathEncoding,
			Layer: model.GitFileLayerWorktree, FileType: localSnapshotFileType(file),
			Mode: file.Mode, GitOID: file.GitOID, ContentHash: file.ContentHash,
			ContentBytes: contentBytes, Content: content, RetainContent: retain,
			Assessment: file.ContentAssessment,
		})
	}
	return db.LocalGitSnapshotWrite{
		BindingID: bindingID,
		Summary: model.GitSnapshotSummary{
			SnapshotID: snapshot.SnapshotID, Kind: snapshot.Kind, HeadSHA: snapshot.HeadSHA,
			ManifestDigest: storageSHA256Digest(snapshot.ManifestDigest), SourceRevision: snapshot.SourceRevision,
			CaptureStartedAt: snapshot.CaptureStartedAt, CaptureEndedAt: snapshot.CaptureCompletedAt,
			Assessment: snapshot.Assessment,
		},
		IndexFingerprint: storageSHA256Digest(snapshot.IndexDigest), StatusFingerprint: storageSHA256Digest(snapshot.StatusDigest),
		Provisional: snapshot.Kind == model.GitSnapshotCheckpoint, Files: files,
		Quota: db.DefaultSourceContentQuota,
	}
}

func localSnapshotFileType(file gitevidence.SnapshotFile) db.LocalGitFileType {
	if file.Binary {
		return db.LocalGitFileBinary
	}
	switch file.Kind {
	case gitevidence.FileSymlink:
		return db.LocalGitFileSymlink
	case gitevidence.FileGitlink:
		return db.LocalGitFileSubmodule
	case gitevidence.FileSpecial:
		return db.LocalGitFileSpecial
	case gitevidence.FileMissing:
		return db.LocalGitFileMissing
	default:
		return db.LocalGitFileRegular
	}
}

func snapshotRecord(snapshot *gitevidence.Snapshot) db.LocalGitSnapshotRecord {
	write := localSnapshotWrite("unused", snapshot)
	files := make([]db.LocalGitSnapshotFileRecord, 0, len(write.Files))
	for _, file := range write.Files {
		files = append(files, db.LocalGitSnapshotFileRecord{
			PathKey: file.PathKey, Ordinal: file.Ordinal, RawPath: file.RawPath,
			DisplayPath: file.DisplayPath, PathEncoding: file.PathEncoding, Layer: file.Layer,
			FileType: file.FileType, Mode: file.Mode, GitOID: file.GitOID,
			ContentHash: file.ContentHash, ContentBytes: file.ContentBytes,
			Content: file.Content, Retained: file.RetainContent, Assessment: file.Assessment,
		})
	}
	return db.LocalGitSnapshotRecord{
		Summary: write.Summary, IndexFingerprint: write.IndexFingerprint,
		StatusFingerprint: write.StatusFingerprint, Provisional: write.Provisional, Files: files,
	}
}

func snapshotFromRecord(record db.LocalGitSnapshotRecord, worktreeID string) *gitevidence.Snapshot {
	files := make([]gitevidence.SnapshotFile, 0, len(record.Files))
	contents := make([]gitevidence.ContentBlob, 0, len(record.Files))
	seenContent := map[string]bool{}
	for _, file := range record.Files {
		kind, binary := snapshotFileKind(file.FileType)
		contentRef := ""
		if file.Retained {
			contentRef = file.ContentHash
			if !seenContent[file.ContentHash] {
				contents = append(contents, gitevidence.ContentBlob{SHA256: file.ContentHash, Bytes: append([]byte(nil), file.Content...)})
				seenContent[file.ContentHash] = true
			}
		}
		files = append(files, gitevidence.SnapshotFile{
			Ordinal: file.Ordinal, Key: "path:sha256:" + file.PathKey,
			PathBytes: append([]byte(nil), file.RawPath...), DisplayPath: file.DisplayPath,
			PathEncoding: file.PathEncoding, Present: file.FileType != db.LocalGitFileMissing,
			Kind: kind, Mode: file.Mode, GitOID: file.GitOID, Size: file.ContentBytes,
			ContentHash: file.ContentHash, ContentRef: contentRef, Binary: binary,
			ContentAssessment: file.Assessment, PatchAssessment: file.Assessment,
		})
	}
	return &gitevidence.Snapshot{
		SnapshotID: record.Summary.SnapshotID, Kind: record.Summary.Kind,
		SourceRevision: record.Summary.SourceRevision, WorktreeID: worktreeID,
		HeadSHA: record.Summary.HeadSHA, IndexDigest: record.IndexFingerprint,
		StatusDigest: record.StatusFingerprint, ManifestDigest: record.Summary.ManifestDigest,
		CaptureStartedAt: record.Summary.CaptureStartedAt, CaptureCompletedAt: record.Summary.CaptureEndedAt,
		Assessment: record.Summary.Assessment, Files: files, Contents: contents,
	}
}

func snapshotFileKind(fileType db.LocalGitFileType) (gitevidence.FileKind, bool) {
	switch fileType {
	case db.LocalGitFileSymlink:
		return gitevidence.FileSymlink, false
	case db.LocalGitFileSubmodule:
		return gitevidence.FileGitlink, false
	case db.LocalGitFileBinary:
		return gitevidence.FileRegular, true
	case db.LocalGitFileSpecial:
		return gitevidence.FileSpecial, false
	case db.LocalGitFileMissing:
		return gitevidence.FileMissing, false
	default:
		return gitevidence.FileRegular, false
	}
}

func snapshotSummary(snapshot *gitevidence.Snapshot) *model.GitSnapshotSummary {
	return &model.GitSnapshotSummary{
		SnapshotID: snapshot.SnapshotID, Kind: snapshot.Kind, HeadSHA: snapshot.HeadSHA,
		ManifestDigest: storageSHA256Digest(snapshot.ManifestDigest), SourceRevision: snapshot.SourceRevision,
		CaptureStartedAt: snapshot.CaptureStartedAt, CaptureEndedAt: snapshot.CaptureCompletedAt,
		Assessment: snapshot.Assessment,
	}
}

func snapshotSummaryCopy(summary model.GitSnapshotSummary) *model.GitSnapshotSummary {
	copy := summary
	return &copy
}

func snapshotPathKey(key string, raw []byte) string {
	if value := strings.TrimPrefix(key, "path:sha256:"); len(value) == 64 {
		return value
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func exactOriginSHA(origin *model.SessionGitOrigin) string {
	if origin != nil && origin.HeadSHA.Assessment.State == model.GitEvidenceExact {
		return origin.HeadSHA.Value
	}
	return ""
}

func gitOriginSourceRevision(origin *model.SessionGitOrigin) string {
	if origin == nil {
		return ""
	}
	for _, revision := range []string{
		origin.WorktreePath.SourceRevision,
		origin.HeadSHA.SourceRevision,
		origin.Branch.SourceRevision,
		origin.RepositoryURL.SourceRevision,
		origin.DirtyState.SourceRevision,
	} {
		if revision != "" {
			return revision
		}
	}
	return ""
}

func storageSHA256Digest(value string) string {
	if len(value) == 64 && !strings.ContainsAny(value, "GHIJKLMNOPQRSTUVWXYZghijklmnopqrstuvwxyz") {
		return "sha256:" + value
	}
	return value
}
