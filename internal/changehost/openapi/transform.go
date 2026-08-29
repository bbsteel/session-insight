package openapi

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// transform.go: restricted JSON Pointer evaluation and the fixed transform
// set (design §5.5). Transforms are total functions over scalar values; a
// shape mismatch is a drift signal, never a silent coercion.

// SelectorError marks a field path or value that stopped matching the
// profile's declared shape — the schema-drift signal (design §8).
type SelectorError struct {
	Pointer string
	Detail  string
}

func (e *SelectorError) Error() string {
	return fmt.Sprintf("field selector %s failed: %s", e.Pointer, e.Detail)
}

// EvalPointer resolves a restricted JSON Pointer against a decoded response
// value. "" selects the root. Missing paths and non-scalar targets error.
func EvalPointer(document any, pointer string) (any, error) {
	if pointer == "" {
		return document, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, &SelectorError{Pointer: pointer, Detail: "pointer must start with /"}
	}
	current := document
	for _, rawSegment := range strings.Split(pointer[1:], "/") {
		segment := strings.ReplaceAll(strings.ReplaceAll(rawSegment, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, &SelectorError{Pointer: pointer, Detail: "path traverses a non-object value"}
		}
		current, ok = object[segment]
		if !ok {
			return nil, &SelectorError{Pointer: pointer, Detail: "path not present"}
		}
	}
	return current, nil
}

// EvalSelector extracts and converts one standard field. A nil transform
// passes the raw scalar through as a string.
func EvalSelector(document any, selector FieldSelector) (string, error) {
	value, err := EvalPointer(document, selector.Pointer)
	if err != nil {
		return "", err
	}
	if selector.Transform == nil {
		return scalarString(value, selector.Pointer)
	}
	return ApplyTransform(value, *selector.Transform, selector.Pointer)
}

// EvalItemsSelector evaluates a list response: itemsPointer locates the
// array ("" = root array) and each field selector applies per element.
func EvalItems(document any, itemsPointer string) ([]any, error) {
	value, err := EvalPointer(document, itemsPointer)
	if err != nil {
		return nil, err
	}
	items, ok := value.([]any)
	if !ok {
		return nil, &SelectorError{Pointer: itemsPointer, Detail: "items value is not an array"}
	}
	return items, nil
}

// ApplyTransform converts one scalar value through a fixed transform.
func ApplyTransform(value any, transform FieldTransform, pointer string) (string, error) {
	fail := func(detail string) (string, error) {
		return "", &SelectorError{Pointer: pointer, Detail: detail}
	}
	switch transform.Name {
	case TransformString:
		return scalarString(value, pointer)
	case TransformIntegerToStr:
		number, ok := scalarNumber(value)
		if !ok {
			return fail("expected an integer-compatible value")
		}
		return number, nil
	case TransformBoolean:
		switch typed := value.(type) {
		case bool:
			return strconv.FormatBool(typed), nil
		case string:
			if typed == "true" || typed == "false" {
				return typed, nil
			}
		}
		return fail("expected a boolean value")
	case TransformRFC3339Time:
		text, ok := value.(string)
		if !ok {
			return fail("expected an RFC3339 timestamp string")
		}
		parsed, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return fail("value is not RFC3339")
		}
		return parsed.UTC().Format(time.RFC3339), nil
	case TransformUnixTime:
		number, ok := numericValue(value)
		if !ok {
			return fail("expected a unix timestamp number")
		}
		return time.Unix(int64(number), 0).UTC().Format(time.RFC3339), nil
	case TransformLowercase:
		text, err := scalarString(value, pointer)
		if err != nil {
			return "", err
		}
		return strings.ToLower(text), nil
	case TransformCoalesce:
		if value == nil {
			return "", nil
		}
		return scalarString(value, pointer)
	case TransformEnumMap:
		text, err := scalarString(value, pointer)
		if err != nil {
			return "", err
		}
		mapped, ok := transform.Mapping[text]
		if !ok {
			return fail("value has no enum mapping")
		}
		return mapped, nil
	case TransformGitSHA:
		text, err := scalarString(value, pointer)
		if err != nil {
			return "", err
		}
		if !gitSHAPattern.MatchString(text) {
			return fail("value is not a git object ID")
		}
		return text, nil
	case TransformRepositorySlug:
		text, err := scalarString(value, pointer)
		if err != nil {
			return "", err
		}
		text = strings.TrimSuffix(strings.TrimPrefix(text, "/"), ".git")
		if text == "" || strings.ContainsAny(text, "\x00") {
			return fail("value is not a repository slug")
		}
		return text, nil
	case TransformChangeStatus:
		text, err := scalarString(value, pointer)
		if err != nil {
			return "", err
		}
		return mapChangeStatus(text), nil
	case TransformFileStatus:
		text, err := scalarString(value, pointer)
		if err != nil {
			return "", err
		}
		mapped, ok := mapFileStatus(text)
		if !ok {
			return fail("value has no standard file status")
		}
		return mapped, nil
	default:
		return fail("unknown transform")
	}
}

// mapChangeStatus folds platform lifecycle vocabulary into the standard set;
// unrecognized values degrade to unknown instead of failing the capture.
func mapChangeStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "open", "opened", "reopened", "active", "new", "in_review", "in-review", "reviewing":
		return string(model.ChangeLifecycleOpen)
	case "merged", "merge", "integrated", "accepted", "approved", "landed":
		return string(model.ChangeLifecycleMerged)
	case "closed", "close", "done", "resolved", "completed", "rejected", "declined":
		return string(model.ChangeLifecycleClosed)
	case "abandoned", "abandon", "dropped", "discarded", "withdrawn", "cancelled", "canceled":
		return string(model.ChangeLifecycleAbandoned)
	default:
		return string(model.ChangeLifecycleUnknown)
	}
}

// mapFileStatus folds platform file-change vocabulary into the standard set;
// unrecognized values are a drift signal because every file row must carry a
// valid standard status.
func mapFileStatus(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "added", "add", "new", "created", "a":
		return string(model.GitFileAdded), true
	case "modified", "changed", "updated", "edited", "m":
		return string(model.GitFileModified), true
	case "deleted", "removed", "delete", "d":
		return string(model.GitFileDeleted), true
	case "renamed", "moved", "r":
		return string(model.GitFileRenamed), true
	case "copied", "copy", "c":
		return string(model.GitFileCopied), true
	default:
		return "", false
	}
}

// scalarString renders one scalar without accepting composite values.
func scalarString(value any, pointer string) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10), nil
		}
		return strconv.FormatFloat(typed, 'g', -1, 64), nil
	case bool:
		return strconv.FormatBool(typed), nil
	case nil:
		return "", &SelectorError{Pointer: pointer, Detail: "value is null"}
	default:
		return "", &SelectorError{Pointer: pointer, Detail: "value is not a scalar"}
	}
}

func scalarNumber(value any) (string, bool) {
	switch typed := value.(type) {
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10), true
		}
		return strconv.FormatFloat(typed, 'g', -1, 64), true
	case string:
		if _, err := strconv.ParseFloat(typed, 64); err == nil && typed != "" {
			return typed, true
		}
	}
	return "", false
}

func numericValue(value any) (float64, bool) {
	if typed, ok := value.(float64); ok {
		return typed, true
	}
	return 0, false
}
