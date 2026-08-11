package changehost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type HostPolicyErrorCode string

const (
	HostPolicyInvalidHost        HostPolicyErrorCode = "invalid_host"
	HostPolicyHTTPNotApproved    HostPolicyErrorCode = "http_not_approved"
	HostPolicyPrivateNotApproved HostPolicyErrorCode = "private_network_not_approved"
	HostPolicyResolutionFailed   HostPolicyErrorCode = "resolution_failed"
	HostPolicyApprovalRevoked    HostPolicyErrorCode = "approval_revoked"
)

// HostPolicyError intentionally excludes the input URL, hostname, DNS error,
// and resolver response from Error(). Trusted diagnostics can unwrap Cause.
type HostPolicyError struct {
	Code  HostPolicyErrorCode
	Cause error
}

func (e *HostPolicyError) Error() string {
	if e == nil {
		return ""
	}
	return "change host approval failed: " + string(e.Code)
}

func (e *HostPolicyError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type HostApprovalOptions struct {
	AllowHTTP           bool
	AllowPrivateNetwork bool
}

type HostPreview struct {
	Host                    HostIdentity `json:"host"`
	RequiresHTTPApproval    bool         `json:"requires_http_approval"`
	RequiresPrivateApproval bool         `json:"requires_private_network_approval"`
}

type approvedEndpoint struct {
	Origin    string
	Addresses []netip.Addr
}

type approvalState struct {
	context context.Context
	cancel  context.CancelFunc
}

// ApprovedHost is an opaque, revocable approval capability. Package-external
// callers cannot construct its authority fields or expand its origin/IP set.
type ApprovedHost struct {
	host               HostIdentity
	endpoints          []approvedEndpoint
	httpApproved       bool
	privateNetApproved bool
	state              *approvalState
}

type NetIPResolver interface {
	// LookupNetIP must honor ctx; HostPolicy supplies its own bounded context.
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type HostPolicy struct {
	resolver       NetIPResolver
	resolveTimeout time.Duration
}

func NewHostPolicy(resolver NetIPResolver) *HostPolicy {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &HostPolicy{resolver: resolver, resolveTimeout: 10 * time.Second}
}

// Preview performs syntax and literal-address classification only. DNS is
// deliberately deferred until the explicit approval action.
func (p *HostPolicy) Preview(host HostIdentity) (HostPreview, error) {
	normalized, err := normalizeHostIdentity(host)
	if err != nil {
		return HostPreview{}, &HostPolicyError{Code: HostPolicyInvalidHost, Cause: err}
	}
	host = normalized
	preview := HostPreview{Host: host}
	for _, rawOrigin := range host.EndpointOrigins {
		u, err := url.Parse(rawOrigin)
		if err != nil {
			return HostPreview{}, &HostPolicyError{Code: HostPolicyInvalidHost, Cause: err}
		}
		if u.Scheme == "http" {
			preview.RequiresHTTPApproval = true
		}
		hostname := strings.ToLower(u.Hostname())
		if hostname == "localhost" {
			preview.RequiresPrivateApproval = true
		}
		if addr, err := netip.ParseAddr(hostname); err == nil && isRestrictedAddress(addr) {
			preview.RequiresPrivateApproval = true
		}
	}
	return preview, nil
}

// Approve is the only policy operation that resolves DNS. It re-validates the
// exact explicit origin set and pins every returned address in the result.
func (p *HostPolicy) Approve(ctx context.Context, host HostIdentity, options HostApprovalOptions) (*ApprovedHost, error) {
	preview, err := p.Preview(host)
	if err != nil {
		return nil, err
	}
	if preview.RequiresHTTPApproval && !options.AllowHTTP {
		return nil, &HostPolicyError{Code: HostPolicyHTTPNotApproved}
	}
	host = preview.Host
	resolveContext, cancelResolve := context.WithTimeout(ctx, p.resolveTimeout)
	defer cancelResolve()
	approved := &ApprovedHost{
		host:               host,
		httpApproved:       options.AllowHTTP,
		privateNetApproved: options.AllowPrivateNetwork,
		endpoints:          make([]approvedEndpoint, 0, len(host.EndpointOrigins)),
	}
	for _, rawOrigin := range host.EndpointOrigins {
		u, parseErr := url.Parse(rawOrigin)
		if parseErr != nil {
			return nil, &HostPolicyError{Code: HostPolicyInvalidHost, Cause: parseErr}
		}
		addresses, resolveErr := p.resolve(resolveContext, u.Hostname())
		if resolveErr != nil || len(addresses) == 0 {
			return nil, &HostPolicyError{Code: HostPolicyResolutionFailed, Cause: resolveErr}
		}
		for _, addr := range addresses {
			if isRestrictedAddress(addr) && !options.AllowPrivateNetwork {
				return nil, &HostPolicyError{Code: HostPolicyPrivateNotApproved}
			}
		}
		approved.endpoints = append(approved.endpoints, approvedEndpoint{Origin: rawOrigin, Addresses: addresses})
	}
	if err := resolveContext.Err(); err != nil {
		return nil, &HostPolicyError{Code: HostPolicyResolutionFailed, Cause: err}
	}
	approvalContext, cancelApproval := context.WithCancel(context.Background())
	approved.state = &approvalState{context: approvalContext, cancel: cancelApproval}
	if err := validateApprovedHost(approved); err != nil {
		cancelApproval()
		return nil, err
	}
	return approved, nil
}

func (p *HostPolicy) resolve(ctx context.Context, hostname string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(hostname); err == nil {
		return []netip.Addr{addr.Unmap()}, nil
	}
	addresses, err := p.resolver.LookupNetIP(ctx, "ip", hostname)
	if err != nil {
		return nil, err
	}
	seen := make(map[netip.Addr]bool, len(addresses))
	result := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !address.IsValid() || seen[address] {
			continue
		}
		seen[address] = true
		result = append(result, address)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Compare(result[j]) < 0 })
	return result, nil
}

