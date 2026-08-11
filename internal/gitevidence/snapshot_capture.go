package gitevidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bbsteel/session-insight/internal/model"
)

type Capturer struct {
	runner *Runner
	limits CaptureLimits
	now    func() time.Time

	// Tests use these hooks to deterministically force filesystem and Git
	// races. Production constructors never expose or populate them.
	afterFileRead     func(path string, attempt int)
	afterManifestRead func(attempt int)
}

func NewCapturer(runner *Runner, config CaptureConfig) (*Capturer, error) {
	limits := config.Limits
	if limits == (CaptureLimits{}) {
		limits = DefaultCaptureLimits()
	}
	if runner == nil || limits.Timeout <= 0 || limits.Attempts <= 0 || limits.FileAttempts <= 0 ||
		limits.MaxPaths <= 0 || limits.MaxPathBytes <= 0 || limits.MaxFileBytes <= 0 || limits.MaxSessionBytes <= 0 {
		return nil, &CaptureError{Code: CaptureErrorInvalidConfig}
	}
	return &Capturer{runner: runner, limits: limits, now: func() time.Time { return time.Now().UTC() }}, nil
}

type indexEntry struct {
	mode     string
	oid      string
	unmerged bool
}

type pathStatus struct {
	staged    bool
	unstaged  bool
	untracked bool
	removed   bool
	unmerged  bool
}

type gitObservation struct {
	head         string
	indexRaw     []byte
	statusRaw    []byte
	indexDigest  string
	statusDigest string
}

func (capturer *Capturer) Capture(ctx context.Context, repository Repository, request CaptureRequest) (CaptureResult, error) {
	if capturer == nil || capturer.runner == nil {
		err := &CaptureError{Code: CaptureErrorInvalidConfig}
		return failedCapture(err), err
	}
	if request.SourceRevision == "" || repository.WorktreeRoot == "" || repository.WorktreeID == "" ||
		(request.Kind != model.GitSnapshotBaseline && request.Kind != model.GitSnapshotCheckpoint && request.Kind != model.GitSnapshotFinal) {
		err := &CaptureError{Code: CaptureErrorInvalidRequest}
		return failedCapture(err), err
	}
	if _, err := validateWorkingDirectory(repository.WorktreeRoot); err != nil {
		captureErr := &CaptureError{Code: CaptureErrorInvalidRequest, Cause: err}
		return failedCapture(captureErr), captureErr
	}

	captureContext, cancel := context.WithTimeout(ctx, capturer.limits.Timeout)
	defer cancel()
	if _, err := capturer.verifyRepositoryIdentity(captureContext, repository); err != nil {
		return failedCapture(err), err
	}
	for attempt := 0; attempt < capturer.limits.Attempts; attempt++ {
		startedAt := capturer.now()
		before, err := capturer.observe(captureContext, repository)
		if err != nil {
			return failedCapture(err), err
		}
		pathsResult, err := capturer.runner.Run(captureContext, repository.WorktreeRoot, OperationSnapshotPaths)
		if err != nil {
			wrapped := wrapCaptureGitError(err)
			return failedCapture(wrapped), wrapped
		}
		index, err := parseIndexEntries(before.indexRaw, repository.ObjectFormat)
		if err != nil {
			return failedCapture(err), err
		}
		statuses, err := parseStatusEntries(before.statusRaw)
		if err != nil {
			return failedCapture(err), err
		}
		paths, err := collectSnapshotPaths(pathsResult.Stdout, index, statuses, capturer.limits)
		if err != nil {
			return failedCapture(err), err
		}

		files, contents, assessment, err := capturer.captureFiles(captureContext, repository.WorktreeRoot, paths, index, statuses)
		if err != nil {
			return failedCapture(err), err
		}
		if capturer.afterManifestRead != nil {
			capturer.afterManifestRead(attempt)
		}
		after, err := capturer.observe(captureContext, repository)
		if err != nil {
			return failedCapture(err), err
		}
		if !observationsEqual(before, after) {
			continue
		}
		verified, err := capturer.verifyRepositoryIdentity(captureContext, repository)
		if err != nil {
			return failedCapture(err), err
		}
		if verified.HeadSHA != after.head {
			continue
		}

		completedAt := capturer.now()
		manifestDigest := digestManifest(files)
		snapshotID := request.SnapshotID
		if snapshotID == "" {
			snapshotID = hashIdentity("snapshot", repository.WorktreeID, string(request.Kind), request.SourceRevision,
				before.head, manifestDigest, strconv.FormatInt(startedAt.UnixNano(), 10))
		}
		snapshot := &Snapshot{
			SnapshotID: snapshotID, Kind: request.Kind, SourceRevision: request.SourceRevision,
			WorktreeID: repository.WorktreeID, HeadSHA: before.head,
			IndexDigest: before.indexDigest, StatusDigest: before.statusDigest, ManifestDigest: manifestDigest,
			CaptureStartedAt: startedAt, CaptureCompletedAt: completedAt,
			Assessment: assessment, Files: files, Contents: contents,
		}
		return CaptureResult{Snapshot: snapshot, Assessment: assessment}, nil
	}
	err := &CaptureError{Code: CaptureErrorRaced}
	return failedCapture(err), err
}

