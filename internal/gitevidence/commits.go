package gitevidence

import (
	"bytes"
	"context"
	"errors"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bbsteel/session-insight/internal/model"
)

const (
	hardCandidateCommitLimit = 256
	hardCandidatePathLimit   = 500
	hardCandidatePathBytes   = 256 << 10
	hardCandidateWindow      = 31 * 24 * time.Hour
	hardCandidateTimeout     = 10 * time.Second
	hardCandidateLogLimit    = hardCandidateCommitLimit + 1
	commitLogFormat          = "format:%H%x00%s%x00%an%x00%aI%x00%cI%x00"
)

// CandidateCommitLimits are caller-selectable only below fixed process and
// query ceilings. Tests may lower them; production callers cannot expand the
// local-history surface beyond the hard caps above.
type CandidateCommitLimits struct {
	MaxCandidates   int
	MaxChangedPaths int
	MaxWindow       time.Duration
	Timeout         time.Duration
}

func DefaultCandidateCommitLimits() CandidateCommitLimits {
	return CandidateCommitLimits{
		MaxCandidates:   128,
		MaxChangedPaths: hardCandidatePathLimit,
		MaxWindow:       hardCandidateWindow,
		Timeout:         5 * time.Second,
	}
}

// CandidateCommitPath carries the stable file key separately from the exact
// repository-relative Git path used for the closed literal path query.
type CandidateCommitPath struct {
	Key  string
	Path string
}

// CandidateCommitInput fixes candidate discovery to one server-side binding,
// local object IDs and a bounded observation window. BaselineSHA is preferred;
// OriginSHA is used when no baseline exists or its object has disappeared.
type CandidateCommitInput struct {
	Binding      model.GitRepositoryBinding
	OriginSHA    string
	BaselineSHA  string
	FinalSHA     string
	ChangedPaths []CandidateCommitPath
	WindowStart  time.Time
	WindowEnd    time.Time
}

// CandidateCommitResult separates discovery completeness from each commit's
// relation precision. An exact descendant relation proves topology only; it
// never claims that the Session caused or authored the commit.
type CandidateCommitResult struct {
	Commits    []model.GitCandidateCommit
	Assessment model.GitEvidenceAssessment
}

type CandidateCommitDiscoverer struct {
	runner *Runner
	limits CandidateCommitLimits
}

func NewCandidateCommitDiscoverer(runner *Runner, limits CandidateCommitLimits) (*CandidateCommitDiscoverer, error) {
	if runner == nil || limits.MaxCandidates <= 0 || limits.MaxCandidates > hardCandidateCommitLimit ||
		limits.MaxChangedPaths <= 0 || limits.MaxChangedPaths > hardCandidatePathLimit ||
		limits.MaxWindow <= 0 || limits.MaxWindow > hardCandidateWindow ||
		limits.Timeout <= 0 || limits.Timeout > hardCandidateTimeout {
		return nil, &Error{Code: ErrorInvalidConfig}
	}
	return &CandidateCommitDiscoverer{runner: runner, limits: limits}, nil
}

