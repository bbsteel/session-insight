package worktreereview

import "github.com/bbsteel/session-insight/internal/presentation"

// Presentation returns the adapter-owned terminal presentation. There is no
// evidence-backed customization yet, so every feature is explicit and
// neutral.
func Presentation() presentation.Declaration {
	return presentation.NativeNeutralDeclaration(AgentType)
}

// PresentationMigrationState is empty: no legacy custom profile exists.
func PresentationMigrationState() presentation.MigrationState {
	return ""
}
