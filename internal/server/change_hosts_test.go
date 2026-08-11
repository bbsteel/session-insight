package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"testing"

	"github.com/bbsteel/session-insight/internal/changehost"
	"github.com/bbsteel/session-insight/internal/model"
)

type staticChangeHostResolver struct {
	addresses map[string][]netip.Addr
	calls     int
}

func (r *staticChangeHostResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.calls++
	return append([]netip.Addr(nil), r.addresses[host]...), nil
}

func TestChangeHostPreviewApproveStatusAndRevokeAPI(t *testing.T) {
	database := openCollabAPIDB(t)
	server := New(database, nil)
	resolver := &staticChangeHostResolver{addresses: map[string][]netip.Addr{
		"github.com":     {netip.MustParseAddr("8.8.8.8")},
		"api.github.com": {netip.MustParseAddr("1.1.1.1")},
	}}
	server.hostPolicy = changehost.NewHostPolicy(resolver)

	response := serveChangeRequestAPI(server, "POST", "/api/change-hosts/preview", `{
		"reference":"https://github.com/acme/widgets/pull/42"
	}`)
	if response.Code != http.StatusOK || resolver.calls != 0 {
		t.Fatalf("preview status=%d DNS calls=%d body=%s", response.Code, resolver.calls, response.Body.String())
	}
	var preview changehost.HostPreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Host.Key != "host-github-public" || len(preview.Host.EndpointOrigins) != 2 {
		t.Fatalf("unexpected preview: %+v", preview)
	}

	response = serveChangeRequestAPI(
		server, "POST", "/api/change-hosts/host-github-public/approve", `{}`,
	)
	if response.Code != http.StatusOK || resolver.calls != 2 {
		t.Fatalf("approve status=%d DNS calls=%d body=%s", response.Code, resolver.calls, response.Body.String())
	}
	var status changehost.HostStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.ApprovalState != changehost.HostApproved || status.Assessment.State != model.GitEvidenceExact ||
		status.Capabilities.Operations[changehost.CapabilitySnapshotPatches].State != changehost.CapabilitySupported {
		t.Fatalf("unexpected approved status: %+v", status)
	}

	response = serveChangeRequestAPI(server, "GET", "/api/change-hosts", "")
	var list changehost.HostListResponse
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &list) != nil || len(list.Hosts) != 1 {
		t.Fatalf("host list status=%d body=%s", response.Code, response.Body.String())
	}

	response = serveChangeRequestAPI(
		server, "POST", "/api/change-hosts/host-github-public/revoke", "",
	)
	if response.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", response.Code, response.Body.String())
	}
	response = serveChangeRequestAPI(
		server, "GET", "/api/change-hosts/host-github-public/status", "",
	)
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || status.ApprovalState != changehost.HostRevoked ||
		status.Assessment.ReasonCode != model.ReasonChangeHostRevoked {
		t.Fatalf("unexpected revoked status=%d host=%+v", response.Code, status)
	}
	response = serveChangeRequestAPI(
		server, "POST", "/api/change-hosts/host-github-public/approve", `{}`,
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("revoked host was re-approved: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGenericReferenceNeverCreatesNetworkHostApproval(t *testing.T) {
	database := openCollabAPIDB(t)
	server := New(database, nil)
	response := serveChangeRequestAPI(server, "POST", "/api/change-hosts/preview", `{
		"reference":"https://code.example/team/repo/reviews/7"
	}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("generic preview status=%d body=%s", response.Code, response.Body.String())
	}
	hosts, err := database.ListChangeHosts()
	if err != nil || len(hosts) != 0 {
		t.Fatalf("generic reference created a host: hosts=%+v err=%v", hosts, err)
	}
}