// Discover ranks exact ancestry first, then supplements it with literal
// changed-path overlap and the bounded time window. When the boundary cannot
// be used, the same heuristic relations carry that typed degradation. Path and
// time association always remains estimated.
func (discoverer *CandidateCommitDiscoverer) Discover(ctx context.Context, repository *Repository, input CandidateCommitInput) (CandidateCommitResult, error) {
	result := CandidateCommitResult{Commits: []model.GitCandidateCommit{}, Assessment: model.ExactGitEvidence()}
	paths, err := discoverer.validateInput(repository, input)
	if err != nil {
		result.Assessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonGitCommandFailed)
		return result, err
	}
	discoveryContext, cancel := context.WithTimeout(ctx, discoverer.limits.Timeout)
	defer cancel()

	finalExists, err := discoverer.runner.commitObjectExists(discoveryContext, repository.WorktreeRoot, input.FinalSHA)
	if err != nil {
		return candidateRunnerFailure(result, err)
	}
	if !finalExists {
		result.Assessment = model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonSnapshotObjectMissing)
		return result, nil
	}

	boundary := input.BaselineSHA
	fallbackReason := model.GitEvidenceReasonCode("")
	if boundary == "" {
		boundary = input.OriginSHA
	}
	if boundary == "" {
		fallbackReason = model.ReasonBaselineNotCaptured
	} else {
		boundaryExists, boundaryErr := discoverer.runner.commitObjectExists(discoveryContext, repository.WorktreeRoot, boundary)
		if boundaryErr != nil {
			return candidateRunnerFailure(result, boundaryErr)
		}
		if !boundaryExists && input.BaselineSHA != "" && input.OriginSHA != "" && input.OriginSHA != input.BaselineSHA {
			originExists, originErr := discoverer.runner.commitObjectExists(discoveryContext, repository.WorktreeRoot, input.OriginSHA)
			if originErr != nil {
				return candidateRunnerFailure(result, originErr)
			}
			if originExists {
				boundary = input.OriginSHA
				boundaryExists = true
				fallbackReason = model.ReasonSnapshotObjectMissing
			}
		}
		if !boundaryExists {
			fallbackReason = model.ReasonSnapshotObjectMissing
			boundary = ""
		}
	}

	seen := make(map[string]bool, discoverer.limits.MaxCandidates)
	if boundary != "" {
		ancestor, ancestryErr := discoverer.runner.commitIsAncestor(discoveryContext, repository.WorktreeRoot, boundary, input.FinalSHA)
		if ancestryErr != nil {
			return candidateRunnerFailure(result, ancestryErr)
		}
		if ancestor {
			overflow, topologyErr := discoverer.discoverTopology(discoveryContext, repository, boundary, input.FinalSHA, seen, &result)
			if topologyErr != nil {
				return candidateRunnerFailure(result, topologyErr)
			}
			if !overflow {
				heuristicReason := fallbackReason
				if heuristicReason == "" {
					heuristicReason = model.ReasonAgentGitFactMissing
				}
				heuristicOverflow, heuristicErr := discoverer.discoverHeuristics(discoveryContext, repository, input, paths, heuristicReason, seen, &result)
				if heuristicErr != nil {
					return candidateRunnerFailure(result, heuristicErr)
				}
				overflow = heuristicOverflow
			}
			result.Assessment = candidateAssessment(fallbackReason, overflow)
			return result, nil
		}
		fallbackReason = model.ReasonHeadHistoryRewritten
	}

	overflow, err := discoverer.discoverHeuristics(discoveryContext, repository, input, paths, fallbackReason, seen, &result)
	if err != nil {
		return candidateRunnerFailure(result, err)
	}
	result.Assessment = candidateAssessment(fallbackReason, overflow)
	return result, nil
}

func (discoverer *CandidateCommitDiscoverer) validateInput(repository *Repository, input CandidateCommitInput) ([]string, error) {
	invalid := func() ([]string, error) {
		return nil, &Error{Code: ErrorInvalidInput, Operation: OperationCommitAncestry}
	}
	if discoverer == nil || discoverer.runner == nil || repository == nil {
		return invalid()
	}
	if repository.ObjectFormat != ObjectFormatSHA1 && repository.ObjectFormat != ObjectFormatSHA256 {
		return invalid()
	}
	// The private administrative paths are populated only by Resolver. This
	// prevents package-external callers from reconstructing a repository query
	// from client-visible binding fields and an arbitrary worktree path.
	if repository.gitDir == "" || repository.commonDir == "" {
		return invalid()
	}
	binding := input.Binding
	if strings.TrimSpace(binding.RepositoryEntryKey) == "" || strings.TrimSpace(binding.RepositoryEntryKey) != binding.RepositoryEntryKey || strings.ContainsRune(binding.RepositoryEntryKey, '\x00') || len(binding.RepositoryEntryKey) > 512 || !utf8.ValidString(binding.RepositoryEntryKey) ||
		binding.WorktreeRoot != repository.WorktreeRoot || binding.CommonRootID != repository.CommonRootID || binding.WorktreeID != repository.WorktreeID {
		return invalid()
	}
	if !validObjectID(input.FinalSHA, repository.ObjectFormat) ||
		(input.BaselineSHA != "" && !validObjectID(input.BaselineSHA, repository.ObjectFormat)) ||
		(input.OriginSHA != "" && !validObjectID(input.OriginSHA, repository.ObjectFormat)) {
		return invalid()
	}
	if input.WindowStart.IsZero() || input.WindowEnd.IsZero() || input.WindowEnd.Before(input.WindowStart) || input.WindowEnd.Sub(input.WindowStart) > discoverer.limits.MaxWindow {
		return invalid()
	}
	if len(input.ChangedPaths) > discoverer.limits.MaxChangedPaths {
		return invalid()
	}
	stablePaths := append([]CandidateCommitPath(nil), input.ChangedPaths...)
	sort.SliceStable(stablePaths, func(i, j int) bool {
		if stablePaths[i].Key != stablePaths[j].Key {
			return stablePaths[i].Key < stablePaths[j].Key
		}
		return stablePaths[i].Path < stablePaths[j].Path
	})
	seenKeys := make(map[string]bool, len(stablePaths))
	seenPaths := make(map[string]bool, len(stablePaths))
	paths := make([]string, 0, len(stablePaths))
	totalBytes := 0
	for _, changed := range stablePaths {
		if strings.TrimSpace(changed.Key) == "" || strings.TrimSpace(changed.Key) != changed.Key || strings.ContainsRune(changed.Key, '\x00') || len(changed.Key) > 512 || !utf8.ValidString(changed.Key) || seenKeys[changed.Key] {
			return invalid()
		}
		seenKeys[changed.Key] = true
		if !validCandidatePath(changed.Path) {
			return invalid()
		}
		totalBytes += len(changed.Path)
		if totalBytes > hardCandidatePathBytes {
			return invalid()
		}
		if !seenPaths[changed.Path] {
			paths = append(paths, changed.Path)
			seenPaths[changed.Path] = true
		}
	}
	return paths, nil
}

