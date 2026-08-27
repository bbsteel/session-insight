package hermes

import "github.com/bbsteel/session-insight/internal/presentation"

// Presentation returns Hermes Agent's adapter-owned terminal presentation.
// There is no evidence-backed customization in Slice B, so every feature
// and dimension is explicit and neutral.
func Presentation() presentation.Declaration {
	return presentation.NativeNeutralDeclaration(agentType)
}

// PresentationMigrationState is empty: Hermes has no legacy custom profile.
func PresentationMigrationState() presentation.MigrationState {
	return ""
}