func (capturer *Capturer) verifyRepositoryIdentity(ctx context.Context, expected Repository) (*Repository, error) {
	resolver, err := NewResolver(capturer.runner)
	if err != nil {
		return nil, &CaptureError{Code: CaptureErrorInvalidConfig, Cause: err}
	}
	resolution, err := resolver.Resolve(ctx, expected.WorktreeRoot)
	if err != nil || resolution.Repository == nil {
		return nil, &CaptureError{Code: CaptureErrorIdentityChanged, Cause: err}
	}
	actual := resolution.Repository
	if actual.WorktreeRoot != expected.WorktreeRoot || actual.CommonRootID != expected.CommonRootID || actual.WorktreeID != expected.WorktreeID || actual.ObjectFormat != expected.ObjectFormat {
		return nil, &CaptureError{Code: CaptureErrorIdentityChanged}
	}
	return actual, nil
}

func failedCapture(err error) CaptureResult {
	return CaptureResult{Assessment: model.NonExactGitEvidence(model.GitEvidenceUnavailable, captureReason(err))}
}

func wrapCaptureGitError(err error) error {
	var runnerErr *Error
	if errors.As(err, &runnerErr) && runnerErr.Code == ErrorOutputLimitExceeded {
		return &CaptureError{Code: CaptureErrorLimit, Cause: err}
	}
	return &CaptureError{Code: CaptureErrorGit, Cause: err}
}

func (capturer *Capturer) observe(ctx context.Context, repository Repository) (gitObservation, error) {
	headResult, err := capturer.runner.Run(ctx, repository.WorktreeRoot, OperationHead)
	if err != nil {
		return gitObservation{}, wrapCaptureGitError(err)
	}
	head, err := parseSingleLine(OperationHead, headResult.Stdout)
	if err != nil || !validObjectID(head, repository.ObjectFormat) {
		return gitObservation{}, &CaptureError{Code: CaptureErrorMalformed, Cause: err}
	}
	indexResult, err := capturer.runner.Run(ctx, repository.WorktreeRoot, OperationIndexState)
	if err != nil {
		return gitObservation{}, wrapCaptureGitError(err)
	}
	statusResult, err := capturer.runner.Run(ctx, repository.WorktreeRoot, OperationStatusState)
	if err != nil {
		return gitObservation{}, wrapCaptureGitError(err)
	}
	return gitObservation{
		head: head, indexRaw: indexResult.Stdout, statusRaw: statusResult.Stdout,
		indexDigest: hashBytes(indexResult.Stdout), statusDigest: hashBytes(statusResult.Stdout),
	}, nil
}

func observationsEqual(left, right gitObservation) bool {
	return left.head == right.head && bytes.Equal(left.indexRaw, right.indexRaw) && bytes.Equal(left.statusRaw, right.statusRaw)
}