func validCandidatePath(value string) bool {
	if value == "" || strings.ContainsRune(value, '\x00') || len(value) > 4096 || strings.HasPrefix(value, "/") {
		return false
	}
	cleaned := path.Clean(value)
	return cleaned == value && cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}

func (discoverer *CandidateCommitDiscoverer) discoverTopology(ctx context.Context, repository *Repository, boundary, final string, seen map[string]bool, result *CandidateCommitResult) (bool, error) {
	request := commitLogRequest{
		mode: commitLogFirstParent, boundary: boundary, final: final,
		limit: discoverer.limits.MaxCandidates + 1,
	}
	records, err := discoverer.runCommitLog(ctx, repository, request)
	if err != nil {
		return false, err
	}
	overflow := appendCandidateRecords(result, seen, records, discoverer.limits.MaxCandidates, model.GitCommitDescendant, model.ExactGitEvidence())
	if overflow {
		return true, nil
	}
	request.mode = commitLogAncestry
	records, err = discoverer.runCommitLog(ctx, repository, request)
	if err != nil {
		return false, err
	}
	overflow = appendCandidateRecords(result, seen, records, discoverer.limits.MaxCandidates, model.GitCommitDescendant, model.ExactGitEvidence())
	return overflow, nil
}

func (discoverer *CandidateCommitDiscoverer) discoverHeuristics(ctx context.Context, repository *Repository, input CandidateCommitInput, paths []string, reason model.GitEvidenceReasonCode, seen map[string]bool, result *CandidateCommitResult) (bool, error) {
	assessment := model.NonExactGitEvidence(model.GitEvidenceEstimated, reason)
	if reason != model.ReasonAgentGitFactMissing {
		assessment = model.NonExactGitEvidence(model.GitEvidenceEstimated, reason, model.ReasonAgentGitFactMissing)
	}
	overflow := false
	if len(paths) != 0 {
		records, err := discoverer.runCommitLog(ctx, repository, commitLogRequest{
			mode: commitLogPaths, final: input.FinalSHA, paths: paths,
			since: input.WindowStart, until: input.WindowEnd, limit: discoverer.limits.MaxCandidates + 1,
		})
		if err != nil {
			return false, err
		}
		overflow = appendCandidateRecords(result, seen, records, discoverer.limits.MaxCandidates, model.GitCommitPathOverlap, assessment)
	}
	records, err := discoverer.runCommitLog(ctx, repository, commitLogRequest{
		mode: commitLogWindow, final: input.FinalSHA,
		since: input.WindowStart, until: input.WindowEnd, limit: discoverer.limits.MaxCandidates + 1,
	})
	if err != nil {
		return false, err
	}
	if appendCandidateRecords(result, seen, records, discoverer.limits.MaxCandidates, model.GitCommitTimeWindow, assessment) {
		overflow = true
	}
	return overflow, nil
}

func (discoverer *CandidateCommitDiscoverer) runCommitLog(ctx context.Context, repository *Repository, request commitLogRequest) ([]gitCommitRecord, error) {
	raw, err := discoverer.runner.commitLog(ctx, repository.WorktreeRoot, request)
	if err != nil {
		return nil, err
	}
	return parseCommitLog(raw.Stdout, repository.ObjectFormat, request.operation())
}

