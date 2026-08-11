package db

import (
	"reflect"
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestChangeHostPreviewApprovalStatusAndRevocationLifecycle(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	record := ChangeHostRecord{
		HostID: "host-github-public", Provider: model.ChangeProviderGitHub,
		DisplayOrigin:   "https://github.com",
		EndpointOrigins: []string{"https://github.com", "https://api.github.com"},
	}
	if err := database.StoreChangeHostPreview(record); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := database.ChangeHost(record.HostID)
	if err != nil || !ok {
		t.Fatalf("read preview: ok=%v err=%v", ok, err)
	}
	if stored.Lifecycle != "preview" || stored.Assessment.ReasonCode != model.ReasonChangeHostNotApproved ||
		!reflect.DeepEqual(stored.EndpointOrigins, record.EndpointOrigins) {
		t.Fatalf("unexpected preview: %+v", stored)
	}
	approvedAt := time.Date(2026, 8, 11, 7, 0, 0, 0, time.UTC)
	if err := database.ApproveChangeHost(record.HostID, false, false, approvedAt); err != nil {
		t.Fatal(err)
	}
	checkedAt := approvedAt.Add(time.Minute)
	if err := database.TouchChangeHost(record.HostID, checkedAt, model.ExactGitEvidence()); err != nil {
		t.Fatal(err)
	}
	stored, ok, err = database.ChangeHost(record.HostID)
	if err != nil || !ok || stored.Lifecycle != "approved" || stored.ApprovedAt == nil ||
		stored.LastCheckedAt == nil || stored.Assessment.State != model.GitEvidenceExact {
		t.Fatalf("unexpected approved host: ok=%v err=%v host=%+v", ok, err, stored)
	}
	revokedAt := checkedAt.Add(time.Minute)
	revoked, err := database.RevokeChangeHost(record.HostID, revokedAt)
	if err != nil || !revoked {
		t.Fatalf("revoke: revoked=%v err=%v", revoked, err)
	}
	stored, _, err = database.ChangeHost(record.HostID)
	if err != nil || stored.Lifecycle != "revoked" || stored.Assessment.ReasonCode != model.ReasonChangeHostRevoked {
		t.Fatalf("unexpected revoked host: err=%v host=%+v", err, stored)
	}
	if err := database.StoreChangeHostPreview(record); err == nil {
		t.Fatal("revoked host was revived through preview")
	}
	if err := database.ApproveChangeHost(record.HostID, false, false, revokedAt.Add(time.Minute)); err == nil {
		t.Fatal("revoked host was re-approved")
	}
	hosts, err := database.ListChangeHosts()
	if err != nil || len(hosts) != 1 {
		t.Fatalf("list hosts: hosts=%+v err=%v", hosts, err)
	}
}
