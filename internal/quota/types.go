// Package quota provides local-first coding-plan quota discovery.
//
// Provider implementations own credential discovery and upstream response
// parsing. The package-level contract deliberately describes only normalized
// quota facts so the server and frontend do not need provider-specific logic.
package quota

import (
	"context"
	"sync"
	"time"
)

// ProviderID identifies one coding service account or subscription source.
type ProviderID string

const (
	ProviderCodex    ProviderID = "codex"
	ProviderClaude   ProviderID = "claude"
	ProviderGemini   ProviderID = "gemini"
	ProviderKimi     ProviderID = "kimi"
	ProviderGrok     ProviderID = "grok"
	ProviderCopilot  ProviderID = "copilot"
	ProviderOpenCode ProviderID = "opencode"
	ProviderQwen     ProviderID = "qwen"
)

// SnapshotStatus describes the trust and availability of one provider result.
type SnapshotStatus string

const (
	StatusAvailable     SnapshotStatus = "available"
	StatusStale         SnapshotStatus = "stale"
	StatusNotConfigured SnapshotStatus = "not_configured"
	StatusUnauthorized  SnapshotStatus = "unauthorized"
	StatusRateLimited   SnapshotStatus = "rate_limited"
	StatusNetworkError  SnapshotStatus = "network_error"
	StatusInvalidData   SnapshotStatus = "invalid_data"
	StatusUnsupported   SnapshotStatus = "unsupported"
)

// ProviderDefinition is the stable catalog entry returned before a provider
// has been queried. Display names are resolved by the frontend locale catalog.
type ProviderDefinition struct {
	ID                 ProviderID `json:"id"`
	DisplayNameKey     string     `json:"display_name_key"`
	DescriptionKey     string     `json:"description_key"`
	DocumentationURL   string     `json:"documentation_url"`
	SupportsExactQuota bool       `json:"supports_exact_quota"`
}

// QuotaWindow is one independently resetting allowance. Percent values are
// normalized to the user's remaining share, while amount fields preserve an
// upstream absolute value when the provider exposes one.
type QuotaWindow struct {
	ID               string     `json:"id"`
	RemainingPercent *float64   `json:"remaining_percent,omitempty"`
	UsedPercent      *float64   `json:"used_percent,omitempty"`
	RemainingAmount  *float64   `json:"remaining_amount,omitempty"`
	UsedAmount       *float64   `json:"used_amount,omitempty"`
	LimitAmount      *float64   `json:"limit_amount,omitempty"`
	Unit             string     `json:"unit,omitempty"`
	ResetAt          *time.Time `json:"reset_at,omitempty"`
	WindowSeconds    *int64     `json:"window_seconds,omitempty"`
}

// QuotaSnapshot is safe to serialize to the browser. It never contains a
// credential, raw upstream response, account identifier, or request headers.
type QuotaSnapshot struct {
	ProviderID         ProviderID     `json:"provider_id"`
	Status             SnapshotStatus `json:"status"`
	ReasonCode         string         `json:"reason_code,omitempty"`
	Windows            []QuotaWindow  `json:"windows,omitempty"`
	ObservedAt         *time.Time     `json:"observed_at,omitempty"`
	AttemptedAt        *time.Time     `json:"attempted_at,omitempty"`
	Stale              bool           `json:"stale,omitempty"`
	SourceKind         string         `json:"source_kind,omitempty"`
	PlanLabel          string         `json:"plan_label,omitempty"`
	SupportsExactQuota bool           `json:"supports_exact_quota"`
}

// QuotaProvider fetches one independent provider snapshot. A provider should
// return a non-available snapshot for local credential or upstream failures;
// the manager will add stale data when a previous successful result exists.
type QuotaProvider interface {
	Definition() ProviderDefinition
	Fetch(context.Context) QuotaSnapshot
}

type quotaCacheEntry struct {
	lastSnapshot QuotaSnapshot
	lastGood     *QuotaSnapshot
	expiresAt    time.Time
	errorExpires time.Time
}

// ManagerOptions controls the in-memory cache and clock used by Manager.
type ManagerOptions struct {
	SuccessTTL time.Duration
	ErrorTTL   time.Duration
	Now        func() time.Time
}

// Manager coordinates independent provider fetches and keeps short-lived
// process-local cache entries. It intentionally does not persist quota data or
// credentials in SessionInsight's database.
type Manager struct {
	providers  []QuotaProvider
	entries    map[ProviderID]*quotaCacheEntry
	cacheMu    sync.Mutex
	refreshMu  sync.Mutex
	successTTL time.Duration
	errorTTL   time.Duration
	now        func() time.Time
}

