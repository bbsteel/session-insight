package changehost

import (
	"context"
	"fmt"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
)

// SnapshotStore is the narrow persistence boundary used by provider sync.
// Provider network work always completes before this method is called.
type SnapshotStore interface {
	StoreChangeRequestSnapshot(db.ChangeRequestSnapshotWrite) (string, error)
}

type SnapshotSyncOptions struct {
	Quota db.SourceContentQuota
}

type SnapshotSyncResult struct {
	ChangeKey string
	Snapshot  SnapshotResult
}

// ProfileProvenance is implemented by profile-driven providers so snapshots
// record which immutable mapping revision produced them.
type ProfileProvenance interface {
	ProfileID() string
	ProfileRevision() int
}

// SyncSnapshot captures and atomically publishes one immutable provider
// snapshot. An empty requestedVersion means "current" and is the only form
// allowed to advance the cache head; a fixed historical request remains
// queryable without rolling the current head backward.
func SyncSnapshot(
	ctx context.Context,
	provider Provider,
	store SnapshotStore,
	identity model.ChangeRequestIdentity,
	requestedVersion model.ContentVersionKey,
	options SnapshotSyncOptions,
) (SnapshotSyncResult, error) {
	if ctx == nil || nilInterface(provider) || nilInterface(store) {
		return SnapshotSyncResult{}, ErrProviderContract
	}
	if validation := model.ValidateChangeRequestIdentity(identity); !validation.OK() ||
		identity.Provider != provider.Kind() || identity.HostID != provider.Host().Key {
		return SnapshotSyncResult{}, ErrProviderContract
	}

	syncStartedAt := time.Now().UTC()
	result, err := provider.GetSnapshot(ctx, identity, requestedVersion)
	if err != nil {
		return SnapshotSyncResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return SnapshotSyncResult{}, err
	}
	if errs := ValidateSnapshotResult(result); len(errs) != 0 {
		return SnapshotSyncResult{}, fmt.Errorf("%w: %v", ErrProviderContract, errs)
	}
	if result.Snapshot.Identity.Provider != provider.Kind() || result.Snapshot.Identity.HostID != provider.Host().Key {
		return SnapshotSyncResult{}, ErrProviderContract
	}
	contents := make([]db.ChangeRequestContentWrite, 0, len(result.Contents))
	for _, content := range result.Contents {
		contents = append(contents, db.ChangeRequestContentWrite{
			FileKey: content.FileKey,
			Purpose: string(content.Purpose),
			Content: append([]byte(nil), content.Content...),
		})
	}
	write := db.ChangeRequestSnapshotWrite{
		Snapshot: result.Snapshot, SyncStartedAt: syncStartedAt,
		UpdateCacheHead: requestedVersion == "", Contents: contents,
		Quota: options.Quota,
	}
	if provenance, ok := provider.(ProfileProvenance); ok {
		write.ProfileID = provenance.ProfileID()
		write.ProfileRevision = provenance.ProfileRevision()
	}
	changeKey, err := store.StoreChangeRequestSnapshot(write)
	if err != nil {
		return SnapshotSyncResult{}, err
	}
	return SnapshotSyncResult{ChangeKey: changeKey, Snapshot: result}, nil
}
