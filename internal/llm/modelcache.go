package llm

import (
	"context"
	"sync"
	"time"
)

// acpModelTTL is how long a fetched ACP model list result stays served from
// cache — failures included: a CLI without login keeps failing identically,
// and re-spawning the adapter chain to rediscover that wastes tens of
// seconds. The UI's force-refresh is the escape hatch for both stale lists
// and fixed credentials.
const acpModelTTL = time.Hour

type modelCacheEntry struct {
	// mu is held for the whole fetch, which doubles as concurrency dedup:
	// a second caller for the same agent blocks here instead of spawning a
	// second adapter process, then reads the cache the first one filled.
	mu     sync.Mutex
	models []Model
	err    error
	at     time.Time
}

var (
	acpModelCacheMu sync.Mutex
	acpModelCache   = map[string]*modelCacheEntry{}
)

// ListACPModelsCached returns the model list for one local agent CLI,
// serving both successes and failures from cache within acpModelTTL. force
// bypasses the TTL check but still populates the cache for later callers.
func ListACPModelsCached(ctx context.Context, agent string, force bool) ([]Model, error) {
	acpModelCacheMu.Lock()
	entry, ok := acpModelCache[agent]
	if !ok {
		entry = &modelCacheEntry{}
		acpModelCache[agent] = entry
	}
	acpModelCacheMu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if !force && !entry.at.IsZero() && time.Since(entry.at) < acpModelTTL {
		return entry.models, entry.err
	}
	client := &acpClient{cfg: Config{Kind: "acp", Agent: agent}}
	models, err := client.ListModels(ctx)
	// A cancelled request (user navigated away) says nothing about the
	// agent — don't poison the cache with it.
	if err != nil && ctx.Err() != nil {
		return nil, err
	}
	entry.models, entry.err, entry.at = models, err, time.Now()
	return models, err
}