func appendCandidateRecords(result *CandidateCommitResult, seen map[string]bool, records []gitCommitRecord, limit int, relation model.GitCandidateCommitRelation, assessment model.GitEvidenceAssessment) bool {
	overflow := false
	for _, record := range records {
		if seen[record.sha] {
			continue
		}
		seen[record.sha] = true
		if len(result.Commits) >= limit {
			overflow = true
			continue
		}
		result.Commits = append(result.Commits, model.GitCandidateCommit{
			Ordinal: len(result.Commits), SHA: record.sha, Subject: record.subject,
			AuthorName: record.authorName, AuthoredAt: record.authoredAt, CommittedAt: record.committedAt,
			Relation: relation, Assessment: assessment, Evidence: []model.GitEvidenceLink{},
		})
	}
	return overflow
}

func candidateAssessment(reason model.GitEvidenceReasonCode, overflow bool) model.GitEvidenceAssessment {
	if reason == "" && !overflow {
		return model.ExactGitEvidence()
	}
	if reason == "" {
		return model.NonExactGitEvidence(model.GitEvidenceEstimated, model.ReasonSnapshotLimitExceeded)
	}
	if overflow {
		return model.NonExactGitEvidence(model.GitEvidenceEstimated, reason, model.ReasonSnapshotLimitExceeded)
	}
	return model.NonExactGitEvidence(model.GitEvidenceEstimated, reason)
}

func candidateRunnerFailure(result CandidateCommitResult, err error) (CandidateCommitResult, error) {
	reason := model.ReasonGitCommandFailed
	var typed *Error
	if errors.As(err, &typed) {
		reason = typed.EvidenceReason()
	}
	result.Assessment = model.NonExactGitEvidence(model.GitEvidenceUnavailable, reason)
	return result, err
}

type commitLogMode uint8

const (
	commitLogFirstParent commitLogMode = iota + 1
	commitLogAncestry
	commitLogPaths
	commitLogWindow
)

type commitLogRequest struct {
	mode     commitLogMode
	boundary string
	final    string
	paths    []string
	since    time.Time
	until    time.Time
	limit    int
}

func (request commitLogRequest) operation() Operation {
	switch request.mode {
	case commitLogFirstParent:
		return OperationCommitFirst
	case commitLogAncestry:
		return OperationCommitAncestry
	case commitLogPaths:
		return OperationCommitPaths
	case commitLogWindow:
		return OperationCommitWindow
	default:
		return OperationCommitAncestry
	}
}

func (runner *Runner) commitObjectExists(ctx context.Context, cwd, sha string) (bool, error) {
	if !validAnyObjectID(sha) {
		return false, &Error{Code: ErrorInvalidInput, Operation: OperationCommitObject}
	}
	argv := append(fixedPrefix(), "cat-file", "-e", sha+"^{commit}")
	_, err := runner.runArgs(ctx, cwd, OperationCommitObject, argv)
	if err == nil {
		return true, nil
	}
	var typed *Error
	if errors.As(err, &typed) && typed.Code == ErrorCommandFailed && (typed.ExitCode == 1 || typed.ExitCode == 128) {
		return false, nil
	}
	return false, err
}

func (runner *Runner) commitIsAncestor(ctx context.Context, cwd, boundary, final string) (bool, error) {
	if !validAnyObjectID(boundary) || !validAnyObjectID(final) || len(boundary) != len(final) {
		return false, &Error{Code: ErrorInvalidInput, Operation: OperationCommitAncestry}
	}
	argv := append(fixedPrefix(), "merge-base", "--is-ancestor", boundary, final)
	_, err := runner.runArgs(ctx, cwd, OperationCommitAncestry, argv)
	if err == nil {
		return true, nil
	}
	var typed *Error
	if errors.As(err, &typed) && typed.Code == ErrorCommandFailed && typed.ExitCode == 1 {
		return false, nil
	}
	return false, err
}

