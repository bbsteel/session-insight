package claude

import "github.com/bbsteel/session-insight/internal/presentation"

// Presentation returns Claude Code's adapter-owned terminal presentation.
// Slice B keeps every dimension neutral; production still uses the legacy
// profileFor path until Slice F switches this Agent after evidence lands.
func Presentation() presentation.Declaration {
	return presentation.NativeNeutralDeclaration("claude")
}

// PresentationMigrationState records that Claude still renders via the
// pre-declaration production path and must not claim verified/partial.
func PresentationMigrationState() presentation.MigrationState {
	return presentation.MigrationLegacyUnverified
}
