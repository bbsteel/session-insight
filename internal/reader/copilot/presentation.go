package copilot

import "github.com/bbsteel/session-insight/internal/presentation"

// Presentation returns Copilot's adapter-owned terminal presentation.
// There is no evidence-backed customization in Slice B, so every feature
// and dimension is explicit and neutral.
func Presentation() presentation.Declaration {
	return presentation.NativeNeutralDeclaration("copilot")
}

// PresentationMigrationState is empty: Copilot has no legacy custom profile.
func PresentationMigrationState() presentation.MigrationState {
	return ""
}
