package changehost

import (
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/bbsteel/session-insight/internal/model"
)

var (
	ErrDuplicateProvider  = errors.New("change provider already registered")
	ErrProviderNotFound   = errors.New("change provider not registered")
	ErrAmbiguousReference = errors.New("change reference is ambiguous")
	ErrProviderContract   = errors.New("change provider contract mismatch")
)

// ProviderFactory binds a provider implementation to an already-approved
// host and its read-only HTTP client. Registration never performs network I/O.
type ProviderFactory func(HostIdentity, *HTTPClient) (Provider, error)

// Registry is the provider-owned source of truth for parsing and runtime
// implementations. Consumers inspect provider capabilities through Provider;
// they do not maintain a parallel provider matrix.
type Registry struct {
	mu        sync.RWMutex
	parsers   map[model.ChangeProviderKind]ReferenceParser
	factories map[model.ChangeProviderKind]ProviderFactory
}

func NewRegistry() *Registry {
	return &Registry{
		parsers:   make(map[model.ChangeProviderKind]ReferenceParser),
		factories: make(map[model.ChangeProviderKind]ProviderFactory),
	}
}

// NewDefaultRegistry returns the built-in v0.6 provider set. Registration is
// local-only: constructing the registry never approves a host or performs
// network I/O.
func NewDefaultRegistry() *Registry {
	registry := NewRegistry()
	if err := RegisterBuiltIns(registry); err != nil {
		panic(fmt.Sprintf("register built-in change providers: %v", err))
	}
	return registry
}

// RegisterBuiltIns installs GitHub and GitLab automatic providers plus the
// offline-only generic parser. Generic intentionally has no provider factory.
func RegisterBuiltIns(registry *Registry) error {
	if registry == nil {
		return ErrProviderContract
	}
	for _, parser := range []ReferenceParser{GitHubParser{}, GitLabParser{}, GenericParser{}} {
		if err := registry.RegisterParser(parser); err != nil {
			return err
		}
	}
	factories := []struct {
		kind    model.ChangeProviderKind
		factory ProviderFactory
	}{
		{model.ChangeProviderGitHub, func(host HostIdentity, client *HTTPClient) (Provider, error) {
			return NewGitHubProvider(host, client)
		}},
		{model.ChangeProviderGitLab, func(host HostIdentity, client *HTTPClient) (Provider, error) {
			return NewGitLabProvider(host, client)
		}},
	}
	for _, item := range factories {
		if err := registry.RegisterFactory(item.kind, item.factory); err != nil {
			return err
		}
	}
	return nil
}

func (r *Registry) RegisterParser(parser ReferenceParser) error {
	if nilInterface(parser) || !model.IsKnownChangeProviderKind(parser.Kind()) {
		return fmt.Errorf("%w: invalid parser", ErrProviderNotFound)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.parsers[parser.Kind()]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateProvider, parser.Kind())
	}
	r.parsers[parser.Kind()] = parser
	return nil
}

func (r *Registry) RegisterFactory(kind model.ChangeProviderKind, factory ProviderFactory) error {
	if !model.IsKnownChangeProviderKind(kind) || kind == model.ChangeProviderGeneric || factory == nil {
		return fmt.Errorf("%w: invalid provider factory", ErrProviderNotFound)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[kind]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateProvider, kind)
	}
	r.factories[kind] = factory
	return nil
}

func (r *Registry) NewProvider(kind model.ChangeProviderKind, host HostIdentity, client *HTTPClient) (Provider, error) {
	if client == nil || !model.IsKnownChangeProviderKind(kind) || kind == model.ChangeProviderGeneric || host.Provider != kind || !sameHostIdentity(host, client.approved.Identity()) {
		return nil, ErrProviderContract
	}
	r.mu.RLock()
	factory := r.factories[kind]
	r.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("%w: %s", ErrProviderNotFound, kind)
	}
	provider, err := factory(host, client)
	if err != nil {
		return nil, err
	}
	if nilInterface(provider) {
		return nil, ErrProviderContract
	}
	if errs := ValidateProvider(provider); len(errs) != 0 {
		return nil, errs
	}
	if provider.Kind() != kind || !sameHostIdentity(provider.Host(), host) {
		return nil, ErrProviderContract
	}
	return provider, nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (r *Registry) ParseReference(raw string) (model.ChangeRequestReference, bool) {
	ref, err := r.ResolveReference(raw)
	return ref, err == nil
}

