package quota

import "context"

// NewDefaultProviders is the only registry needed by the server. Adding a
// provider later means implementing QuotaProvider and adding it here; the
// cache, API response, and frontend panel remain provider-agnostic.
func NewDefaultProviders(options ProviderOptions) []QuotaProvider {
	options = normalizeProviderOptions(options)
	return []QuotaProvider{
		NewCodexProvider(options),
		NewClaudeProvider(options),
		NewGeminiProvider(options),
		NewKimiProvider(options),
		NewGrokProvider(options),
		newUnsupportedProvider(ProviderCopilot, "quota.provider.copilot", "quota.provider.copilotDescription", "https://docs.github.com/en/copilot", "included_allowance_unavailable"),
		newUnsupportedProvider(ProviderOpenCode, "quota.provider.opencode", "quota.provider.opencodeDescription", "https://opencode.ai", "upstream_provider_dependent"),
		newUnsupportedProvider(ProviderQwen, "quota.provider.qwen", "quota.provider.qwenDescription", "https://qwenlm.github.io/qwen-code-docs/", "quota_endpoint_unavailable"),
	}
}

type unsupportedProvider struct {
	definition ProviderDefinition
	reasonCode string
}

func newUnsupportedProvider(id ProviderID, displayNameKey, descriptionKey, documentationURL, reasonCode string) QuotaProvider {
	return &unsupportedProvider{
		definition: ProviderDefinition{
			ID:                 id,
			DisplayNameKey:     displayNameKey,
			DescriptionKey:     descriptionKey,
			DocumentationURL:   documentationURL,
			SupportsExactQuota: false,
		},
		reasonCode: reasonCode,
	}
}

func (p *unsupportedProvider) Definition() ProviderDefinition {
	return p.definition
}

func (p *unsupportedProvider) Fetch(_ context.Context) QuotaSnapshot {
	return quotaFailure(StatusUnsupported, p.reasonCode)
}
