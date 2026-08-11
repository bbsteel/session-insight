package gitevidence

import (
	"errors"
	"fmt"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

type FileKind string

const (
	FileRegular FileKind = "regular"
	FileSymlink FileKind = "symlink"
	FileGitlink FileKind = "gitlink"
	FileSpecial FileKind = "special"
	FileMissing FileKind = "missing"
)

type PathState string

const (
	PathTracked   PathState = "tracked"
	PathStaged    PathState = "staged"
	PathUnstaged  PathState = "unstaged"
	PathUntracked PathState = "untracked"
	PathRemoved   PathState = "removed"
	PathUnmerged  PathState = "unmerged"
)

type FileIssueCode string

const (
	FileIssueBinary       FileIssueCode = "binary"
	FileIssueSymlink      FileIssueCode = "symlink"
	FileIssueGitlink      FileIssueCode = "gitlink"
	FileIssueSpecial      FileIssueCode = "special_file"
	FileIssueOversize     FileIssueCode = "file_oversize"
	FileIssueSessionLimit FileIssueCode = "session_content_limit"
	FileIssueRaced        FileIssueCode = "file_raced"
	FileIssueUnsafePath   FileIssueCode = "unsafe_path"
	FileIssueRemoved      FileIssueCode = "removed"
	FileIssueUnmerged     FileIssueCode = "unmerged"
)

type ContentBlob struct {
	SHA256 string
	Bytes  []byte
}

// SnapshotFile is storage-private capture data. PathBytes preserves Git's raw
// path bytes; DisplayPath is safe UTF-8 for diagnostics only and must never be
// used for filesystem access.
type SnapshotFile struct {
	Ordinal           int
	Key               string
	PathBytes         []byte
	DisplayPath       string
	PathEncoding      model.GitPathEncoding
	Present           bool
	Kind              FileKind
	States            []PathState
	Mode              string
	IndexMode         string
	GitOID            string
	Size              int64
	ModTimeUnixNano   int64
	ContentHash       string
	ContentRef        string
	Binary            bool
	Issues            []FileIssueCode
	ContentAssessment model.GitEvidenceAssessment
	PatchAssessment   model.GitEvidenceAssessment
}

type Snapshot struct {
	SnapshotID         string
	Kind               model.GitSnapshotKind
	SourceRevision     string
	WorktreeID         string
	HeadSHA            string
	IndexDigest        string
	StatusDigest       string
	ManifestDigest     string
	CaptureStartedAt   time.Time
	CaptureCompletedAt time.Time
	Assessment         model.GitEvidenceAssessment
	Files              []SnapshotFile
	Contents           []ContentBlob
}

type CaptureRequest struct {
	SnapshotID     string
	Kind           model.GitSnapshotKind
	SourceRevision string
}

type CaptureLimits struct {
	Timeout         time.Duration
	Attempts        int
	FileAttempts    int
	MaxPaths        int
	MaxPathBytes    int
	MaxFileBytes    int64
	MaxSessionBytes int64
}

func DefaultCaptureLimits() CaptureLimits {
	return CaptureLimits{
		Timeout:         20 * time.Second,
		Attempts:        3,
		FileAttempts:    3,
		MaxPaths:        500,
		MaxPathBytes:    4096,
		MaxFileBytes:    1 << 20,
		MaxSessionBytes: 20 << 20,
	}
}

type CaptureConfig struct {
	Limits CaptureLimits
}

type CaptureErrorCode string

const (
	CaptureErrorInvalidConfig   CaptureErrorCode = "invalid_config"
	CaptureErrorInvalidRequest  CaptureErrorCode = "invalid_request"
	CaptureErrorGit             CaptureErrorCode = "git_failed"
	CaptureErrorLimit           CaptureErrorCode = "snapshot_limit_exceeded"
	CaptureErrorRaced           CaptureErrorCode = "capture_raced"
	CaptureErrorMalformed       CaptureErrorCode = "malformed_git_state"
	CaptureErrorUnsafePath      CaptureErrorCode = "unsafe_path"
	CaptureErrorIdentityChanged CaptureErrorCode = "worktree_identity_changed"
)

type CaptureError struct {
	Code  CaptureErrorCode
	Cause error
}

func (e *CaptureError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("Git snapshot capture failed: %s", e.Code)
}

func (e *CaptureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func captureReason(err error) model.GitEvidenceReasonCode {
	var captureErr *CaptureError
	if errors.As(err, &captureErr) {
		switch captureErr.Code {
		case CaptureErrorLimit:
			return model.ReasonSnapshotLimitExceeded
		case CaptureErrorRaced:
			return model.ReasonCaptureRaced
		case CaptureErrorUnsafePath:
			return model.ReasonWorktreeIdentityChanged
		case CaptureErrorIdentityChanged:
			return model.ReasonWorktreeIdentityChanged
		}
	}
	var runnerErr *Error
	if errors.As(err, &runnerErr) {
		return runnerErr.EvidenceReason()
	}
	return model.ReasonGitCommandFailed
}

type CaptureResult struct {
	Snapshot   *Snapshot
	Assessment model.GitEvidenceAssessment
}

type NetFileChange struct {
	Ordinal          int
	Key              string
	PathBytes        []byte
	DisplayPath      string
	PathEncoding     model.GitPathEncoding
	Status           model.GitFileStatus
	Before           *SnapshotFile
	After            *SnapshotFile
	StatusAssessment model.GitEvidenceAssessment
	PatchAssessment  model.GitEvidenceAssessment
}

type NetDiff struct {
	BaselineManifest string
	FinalManifest    string
	Assessment       model.GitEvidenceAssessment
	Files            []NetFileChange
}
