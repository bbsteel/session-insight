package opencode

import "github.com/bbsteel/session-insight/internal/presentation"

// Presentation returns OpenCode's adapter-owned terminal presentation.
// There is no evidence-backed customization in Slice B, so every feature
// and dimension is explicit and neutral.
func Presentation() presentation.Declaration {
	return presentation.NativeNeutralDeclaration("opencode")
}

// PresentationMigrationState is empty: OpenCode has no legacy custom profile.
func PresentationMigrationState() presentation.MigrationState {
	return ""
}
