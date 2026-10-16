package pi

import "github.com/bbsteel/session-insight/internal/presentation"

// Presentation returns Pi's adapter-owned terminal presentation. There is no
// evidence-backed customization yet, so every feature and dimension is
// explicit and neutral.
func Presentation() presentation.Declaration {
	return presentation.NativeNeutralDeclaration(agentType)
}

// PresentationMigrationState is empty: Pi has no legacy custom profile.
func PresentationMigrationState() presentation.MigrationState {
	return ""
}