func validateApprovedHost(approved *ApprovedHost) error {
	if approved == nil || approved.state == nil || approved.state.context == nil || approved.state.cancel == nil {
		return &HostPolicyError{Code: HostPolicyInvalidHost, Cause: errors.New("approval capability is missing")}
	}
	select {
	case <-approved.state.context.Done():
		return &HostPolicyError{Code: HostPolicyApprovalRevoked}
	default:
	}
	if errs := ValidateHostIdentity(approved.host); len(errs) != 0 {
		return &HostPolicyError{Code: HostPolicyInvalidHost, Cause: errs}
	}
	if len(approved.endpoints) != len(approved.host.EndpointOrigins) {
		return &HostPolicyError{Code: HostPolicyInvalidHost, Cause: errors.New("approved endpoint set differs from host identity")}
	}
	seen := make(map[string]bool, len(approved.endpoints))
	for _, endpoint := range approved.endpoints {
		if seen[endpoint.Origin] || len(endpoint.Addresses) == 0 {
			return &HostPolicyError{Code: HostPolicyInvalidHost, Cause: errors.New("invalid approved endpoint")}
		}
		seen[endpoint.Origin] = true
		matched := false
		for _, expected := range approved.host.EndpointOrigins {
			if endpoint.Origin == expected {
				matched = true
				break
			}
		}
		if !matched {
			return &HostPolicyError{Code: HostPolicyInvalidHost, Cause: errors.New("origin expansion is forbidden")}
		}
		u, _ := url.Parse(endpoint.Origin)
		if u.Scheme == "http" && !approved.httpApproved {
			return &HostPolicyError{Code: HostPolicyHTTPNotApproved}
		}
		for _, address := range endpoint.Addresses {
			if !address.IsValid() || (isRestrictedAddress(address) && !approved.privateNetApproved) {
				return &HostPolicyError{Code: HostPolicyPrivateNotApproved}
			}
		}
	}
	return nil
}

func normalizeHostIdentity(host HostIdentity) (HostIdentity, error) {
	if issue := validateOrigin("display_origin", host.DisplayOrigin); issue != nil {
		return HostIdentity{}, issue
	}
	displayOrigin, err := endpointOrigin(host.DisplayOrigin)
	if err != nil {
		return HostIdentity{}, err
	}
	normalized := host
	normalized.DisplayOrigin = displayOrigin
	normalized.EndpointOrigins = make([]string, 0, len(host.EndpointOrigins))
	for i, rawOrigin := range host.EndpointOrigins {
		if issue := validateOrigin(fmt.Sprintf("endpoint_origins[%d]", i), rawOrigin); issue != nil {
			return HostIdentity{}, issue
		}
		origin, err := endpointOrigin(rawOrigin)
		if err != nil {
			return HostIdentity{}, err
		}
		normalized.EndpointOrigins = append(normalized.EndpointOrigins, origin)
	}
	if errs := ValidateHostIdentity(normalized); len(errs) != 0 {
		return HostIdentity{}, errs
	}
	return normalized, nil
}

func cloneApprovedHost(approved *ApprovedHost) *ApprovedHost {
	if approved == nil {
		return nil
	}
	cloned := *approved
	cloned.host.EndpointOrigins = append([]string(nil), approved.host.EndpointOrigins...)
	cloned.endpoints = make([]approvedEndpoint, len(approved.endpoints))
	for i, endpoint := range approved.endpoints {
		cloned.endpoints[i] = approvedEndpoint{
			Origin: endpoint.Origin, Addresses: append([]netip.Addr(nil), endpoint.Addresses...),
		}
	}
	return &cloned
}

func endpointOrigin(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("invalid endpoint URL")
	}
	hostname := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", errors.New("invalid endpoint port")
	}
	hostPort := hostname
	if (u.Scheme == "https" && port != "443") || (u.Scheme == "http" && port != "80") {
		hostPort = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		hostPort = "[" + hostname + "]"
	}
	return (&url.URL{Scheme: strings.ToLower(u.Scheme), Host: hostPort}).String(), nil
}

func endpointDialAddress(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(u.Hostname(), port), nil
}

func isRestrictedAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return true
	}
	for _, prefix := range restrictedNetworkPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

var restrictedNetworkPrefixes = func() []netip.Prefix {
	values := []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"2001:db8::/32",
	}
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}()

func (a *ApprovedHost) endpoint(origin string) (approvedEndpoint, bool) {
	if a == nil {
		return approvedEndpoint{}, false
	}
	for _, endpoint := range a.endpoints {
		if endpoint.Origin == origin {
			return endpoint, true
		}
	}
	return approvedEndpoint{}, false
}

// Identity returns a defensive copy suitable for status/provider binding.
func (a *ApprovedHost) Identity() HostIdentity {
	if a == nil {
		return HostIdentity{}
	}
	host := a.host
	host.EndpointOrigins = append([]string(nil), a.host.EndpointOrigins...)
	return host
}

// Revoke cancels queued and in-flight requests using this capability. It is
// idempotent and does not delete cached evidence.
func (a *ApprovedHost) Revoke() {
	if a != nil && a.state != nil && a.state.cancel != nil {
		a.state.cancel()
	}
}

func (a *ApprovedHost) active() bool {
	if a == nil || a.state == nil || a.state.context == nil {
		return false
	}
	select {
	case <-a.state.context.Done():
		return false
	default:
		return true
	}
}

func (a *ApprovedHost) String() string {
	if a == nil {
		return "approved change host"
	}
	return fmt.Sprintf("approved change host %s", a.host.Key)
}