func parseIndexEntries(raw []byte, format ObjectFormat) (map[string]indexEntry, error) {
	entries := map[string]indexEntry{}
	for _, record := range splitNUL(raw) {
		header, path, ok := strings.Cut(record, "\t")
		fields := strings.Fields(header)
		if !ok || path == "" || len(fields) != 3 || !validObjectID(fields[1], format) {
			return nil, &CaptureError{Code: CaptureErrorMalformed}
		}
		stage, err := strconv.Atoi(fields[2])
		if err != nil || stage < 0 || stage > 3 || !validGitMode(fields[0]) {
			return nil, &CaptureError{Code: CaptureErrorMalformed}
		}
		entry := entries[path]
		if stage == 0 {
			entry.mode, entry.oid = fields[0], fields[1]
		} else {
			entry.unmerged = true
			if entry.mode == "" {
				entry.mode, entry.oid = fields[0], fields[1]
			}
		}
		entries[path] = entry
	}
	return entries, nil
}

func parseStatusEntries(raw []byte) (map[string]pathStatus, error) {
	statuses := map[string]pathStatus{}
	records := splitNUL(raw)
	for index := 0; index < len(records); index++ {
		record := records[index]
		if len(record) < 2 {
			return nil, &CaptureError{Code: CaptureErrorMalformed}
		}
		switch record[0] {
		case '?':
			if !strings.HasPrefix(record, "? ") || len(record) == 2 {
				return nil, &CaptureError{Code: CaptureErrorMalformed}
			}
			status := statuses[record[2:]]
			status.untracked = true
			statuses[record[2:]] = status
		case '!':
			continue
		case '1':
			fields := strings.SplitN(record, " ", 9)
			if len(fields) != 9 || len(fields[1]) != 2 || fields[8] == "" {
				return nil, &CaptureError{Code: CaptureErrorMalformed}
			}
			status := statuses[fields[8]]
			applyXY(&status, fields[1])
			statuses[fields[8]] = status
		case '2':
			fields := strings.SplitN(record, " ", 10)
			if len(fields) != 10 || len(fields[1]) != 2 || fields[9] == "" || index+1 >= len(records) {
				return nil, &CaptureError{Code: CaptureErrorMalformed}
			}
			index++
			oldPath := records[index]
			if oldPath == "" {
				return nil, &CaptureError{Code: CaptureErrorMalformed}
			}
			current := statuses[fields[9]]
			applyXY(&current, strings.Map(normalizeRenameCode, fields[1]))
			statuses[fields[9]] = current
			old := statuses[oldPath]
			old.removed = true
			if fields[1][0] != '.' {
				old.staged = true
			}
			if fields[1][1] != '.' {
				old.unstaged = true
			}
			statuses[oldPath] = old
		case 'u':
			fields := strings.SplitN(record, " ", 11)
			if len(fields) != 11 || fields[10] == "" {
				return nil, &CaptureError{Code: CaptureErrorMalformed}
			}
			status := statuses[fields[10]]
			status.unmerged, status.staged, status.unstaged = true, true, true
			statuses[fields[10]] = status
		default:
			return nil, &CaptureError{Code: CaptureErrorMalformed}
		}
	}
	return statuses, nil
}

func normalizeRenameCode(code rune) rune {
	if code == 'R' || code == 'C' {
		return 'A'
	}
	return code
}

func applyXY(status *pathStatus, xy string) {
	if xy[0] != '.' {
		status.staged = true
	}
	if xy[1] != '.' {
		status.unstaged = true
	}
	if xy[0] == 'D' || xy[1] == 'D' {
		status.removed = true
	}
	if xy[0] == 'U' || xy[1] == 'U' {
		status.unmerged = true
	}
}

func collectSnapshotPaths(raw []byte, index map[string]indexEntry, statuses map[string]pathStatus, limits CaptureLimits) ([]string, error) {
	set := make(map[string]struct{}, len(index)+len(statuses))
	for _, path := range splitNUL(raw) {
		set[path] = struct{}{}
	}
	for path := range index {
		set[path] = struct{}{}
	}
	for path := range statuses {
		set[path] = struct{}{}
	}
	if len(set) > limits.MaxPaths {
		return nil, &CaptureError{Code: CaptureErrorLimit}
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		if err := validateSnapshotPath(path, limits.MaxPathBytes); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool { return bytes.Compare([]byte(paths[i]), []byte(paths[j])) < 0 })
	return paths, nil
}

