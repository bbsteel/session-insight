package render

import (
	"fmt"
	"hash/fnv"

	"github.com/bbsteel/session-insight/internal/model"
)

// PositionsRevision derives the cache key shared by replay layout producers
// and evidence anchors. Renderer format/options are independent from the
// authoritative source revision and therefore participate explicitly.
func PositionsRevision(session model.Session, options Options) int64 {
	hash := fnv.New64a()
	fmt.Fprintf(hash, "%d|%d|%d", model.SessionRevision(session), FormatVersion, options.Mask())
	return int64(hash.Sum64() &^ (1 << 63))
}