// ResolveReference fails closed when multiple automatic providers claim the
// same input. Generic parsing is considered only when none do.
func (r *Registry) ResolveReference(raw string) (model.ChangeRequestReference, error) {
	parsers := r.orderedParsers()
	matches := make([]model.ChangeRequestReference, 0, 1)
	for _, parser := range parsers {
		if parser.Kind() == model.ChangeProviderGeneric {
			continue
		}
		ref, ok := parser.ParseReference(raw)
		if !ok || ref.Provider != parser.Kind() {
			continue
		}
		if validation := model.ValidateChangeRequestReference(ref); validation.OK() {
			matches = append(matches, ref)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return model.ChangeRequestReference{}, ErrAmbiguousReference
	}
	for _, parser := range parsers {
		if parser.Kind() != model.ChangeProviderGeneric {
			continue
		}
		ref, ok := parser.ParseReference(raw)
		if ok && ref.Provider == model.ChangeProviderGeneric {
			if validation := model.ValidateChangeRequestReference(ref); validation.OK() {
				return ref, nil
			}
		}
	}
	return model.ChangeRequestReference{}, ErrProviderNotFound
}

func (r *Registry) ParseRemote(raw string) (model.HostedRepositoryReference, bool) {
	ref, err := r.ResolveRemote(raw)
	return ref, err == nil
}

// ResolveRemote applies the same fail-closed ambiguity rule and validates the
// sanitized remote before returning it to repository discovery.
func (r *Registry) ResolveRemote(raw string) (model.HostedRepositoryReference, error) {
	parsers := r.orderedParsers()
	matches := make([]model.HostedRepositoryReference, 0, 1)
	for _, parser := range parsers {
		if parser.Kind() == model.ChangeProviderGeneric {
			continue
		}
		ref, ok := parser.ParseRemote(raw)
		if ok && ref.Provider == parser.Kind() && validRepositoryReference(ref) {
			matches = append(matches, ref)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return model.HostedRepositoryReference{}, ErrAmbiguousReference
	}
	for _, parser := range parsers {
		if parser.Kind() != model.ChangeProviderGeneric {
			continue
		}
		ref, ok := parser.ParseRemote(raw)
		if ok && ref.Provider == model.ChangeProviderGeneric && validRepositoryReference(ref) {
			return ref, nil
		}
	}
	return model.HostedRepositoryReference{}, ErrProviderNotFound
}

func validRepositoryReference(ref model.HostedRepositoryReference) bool {
	if !model.IsKnownChangeProviderKind(ref.Provider) || strings.TrimSpace(ref.Slug) == "" || strings.TrimSpace(ref.Slug) != ref.Slug {
		return false
	}
	if issue := validateOrigin("display_origin", ref.DisplayOrigin); issue != nil {
		return false
	}
	origin, err := endpointOrigin(ref.DisplayOrigin)
	if err != nil || origin != ref.DisplayOrigin {
		return false
	}
	remote, err := url.Parse(ref.SanitizedRemote)
	if err != nil || remote.Host == "" || remote.User != nil || remote.RawQuery != "" || remote.Fragment != "" || remote.Path == "" || remote.Path == "/" {
		return false
	}
	remoteOrigin, err := endpointOrigin(remote.String())
	return err == nil && remoteOrigin == ref.DisplayOrigin
}

func sameHostIdentity(left, right HostIdentity) bool {
	if left.Key != right.Key || left.Provider != right.Provider || left.DisplayOrigin != right.DisplayOrigin || len(left.EndpointOrigins) != len(right.EndpointOrigins) {
		return false
	}
	for i := range left.EndpointOrigins {
		if left.EndpointOrigins[i] != right.EndpointOrigins[i] {
			return false
		}
	}
	return true
}

func (r *Registry) Kinds() []model.ChangeProviderKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kinds := make([]model.ChangeProviderKind, 0, len(r.parsers))
	for kind := range r.parsers {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}

func (r *Registry) orderedParsers() []ReferenceParser {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kinds := make([]model.ChangeProviderKind, 0, len(r.parsers))
	for kind := range r.parsers {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if kinds[i] == model.ChangeProviderGeneric {
			return false
		}
		if kinds[j] == model.ChangeProviderGeneric {
			return true
		}
		return kinds[i] < kinds[j]
	})
	parsers := make([]ReferenceParser, 0, len(kinds))
	for _, kind := range kinds {
		parsers = append(parsers, r.parsers[kind])
	}
	return parsers
}