func splitNUL(raw []byte) []string {
	parts := bytes.Split(raw, []byte{0})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) != 0 {
			result = append(result, string(part))
		}
	}
	return result
}

func validateSnapshotPath(path string, maxBytes int) error {
	if path == "" || len(path) > maxBytes || strings.ContainsRune(path, '\x00') || filepath.IsAbs(path) {
		return &CaptureError{Code: CaptureErrorUnsafePath}
	}
	native := filepath.FromSlash(path)
	cleaned := filepath.Clean(native)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned != native {
		return &CaptureError{Code: CaptureErrorUnsafePath}
	}
	return nil
}

func validGitMode(mode string) bool {
	switch mode {
	case "100644", "100755", "120000", "160000":
		return true
	default:
		return false
	}
}

func (capturer *Capturer) captureFiles(ctx context.Context, worktreeRoot string, paths []string, index map[string]indexEntry, statuses map[string]pathStatus) ([]SnapshotFile, []ContentBlob, model.GitEvidenceAssessment, error) {
	root, err := os.OpenRoot(worktreeRoot)
	if err != nil {
		return nil, nil, model.GitEvidenceAssessment{}, &CaptureError{Code: CaptureErrorUnsafePath}
	}
	defer root.Close()

	files := make([]SnapshotFile, 0, len(paths))
	blobs := map[string][]byte{}
	var storedBytes int64
	var degradationReasons []model.GitEvidenceReasonCode
	for ordinal, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, nil, model.GitEvidenceAssessment{}, &CaptureError{Code: CaptureErrorRaced, Cause: err}
		}
		entry, tracked := index[path]
		status := statuses[path]
		file, content := capturer.captureFile(ctx, root, path, entry, tracked, status, capturer.limits.MaxSessionBytes-storedBytes)
		file.Ordinal = ordinal
		if content != nil {
			if _, exists := blobs[file.ContentHash]; !exists {
				blobs[file.ContentHash] = append([]byte(nil), content...)
				storedBytes += int64(len(content))
			}
			file.ContentRef = file.ContentHash
		}
		if file.ContentAssessment.State != model.GitEvidenceExact {
			degradationReasons = appendUniqueReason(degradationReasons, file.ContentAssessment.ReasonCode)
		}
		files = append(files, file)
	}
	contents := make([]ContentBlob, 0, len(blobs))
	for sha, content := range blobs {
		contents = append(contents, ContentBlob{SHA256: sha, Bytes: content})
	}
	sort.Slice(contents, func(i, j int) bool { return contents[i].SHA256 < contents[j].SHA256 })
	assessment := model.ExactGitEvidence()
	if len(degradationReasons) != 0 {
		assessment = model.NonExactGitEvidence(model.GitEvidenceEstimated, degradationReasons[0], degradationReasons[1:]...)
	}
	return files, contents, assessment, nil
}

func (capturer *Capturer) captureFile(ctx context.Context, root *os.Root, path string, entry indexEntry, tracked bool, status pathStatus, remainingBytes int64) (SnapshotFile, []byte) {
	file := newSnapshotFile(path, entry, tracked, status)
	last := file
	for attempt := 0; attempt < capturer.limits.FileAttempts; attempt++ {
		captured, content, retry := capturer.readPathOnce(ctx, root, path, file, remainingBytes, attempt)
		last = captured
		if !retry {
			return captured, content
		}
	}
	last.Present = true
	if last.IndexMode == "160000" {
		last.Kind, last.Mode = FileGitlink, "160000"
		last.Issues = appendUniqueIssue(last.Issues, FileIssueGitlink)
	} else {
		last.Kind = FileSpecial
	}
	last.Issues = append(last.Issues, FileIssueRaced)
	last.ContentHash, last.ContentRef = "", ""
	last.ContentAssessment = model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonCaptureRaced)
	last.PatchAssessment = last.ContentAssessment
	return last, nil
}

