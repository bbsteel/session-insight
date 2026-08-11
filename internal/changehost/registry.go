package changehost

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/bbsteel/session-insight/internal/model"
)

var (
	ErrDuplicateProvider = errors.New("change provider already registered")
	ErrProviderNotFound  = errors.New("change provider not registered")
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

func (r *Registry) RegisterParser(parser ReferenceParser) error {
	if parser == nil || !model.IsKnownChangeProviderKind(parser.Kind()) {
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
	if errs := ValidateProvider(provider); len(errs) != 0 {
		return nil, errs
	}
	return provider, nil
}

func (r *Registry) ParseReference(raw string) (model.ChangeRequestReference, bool) {
	for _, parser := range r.orderedParsers() {
		ref, ok := parser.ParseReference(raw)
		if !ok || ref.Provider != parser.Kind() {
			continue
		}
		if validation := model.ValidateChangeRequestReference(ref); validation.OK() {
			return ref, true
		}
	}
	return model.ChangeRequestReference{}, false
}

func (r *Registry) ParseRemote(raw string) (model.HostedRepositoryReference, bool) {
	for _, parser := range r.orderedParsers() {
		ref, ok := parser.ParseRemote(raw)
		if ok && ref.Provider == parser.Kind() {
			return ref, true
		}
	}
	return model.HostedRepositoryReference{}, false
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
