package changehost

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func decodeProviderJSON(body []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("provider response contains trailing JSON")
		}
		return err
	}
	return nil
}

func providerOpaqueKey(prefix string, parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(hash, "%d:", len(part))
		_, _ = hash.Write([]byte(part))
	}
	return prefix + "-" + hex.EncodeToString(hash.Sum(nil))
}

func providerContentKey(provider model.ChangeProviderKind, objectID, nativeVersion, base, diffBase, head, manifest string) model.ContentVersionKey {
	return model.ContentVersionKey(providerOpaqueKey(
		string(provider)+"-content", objectID, nativeVersion, base, diffBase, head, manifest,
	))
}

func providerFileKey(provider model.ChangeProviderKind, objectID, oldPath, newPath string) string {
	return providerOpaqueKey(string(provider)+"-file", objectID, oldPath, newPath)
}

func providerMetadataRevision(parts ...string) string {
	return providerOpaqueKey("metadata", parts...)
}

func safeProviderText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && !strings.ContainsRune(value, '\x00')
}

func safeProviderPath(value string) bool {
	if !safeProviderText(value, 4096) || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func providerURLMatchesOrigin(rawURL, origin string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Host == "" {
		return false
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String() == origin
}

func parseProviderTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}

func metadataOnlyCompleteness() model.ChangeRequestCompleteness {
	missing := model.NonExactGitEvidence(model.GitEvidenceUnavailable, model.ReasonChangeRequestPartial)
	return model.ChangeRequestCompleteness{
		Metadata: model.ExactGitEvidence(), FileSet: missing, Patches: missing,
		Modes: missing, Commits: missing,
	}
}

func aggregateResultMetadata(items ...ResultMetadata) ResultMetadata {
	result := ResultMetadata{Assessment: model.ExactGitEvidence()}
	for _, item := range items {
		result.PageCount += item.PageCount
		result.ItemCount += item.ItemCount
		result.BytesRead += item.BytesRead
		if item.Assessment.State != model.GitEvidenceExact {
			result.Assessment = item.Assessment
		}
		if item.RetryAfterSeconds != nil {
			value := *item.RetryAfterSeconds
			result.RetryAfterSeconds = &value
		}
		if item.RateLimit != nil {
			result.RateLimit = item.RateLimit
		}
	}
	return result
}

func providerError(operation Operation, code ErrorCode, cause error) error {
	return &Error{Code: code, Operation: operation, Cause: cause}
}

func lifecycleFromProvider(state string, merged bool) model.ChangeRequestLifecycleState {
	if merged || state == "merged" {
		return model.ChangeLifecycleMerged
	}
	switch state {
	case "open", "opened", "reopened":
		return model.ChangeLifecycleOpen
	case "closed", "locked":
		return model.ChangeLifecycleClosed
	default:
		return model.ChangeLifecycleUnknown
	}
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