func newSnapshotFile(path string, entry indexEntry, tracked bool, status pathStatus) SnapshotFile {
	raw := []byte(path)
	display := safeDisplayPath(path)
	encoding := model.GitPathUTF8
	if !utf8.Valid(raw) {
		encoding = model.GitPathBytesB64
	}
	states := make([]PathState, 0, 5)
	if tracked {
		states = append(states, PathTracked)
	}
	if status.staged {
		states = append(states, PathStaged)
	}
	if status.unstaged {
		states = append(states, PathUnstaged)
	}
	if status.untracked {
		states = append(states, PathUntracked)
	}
	if status.removed {
		states = append(states, PathRemoved)
	}
	if status.unmerged || entry.unmerged {
		states = append(states, PathUnmerged)
	}
	return SnapshotFile{
		Key: "path:sha256:" + hashBytes(raw), PathBytes: append([]byte(nil), raw...),
		DisplayPath: display, PathEncoding: encoding, States: states,
		IndexMode: entry.mode, GitOID: entry.oid,
		ContentAssessment: model.ExactGitEvidence(), PatchAssessment: model.ExactGitEvidence(),
	}
}

func safeDisplayPath(path string) string {
	path = strings.ToValidUTF8(path, "�")
	return strings.Map(func(value rune) rune {
		if value < 0x20 || (value >= 0x7f && value <= 0x9f) {
			return '�'
		}
		return value
	}, path)
}

func (capturer *Capturer) readPathOnce(ctx context.Context, root *os.Root, path string, base SnapshotFile, remainingBytes int64, attempt int) (SnapshotFile, []byte, bool) {
	file := base
	if err := rejectSymlinkParents(root, path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			file.Present, file.Kind = false, FileMissing
			file.Issues = append(file.Issues, FileIssueRemoved)
			return file, nil, false
		}
		file.Present, file.Kind = true, FileSpecial
		file.Issues = append(file.Issues, FileIssueUnsafePath)
		file.ContentAssessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonWorktreeIdentityChanged)
		file.PatchAssessment = file.ContentAssessment
		return file, nil, false
	}
	before, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		file.Present, file.Kind = false, FileMissing
		file.Issues = append(file.Issues, FileIssueRemoved)
		return file, nil, false
	}
	if err != nil {
		return file, nil, true
	}
	file.Present, file.Size, file.ModTimeUnixNano = true, before.Size(), before.ModTime().UnixNano()
	if file.IndexMode == "160000" {
		if capturer.afterFileRead != nil {
			capturer.afterFileRead(path, attempt)
		}
		after, err := root.Lstat(path)
		if err != nil || !stableFileInfo(before, after) {
			return file, nil, true
		}
		file.Kind, file.Mode = FileGitlink, "160000"
		file.Issues = append(file.Issues, FileIssueGitlink)
		file.ContentAssessment = model.ExactGitEvidence()
		file.PatchAssessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonSubmoduleNotExpanded)
		return file, nil, false
	}
	switch {
	case before.Mode()&os.ModeSymlink != 0:
		file.Kind, file.Mode = FileSymlink, "120000"
		file.Issues = append(file.Issues, FileIssueSymlink)
		if before.Size() > capturer.limits.MaxFileBytes || before.Size() > remainingBytes {
			return limitedFile(file, before.Size() > capturer.limits.MaxFileBytes), nil, false
		}
		target, err := root.Readlink(path)
		if err != nil {
			return file, nil, true
		}
		if capturer.afterFileRead != nil {
			capturer.afterFileRead(path, attempt)
		}
		after, err := root.Lstat(path)
		if err != nil || !stableFileInfo(before, after) {
			return file, nil, true
		}
		content := []byte(target)
		if int64(len(content)) > capturer.limits.MaxFileBytes || int64(len(content)) > remainingBytes {
			return limitedFile(file, int64(len(content)) > capturer.limits.MaxFileBytes), nil, false
		}
		file.Size = int64(len(content))
		file.ContentHash = hashBytes(content)
		return file, content, false
	case !before.Mode().IsRegular():
		file.Kind, file.Mode = FileSpecial, gitMode(before.Mode())
		file.Issues = append(file.Issues, FileIssueSpecial)
		file.ContentAssessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonSnapshotObjectMissing)
		file.PatchAssessment = file.ContentAssessment
		return file, nil, false
	case before.Size() > capturer.limits.MaxFileBytes || before.Size() > remainingBytes:
		return limitedFile(file, before.Size() > capturer.limits.MaxFileBytes), nil, false
	}

	handle, err := openSnapshotFile(root, path)
	if err != nil {
		return file, nil, true
	}
	defer handle.Close()
	opened, err := handle.Stat()
	if err != nil || !stableFileInfo(before, opened) || !opened.Mode().IsRegular() {
		return file, nil, true
	}
	content, err := readBoundedWithContext(ctx, handle, capturer.limits.MaxFileBytes+1)
	if err != nil {
		return file, nil, true
	}
	if capturer.afterFileRead != nil {
		capturer.afterFileRead(path, attempt)
	}
	readInfo, statErr := handle.Stat()
	after, lstatErr := root.Lstat(path)
	if statErr != nil || lstatErr != nil || !stableFileInfo(opened, readInfo) || !stableFileInfo(before, after) {
		return file, nil, true
	}
	if int64(len(content)) > capturer.limits.MaxFileBytes || int64(len(content)) > remainingBytes {
		return limitedFile(file, int64(len(content)) > capturer.limits.MaxFileBytes), nil, false
	}
	file.Kind, file.Mode, file.Size = FileRegular, gitMode(opened.Mode()), int64(len(content))
	file.ContentHash = hashBytes(content)
	if bytes.IndexByte(content, 0) >= 0 {
		file.Binary = true
		file.Issues = append(file.Issues, FileIssueBinary)
		file.PatchAssessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonBinaryPatchUnavailable)
	}
	return file, content, false
}

