package imported

import "github.com/bbsteel/session-insight/internal/presentation"

// Presentation returns the fixed non-native declaration for imported
// snapshots. Original Agent names inside a bundle cannot select a native
// profile; imported is always independently runnable as neutral.v1.
func Presentation() presentation.Declaration {
	return presentation.NonNativeDeclaration(AgentType)
}

// PresentationMigrationState is empty: imported never had a native profile.
func PresentationMigrationState() presentation.MigrationState {
	return ""
}