// NewManager creates a quota manager with deterministic provider ordering.
func NewManager(providers []QuotaProvider, options ManagerOptions) *Manager {
	successTTL := options.SuccessTTL
	if successTTL <= 0 {
		successTTL = 4 * time.Minute
	}
	errorTTL := options.ErrorTTL
	if errorTTL <= 0 {
		errorTTL = 30 * time.Second
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		providers:  append([]QuotaProvider(nil), providers...),
		entries:    make(map[ProviderID]*quotaCacheEntry),
		successTTL: successTTL,
		errorTTL:   errorTTL,
		now:        now,
	}
}

// Definitions returns the catalog without contacting any upstream service.
func (m *Manager) Definitions() []ProviderDefinition {
	definitions := make([]ProviderDefinition, 0, len(m.providers))
	for _, provider := range m.providers {
		definitions = append(definitions, provider.Definition())
	}
	return definitions
}

// Fetch returns one result per registered provider. A force refresh bypasses
// both success and error cache entries, but still serializes refreshes so a
// double click cannot fan out duplicate credentialed requests.
func (m *Manager) Fetch(ctx context.Context, forceRefresh bool) []QuotaSnapshot {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	now := m.now()
	results := make([]QuotaSnapshot, len(m.providers))
	type providerResult struct {
		index    int
		provider QuotaProvider
		fresh    QuotaSnapshot
	}
	fetches := make(chan providerResult, len(m.providers))
	pending := 0
	for index, provider := range m.providers {
		definition := provider.Definition()
		if !forceRefresh {
			if cached, ok := m.cachedSnapshot(definition.ID, now); ok {
				results[index] = cached
				continue
			}
		}
		pending++
		go func(index int, provider QuotaProvider) {
			fresh := safeFetch(provider, ctx)
			fetches <- providerResult{index: index, provider: provider, fresh: fresh}
		}(index, provider)
	}
	for completed := 0; completed < pending; completed++ {
		result := <-fetches
		definition := result.provider.Definition()
		attemptedAt := now
		fresh := result.fresh
		fresh.ProviderID = definition.ID
		fresh.AttemptedAt = &attemptedAt
		fresh.SupportsExactQuota = definition.SupportsExactQuota
		if fresh.Status == "" {
			fresh.Status = StatusInvalidData
			fresh.ReasonCode = "empty_provider_status"
		}
		if fresh.Status == StatusAvailable && len(fresh.Windows) > 0 {
			observedAt := attemptedAt
			fresh.ObservedAt = &observedAt
			fresh.Stale = false
			m.storeFresh(definition.ID, fresh, now.Add(m.successTTL))
			results[result.index] = fresh
			continue
		}

		results[result.index] = m.storeFailure(definition.ID, fresh, now)
	}
	return results
}

func safeFetch(provider QuotaProvider, ctx context.Context) (snapshot QuotaSnapshot) {
	defer func() {
		if recover() != nil {
			snapshot = QuotaSnapshot{Status: StatusInvalidData, ReasonCode: "provider_panic"}
		}
	}()
	return provider.Fetch(ctx)
}

func (m *Manager) cachedSnapshot(providerID ProviderID, now time.Time) (QuotaSnapshot, bool) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	entry, ok := m.entries[providerID]
	if !ok || now.After(entry.expiresAt) && now.After(entry.errorExpires) {
		return QuotaSnapshot{}, false
	}
	if entry.lastSnapshot.Status == StatusAvailable && now.After(entry.expiresAt) {
		return QuotaSnapshot{}, false
	}
	if entry.lastSnapshot.Status != StatusAvailable && now.After(entry.errorExpires) {
		return QuotaSnapshot{}, false
	}
	return entry.lastSnapshot, true
}

func (m *Manager) storeFresh(providerID ProviderID, snapshot QuotaSnapshot, expiresAt time.Time) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	entry := m.entries[providerID]
	if entry == nil {
		entry = &quotaCacheEntry{}
		m.entries[providerID] = entry
	}
	copyOfSnapshot := snapshot
	entry.lastSnapshot = copyOfSnapshot
	entry.lastGood = &copyOfSnapshot
	entry.expiresAt = expiresAt
	entry.errorExpires = expiresAt
}

func (m *Manager) storeFailure(providerID ProviderID, failure QuotaSnapshot, now time.Time) QuotaSnapshot {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	entry := m.entries[providerID]
	if entry == nil {
		entry = &quotaCacheEntry{}
		m.entries[providerID] = entry
	}
	if entry.lastGood != nil {
		failure.Windows = append([]QuotaWindow(nil), entry.lastGood.Windows...)
		failure.ObservedAt = entry.lastGood.ObservedAt
		failure.Stale = true
	}
	entry.lastSnapshot = failure
	entry.errorExpires = now.Add(m.errorTTL)
	return failure
}