type boundedReadResult struct {
	content []byte
	err     error
}

func readBoundedWithContext(ctx context.Context, handle *os.File, limit int64) ([]byte, error) {
	completed := make(chan boundedReadResult, 1)
	go func() {
		content, err := io.ReadAll(io.LimitReader(handle, limit))
		completed <- boundedReadResult{content: content, err: err}
	}()
	select {
	case result := <-completed:
		return result.content, result.err
	case <-ctx.Done():
		_ = handle.Close()
		return nil, ctx.Err()
	}
}

func limitedFile(file SnapshotFile, fileLimit bool) SnapshotFile {
	if file.Kind == "" {
		file.Kind = FileRegular
	}
	if fileLimit {
		file.Issues = append(file.Issues, FileIssueOversize)
	} else {
		file.Issues = append(file.Issues, FileIssueSessionLimit)
	}
	file.ContentAssessment = model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonSnapshotLimitExceeded)
	file.PatchAssessment = file.ContentAssessment
	return file
}

func appendUniqueIssue(issues []FileIssueCode, issue FileIssueCode) []FileIssueCode {
	for _, existing := range issues {
		if existing == issue {
			return issues
		}
	}
	return append(issues, issue)
}

func rejectSymlinkParents(root *os.Root, path string) error {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for index := 1; index < len(parts); index++ {
		prefix := strings.Join(parts[:index], "/")
		info, err := root.Lstat(prefix)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe parent")
		}
	}
	return nil
}

func stableFileInfo(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() &&
		left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func gitMode(mode os.FileMode) string {
	if !mode.IsRegular() {
		return ""
	}
	if mode.Perm()&0o111 != 0 {
		return "100755"
	}
	return "100644"
}

func appendUniqueReason(reasons []model.GitEvidenceReasonCode, reason model.GitEvidenceReasonCode) []model.GitEvidenceReasonCode {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func digestManifest(files []SnapshotFile) string {
	digest := sha256.New()
	writeDigestField(digest, []byte("session-insight/git-snapshot-manifest/v1"))
	for _, file := range files {
		writeDigestField(digest, file.PathBytes)
		for _, value := range []string{
			strconv.FormatBool(file.Present), string(file.Kind), file.Mode, file.IndexMode, file.GitOID,
			strconv.FormatInt(file.Size, 10), strconv.FormatInt(file.ModTimeUnixNano, 10),
			file.ContentHash, string(file.ContentAssessment.State), string(file.ContentAssessment.ReasonCode),
		} {
			writeDigestField(digest, []byte(value))
		}
		for _, state := range file.States {
			writeDigestField(digest, []byte(state))
		}
		for _, issue := range file.Issues {
			writeDigestField(digest, []byte(issue))
		}
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeDigestField(writer io.Writer, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}
