package changehost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/db"
	"github.com/bbsteel/session-insight/internal/model"
)

type snapshotSyncProvider struct {
	contractProvider
	result SnapshotResult
	err    error
	called func()
}

func (p snapshotSyncProvider) GetSnapshot(
	context.Context,
	model.ChangeRequestIdentity,
	model.ContentVersionKey,
) (SnapshotResult, error) {
	if p.called != nil {
		p.called()
	}
	return p.result, p.err
}

type recordingSnapshotStore struct {
	writes []db.ChangeRequestSnapshotWrite
	key    string
	err    error
}

func (s *recordingSnapshotStore) StoreChangeRequestSnapshot(write db.ChangeRequestSnapshotWrite) (string, error) {
	s.writes = append(s.writes, write)
	return s.key, s.err
}

func TestSyncSnapshotRecordsRequestStartBeforeNetworkAndPublishesCurrent(t *testing.T) {
	result := validSnapshotResult()
	var calledAt time.Time
	provider := snapshotSyncProvider{
		contractProvider: contractProvider{
			kind: model.ChangeProviderGitHub, host: githubHost(), caps: supportedCapabilities(),
		},
		result: result,
		called: func() { calledAt = time.Now().UTC() },
	}
	store := &recordingSnapshotStore{key: "change-key"}
	synced, err := SyncSnapshot(context.Background(), provider, store, result.Snapshot.Identity, "", SnapshotSyncOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if synced.ChangeKey != "change-key" || synced.Snapshot.Snapshot.SnapshotID != result.Snapshot.SnapshotID {
		t.Fatalf("unexpected sync result: %+v", synced)
	}
	if len(store.writes) != 1 {
		t.Fatalf("store writes = %d", len(store.writes))
	}
	write := store.writes[0]
	if write.SyncStartedAt.IsZero() || write.SyncStartedAt.After(calledAt) {
		t.Fatalf("sync start %s was not captured before provider call %s", write.SyncStartedAt, calledAt)
	}
	if !write.UpdateCacheHead {
		t.Fatal("current provider sync did not request cache-head promotion")
	}
	if len(write.Contents) != 1 || string(write.Contents[0].Content) != "@@ -1 +1 @@\n-old\n+new\n" || write.Contents[0].Purpose != "patch" {
		t.Fatalf("raw provider sidecar was not preserved: %+v", write.Contents)
	}
	result.Contents[0].Content[0] = 'X'
	if write.Contents[0].Content[0] == 'X' {
		t.Fatal("stored content aliases provider-owned mutable bytes")
	}
}

func TestSyncSnapshotDoesNotPromoteHistoricalVersion(t *testing.T) {
	result := validSnapshotResult()
	provider := snapshotSyncProvider{
		contractProvider: contractProvider{
			kind: model.ChangeProviderGitHub, host: githubHost(), caps: supportedCapabilities(),
		},
		result: result,
	}
	store := &recordingSnapshotStore{key: "change-key"}
	if _, err := SyncSnapshot(
		context.Background(), provider, store, result.Snapshot.Identity,
		result.Snapshot.Content.Key, SnapshotSyncOptions{},
	); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 1 || store.writes[0].UpdateCacheHead {
		t.Fatalf("historical sync advanced cache head: %+v", store.writes)
	}
}

func TestSyncSnapshotRejectsInvalidOrCanceledCaptureBeforePersistence(t *testing.T) {
	result := validSnapshotResult()
	result.Contents = nil
	provider := snapshotSyncProvider{
		contractProvider: contractProvider{
			kind: model.ChangeProviderGitHub, host: githubHost(), caps: supportedCapabilities(),
		},
		result: result,
	}
	store := &recordingSnapshotStore{}
	if _, err := SyncSnapshot(context.Background(), provider, store, result.Snapshot.Identity, "", SnapshotSyncOptions{}); !errors.Is(err, ErrProviderContract) {
		t.Fatalf("invalid capture error = %v", err)
	}
	if len(store.writes) != 0 {
		t.Fatal("invalid provider result reached persistence")
	}

	ctx, cancel := context.WithCancel(context.Background())
	provider.result = validSnapshotResult()
	provider.called = cancel
	if _, err := SyncSnapshot(ctx, provider, store, provider.result.Snapshot.Identity, "", SnapshotSyncOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled capture error = %v", err)
	}
	if len(store.writes) != 0 {
		t.Fatal("canceled provider result reached persistence")
	}
}