func (runner *Runner) commitLog(ctx context.Context, cwd string, request commitLogRequest) (Result, error) {
	operation := request.operation()
	if request.limit <= 0 || request.limit > hardCandidateLogLimit || !validAnyObjectID(request.final) {
		return Result{}, &Error{Code: ErrorInvalidInput, Operation: operation}
	}
	argv := append(fixedPrefix(),
		"log", "--no-color", "--no-decorate", "--no-show-signature", "--no-ext-diff", "--no-textconv",
		"--format="+commitLogFormat, "--max-count="+strconv.Itoa(request.limit),
	)
	switch request.mode {
	case commitLogFirstParent, commitLogAncestry:
		if !validAnyObjectID(request.boundary) || len(request.boundary) != len(request.final) {
			return Result{}, &Error{Code: ErrorInvalidInput, Operation: operation}
		}
		argv = append(argv, "--topo-order", "--ancestry-path")
		if request.mode == commitLogFirstParent {
			argv = append(argv, "--first-parent")
		}
		argv = append(argv, request.boundary+".."+request.final, "--")
	case commitLogPaths, commitLogWindow:
		if request.since.IsZero() || request.until.IsZero() || request.until.Before(request.since) || request.until.Sub(request.since) > hardCandidateWindow {
			return Result{}, &Error{Code: ErrorInvalidInput, Operation: operation}
		}
		argv = append(argv,
			"--date-order", "--full-history",
			"--since=@"+strconv.FormatInt(request.since.Unix(), 10),
			"--until=@"+strconv.FormatInt(request.until.Unix(), 10),
			request.final, "--",
		)
		if request.mode == commitLogPaths {
			if len(request.paths) == 0 || len(request.paths) > hardCandidatePathLimit {
				return Result{}, &Error{Code: ErrorInvalidInput, Operation: operation}
			}
			totalBytes := 0
			for _, candidatePath := range request.paths {
				if !validCandidatePath(candidatePath) {
					return Result{}, &Error{Code: ErrorInvalidInput, Operation: operation}
				}
				totalBytes += len(candidatePath)
				if totalBytes > hardCandidatePathBytes {
					return Result{}, &Error{Code: ErrorInvalidInput, Operation: operation}
				}
			}
			argv = append(argv, request.paths...)
		}
	default:
		return Result{}, &Error{Code: ErrorInvalidInput, Operation: operation}
	}
	return runner.runArgs(ctx, cwd, operation, argv)
}

func validAnyObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9') && !(char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

type gitCommitRecord struct {
	sha         string
	subject     string
	authorName  string
	authoredAt  *time.Time
	committedAt *time.Time
}

func parseCommitLog(raw []byte, format ObjectFormat, operation Operation) ([]gitCommitRecord, error) {
	if len(raw) == 0 {
		return []gitCommitRecord{}, nil
	}
	fields := bytes.Split(raw, []byte{0})
	if len(fields) < 6 || (len(fields)-1)%5 != 0 {
		return nil, &Error{Code: ErrorMalformedOutput, Operation: operation}
	}
	trailer := fields[len(fields)-1]
	if len(trailer) != 0 && !bytes.Equal(trailer, []byte("\n")) {
		return nil, &Error{Code: ErrorMalformedOutput, Operation: operation}
	}
	records := make([]gitCommitRecord, 0, (len(fields)-1)/5)
	for index := 0; index < len(fields)-1; index += 5 {
		shaBytes := fields[index]
		if index != 0 {
			if len(shaBytes) == 0 || shaBytes[0] != '\n' {
				return nil, &Error{Code: ErrorMalformedOutput, Operation: operation}
			}
			shaBytes = shaBytes[1:]
		}
		sha := string(shaBytes)
		if !validObjectID(sha, format) {
			return nil, &Error{Code: ErrorMalformedOutput, Operation: operation}
		}
		authoredAt, err := parseCommitTime(fields[index+3])
		if err != nil {
			return nil, &Error{Code: ErrorMalformedOutput, Operation: operation, Cause: err}
		}
		committedAt, err := parseCommitTime(fields[index+4])
		if err != nil {
			return nil, &Error{Code: ErrorMalformedOutput, Operation: operation, Cause: err}
		}
		subject := sanitizeGitDisplay(fields[index+1])
		if subject == "" {
			subject = sha
		}
		records = append(records, gitCommitRecord{
			sha: sha, subject: subject, authorName: sanitizeGitDisplay(fields[index+2]),
			authoredAt: authoredAt, committedAt: committedAt,
		})
	}
	return records, nil
}

func parseCommitTime(raw []byte) (*time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, string(raw))
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func sanitizeGitDisplay(raw []byte) string {
	value := string(raw)
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "\uFFFD")
	}
	value = strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return ' '
		}
		return char
	}, value)
	return strings.Join(strings.Fields(value), " ")
}
