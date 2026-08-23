package server

import (
	"net/http"

	"github.com/bbsteel/session-insight/internal/quota"
)

type codingQuotaProviderJSON struct {
	ID                 quota.ProviderID    `json:"id"`
	DisplayNameKey     string              `json:"display_name_key"`
	DescriptionKey     string              `json:"description_key"`
	QuotaStrategyKey   string              `json:"quota_strategy_key"`
	DocumentationURL   string              `json:"documentation_url"`
	SupportsExactQuota bool                `json:"supports_exact_quota"`
	Snapshot           quota.QuotaSnapshot `json:"snapshot"`
}

type codingQuotaResponse struct {
	Providers []codingQuotaProviderJSON `json:"providers"`
}

func (s *Server) handleGetCodingQuotas(w http.ResponseWriter, r *http.Request) {
	s.writeCodingQuotas(w, r, false)
}

func (s *Server) handleRefreshCodingQuotas(w http.ResponseWriter, r *http.Request) {
	s.writeCodingQuotas(w, r, true)
}

func (s *Server) writeCodingQuotas(w http.ResponseWriter, r *http.Request, forceRefresh bool) {
	if s.codingQuotaManager == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "quota_unavailable")
		return
	}
	requestContext := r.Context()
	definitions := s.codingQuotaManager.Definitions()
	snapshots := s.codingQuotaManager.Fetch(requestContext, forceRefresh)
	providers := make([]codingQuotaProviderJSON, 0, len(definitions))
	for index, definition := range definitions {
		if index >= len(snapshots) {
			break
		}
		providers = append(providers, codingQuotaProviderJSON{
			ID:                 definition.ID,
			DisplayNameKey:     definition.DisplayNameKey,
			DescriptionKey:     definition.DescriptionKey,
			QuotaStrategyKey:   definition.QuotaStrategyKey,
			DocumentationURL:   definition.DocumentationURL,
			SupportsExactQuota: definition.SupportsExactQuota,
			Snapshot:           snapshots[index],
		})
	}
	writeJSON(w, codingQuotaResponse{Providers: providers})
}
