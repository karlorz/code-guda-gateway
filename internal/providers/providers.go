package providers

import "errors"

const (
	ProviderGrok      = "grok"
	ProviderTavily    = "tavily"
	ProviderFirecrawl = "firecrawl"

	DefaultGrokBaseURL      = "https://api.x.ai/v1"
	DefaultTavilyBaseURL    = "https://api.tavily.com"
	DefaultFirecrawlBaseURL = "https://api.firecrawl.dev/v2"
)

var (
	ErrUnknownProvider = errors.New("providers: unknown provider")
	ErrNoEnabledKey    = errors.New("providers: no enabled provider key available")
	ErrDuplicateName   = errors.New("providers: provider key name already exists for provider")
	// ErrInvalidBaseURL is returned when an operator-supplied base URL fails NormalizeBaseURL.
	ErrInvalidBaseURL = errors.New("providers: invalid base URL")
	// ErrProviderKeyNotFound is returned when a provider_keys row id does not exist.
	ErrProviderKeyNotFound = errors.New("providers: provider key not found")
)

func validateProvider(p string) error {
	switch p {
	case ProviderGrok, ProviderTavily, ProviderFirecrawl:
		return nil
	default:
		return ErrUnknownProvider
	}
}

func defaultBaseURL(p string) string {
	switch p {
	case ProviderGrok:
		return DefaultGrokBaseURL
	case ProviderTavily:
		return DefaultTavilyBaseURL
	case ProviderFirecrawl:
		return DefaultFirecrawlBaseURL
	default:
		return ""
	}
}
