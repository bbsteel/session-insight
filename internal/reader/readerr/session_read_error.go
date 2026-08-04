// Package readerr is a dependency-leaf for typed session read failures.
// Concrete adapters import this package; the parent reader package re-exports
// helpers so indexer/server can use errors.As without cycles.
package readerr

import (
	"errors"
	"fmt"

	"github.com/bbsteel/session-insight/internal/model"
)

// Kind classifies structured session read failures.
type Kind string

const (
	SourceMissing     Kind = "source_missing"
	SourceUnreadable  Kind = "source_unreadable"
	FormatUnsupported Kind = "format_unsupported"
	MetadataOnly      Kind = "metadata_only"
	ParseFailed       Kind = "parse_failed"
)

// Error is a typed failure from a reader. Err is for logs and errors.Is/As only.
type Error struct {
	Kind       Kind
	ReasonCode string
	Sources    []model.SessionSourceFile
	Warnings   []model.ParseWarning
	Err        error
}

func (e *Error) Error() string {
	if e == nil {
		return "session read error"
	}
	if e.Err != nil {
		return fmt.Sprintf("session read %s: %v", e.Kind, e.Err)
	}
	if e.ReasonCode != "" {
		return fmt.Sprintf("session read %s (%s)", e.Kind, e.ReasonCode)
	}
	return fmt.Sprintf("session read %s", e.Kind)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// As extracts *Error from err when present.
func As(err error) (*Error, bool) {
	var sre *Error
	if errors.As(err, &sre) {
		return sre, true
	}
	return nil, false
}

// New constructs a typed read failure.
func New(kind Kind, reason string, err error) *Error {
	return &Error{Kind: kind, ReasonCode: reason, Err: err}
}
