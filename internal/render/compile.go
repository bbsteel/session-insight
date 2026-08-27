package render

import "github.com/bbsteel/session-insight/internal/presentation"

// CompilePresentation maps a validated resolved presentation onto the
// immutable formatter Profile. Slice B only guarantees that a fully-neutral
// resolved presentation compiles to the historical default profile used by
// Agents without a dedicated style.
//
// Custom primitives and the production wiring that would replace profileFor
// are Slice C/F. Unknown or custom layout still returns the default profile
// so a half-initialized Style can never reach the formatter.
func CompilePresentation(resolved presentation.Resolved) *Profile {
	p := defaultProfile
	return &p
}
