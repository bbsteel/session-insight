package changehost

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

type policyResolver struct {
	addresses map[string][]netip.Addr
	calls     int
}

func (r *policyResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.calls++
	addresses, ok := r.addresses[host]
	if !ok {
		return nil, errors.New("resolver detail must remain wrapped")
	}
	return addresses, nil
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
	if resolver.calls != 2 || len(approved.Endpoints) != 2 {
		t.Fatalf("approval did not resolve and pin the exact origin set: %+v, calls=%d", approved, resolver.calls)
	}
	if approved.Endpoints[0].Origin != host.EndpointOrigins[0] || approved.Endpoints[1].Origin != host.EndpointOrigins[1] {
		t.Fatalf("approval changed origin order or expanded origins: %+v", approved.Endpoints)
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
	if !approved.HTTPApproved || !approved.PrivateNetApproved {
		t.Fatalf("advanced approvals were not fixed in result: %+v", approved)
	}
}

func TestApprovedHostRejectsOriginExpansionAndPrivateRebinding(t *testing.T) {
	approved := approvedGitHubHost()
	approved.Endpoints = append(approved.Endpoints, ApprovedEndpoint{
		Origin: "https://uploads.github.com", Addresses: []netip.Addr{netip.MustParseAddr("8.8.4.4")},
	})
	if err := ValidateApprovedHost(approved); err == nil {
		t.Fatal("origin expansion accepted")
	}
	approved = approvedGitHubHost()
	approved.Endpoints[0].Addresses = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	if err := ValidateApprovedHost(approved); err == nil {
		t.Fatal("private rebinding accepted without approval")
	}
}

func approvedGitHubHost() ApprovedHost {
	return ApprovedHost{
		Host: githubHost(),
		Endpoints: []ApprovedEndpoint{
			{Origin: "https://github.com", Addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}},
			{Origin: "https://api.github.com", Addresses: []netip.Addr{netip.MustParseAddr("1.1.1.1")}},
		},
	}
}
