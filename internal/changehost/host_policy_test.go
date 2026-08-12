package changehost

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"
)

type policyResolver struct {
	addresses map[string][]netip.Addr
	calls     int
}

type blockingResolver struct{}

func (blockingResolver) LookupNetIP(ctx context.Context, _, _ string) ([]netip.Addr, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (r *policyResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.calls++
	addresses, ok := r.addresses[host]
	if !ok {
		return nil, errors.New("resolver detail must remain wrapped")
	}
	return addresses, nil
}

func TestHostPolicyBoundsApprovalDNSResolution(t *testing.T) {
	policy := NewHostPolicy(blockingResolver{})
	policy.resolveTimeout = 20 * time.Millisecond
	started := time.Now()
	_, err := policy.Approve(context.Background(), githubHost(), HostApprovalOptions{})
	var policyErr *HostPolicyError
	if !errors.As(err, &policyErr) || policyErr.Code != HostPolicyResolutionFailed {
		t.Fatalf("blocking DNS did not return typed failure: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("approval DNS exceeded internal deadline: %s", elapsed)
	}
}

func TestHostPolicyDefersDNSUntilApprovalAndPinsExplicitOrigins(t *testing.T) {
	resolver := &policyResolver{addresses: map[string][]netip.Addr{
		"github.com":     {netip.MustParseAddr("8.8.8.8")},
		"api.github.com": {netip.MustParseAddr("1.1.1.1")},
	}}
	policy := NewHostPolicy(resolver)
	host := githubHost()
	preview, err := policy.Preview(host)
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 0 || preview.RequiresHTTPApproval || preview.RequiresPrivateApproval {
		t.Fatalf("preview performed network or invented an advanced warning: %+v, calls=%d", preview, resolver.calls)
	}
	approved, err := policy.Approve(context.Background(), host, HostApprovalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 2 || len(approved.endpoints) != 2 {
		t.Fatalf("approval did not resolve and pin the exact origin set: %+v, calls=%d", approved, resolver.calls)
	}
	resolver.addresses["github.com"][0] = netip.MustParseAddr("9.9.9.9")
	if approved.endpoints[0].Addresses[0] != netip.MustParseAddr("8.8.8.8") {
		t.Fatal("post-approval DNS mutation changed the pinned address set")
	}
	if approved.endpoints[0].Origin != host.EndpointOrigins[0] || approved.endpoints[1].Origin != host.EndpointOrigins[1] {
		t.Fatalf("approval changed origin order or expanded origins: %+v", approved.endpoints)
	}
}

func TestHostPolicyNormalizesOriginsBeforeApproval(t *testing.T) {
	resolver := &policyResolver{addresses: map[string][]netip.Addr{
		"github.com": {netip.MustParseAddr("8.8.8.8")},
	}}
	host := HostIdentity{
		Key: "host-github", Provider: "github", DisplayOrigin: "https://GitHub.COM:443",
		EndpointOrigins: []string{"https://GitHub.COM:443"},
	}
	preview, err := NewHostPolicy(resolver).Preview(host)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Host.DisplayOrigin != "https://github.com" || preview.Host.EndpointOrigins[0] != "https://github.com" {
		t.Fatalf("host origins were not normalized: %+v", preview.Host)
	}
}

func TestHostPolicyRequiresAdvancedApprovalForPrivateHTTP(t *testing.T) {
	resolver := &policyResolver{addresses: map[string][]netip.Addr{
		"git.internal": {netip.MustParseAddr("10.1.2.3")},
	}}
	host := HostIdentity{
		Key: "host-internal", Provider: "gitlab", DisplayOrigin: "http://git.internal",
		EndpointOrigins: []string{"http://git.internal"},
	}
	policy := NewHostPolicy(resolver)
	preview, err := policy.Preview(host)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.RequiresHTTPApproval {
		t.Fatal("HTTP host did not require advanced transport approval")
	}
	_, err = policy.Approve(context.Background(), host, HostApprovalOptions{})
	var policyErr *HostPolicyError
	if !errors.As(err, &policyErr) || policyErr.Code != HostPolicyHTTPNotApproved || resolver.calls != 0 {
		t.Fatalf("HTTP denial happened after DNS or used wrong code: %v, calls=%d", err, resolver.calls)
	}
	_, err = policy.Approve(context.Background(), host, HostApprovalOptions{AllowHTTP: true})
	if !errors.As(err, &policyErr) || policyErr.Code != HostPolicyPrivateNotApproved {
		t.Fatalf("private address accepted without approval: %v", err)
	}
	approved, err := policy.Approve(context.Background(), host, HostApprovalOptions{AllowHTTP: true, AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	if !approved.httpApproved || !approved.privateNetApproved {
		t.Fatalf("advanced approvals were not fixed in result: %+v", approved)
	}
}

func TestApprovedHostRejectsOriginExpansionAndPrivateRebinding(t *testing.T) {
	approved := approvedGitHubHost(t)
	approved.endpoints = append(approved.endpoints, approvedEndpoint{
		Origin: "https://uploads.github.com", Addresses: []netip.Addr{netip.MustParseAddr("8.8.4.4")},
	})
	if err := validateApprovedHost(approved); err == nil {
		t.Fatal("origin expansion accepted")
	}
	approved = approvedGitHubHost(t)
	approved.endpoints[0].Addresses = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	if err := validateApprovedHost(approved); err == nil {
		t.Fatal("private rebinding accepted without approval")
	}
}

func approvedGitHubHost(t *testing.T) *ApprovedHost {
	t.Helper()
	resolver := &policyResolver{addresses: map[string][]netip.Addr{
		"github.com":     {netip.MustParseAddr("8.8.8.8")},
		"api.github.com": {netip.MustParseAddr("1.1.1.1")},
	}}
	approved, err := NewHostPolicy(resolver).Approve(context.Background(), githubHost(), HostApprovalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return approved
}
