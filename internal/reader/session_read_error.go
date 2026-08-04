package reader

import (
	"github.com/bbsteel/session-insight/internal/reader/readerr"
)

// Re-export typed read error kinds for indexer/server convenience.
type SessionReadErrorKind = readerr.Kind

const (
	ReadSourceMissing     = readerr.SourceMissing
	ReadSourceUnreadable  = readerr.SourceUnreadable
	ReadFormatUnsupported = readerr.FormatUnsupported
	ReadMetadataOnly      = readerr.MetadataOnly
	ReadParseFailed       = readerr.ParseFailed
)

// SessionReadError is an alias of readerr.Error.
type SessionReadError = readerr.Error

// AsSessionReadError extracts a typed read error.
func AsSessionReadError(err error) (*SessionReadError, bool) {
	return readerr.As(err)
}

// NewSessionReadError constructs a typed read failure.
func NewSessionReadError(kind SessionReadErrorKind, reason string, err error) *SessionReadError {
	return readerr.New(kind, reason, err)
}
