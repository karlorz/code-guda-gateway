package providers

import (
	"errors"
	"fmt"
	"strings"
)

// QuotaMode controls how an endpoint resolves credentials for quota refresh.
type QuotaMode string

const (
	// QuotaDisabled skips quota refresh for the endpoint.
	QuotaDisabled QuotaMode = "disabled"
	// QuotaEndpointCredentials reuses the row's inference base_url and key.
	QuotaEndpointCredentials QuotaMode = "endpoint_credentials"
	// QuotaSeparateCredentials uses a dedicated quota URL and encrypted key.
	QuotaSeparateCredentials QuotaMode = "separate_credentials"
)

// QuotaFlow identifies the upstream quota API shape to call.
type QuotaFlow string

const (
	QuotaFlowGrok2APIAdmin        QuotaFlow = "grok2api_admin"
	QuotaFlowTavilyUsage          QuotaFlow = "tavily_usage"
	QuotaFlowFirecrawlCreditUsage QuotaFlow = "firecrawl_credit_usage"
)

// EndpointQuotaInput is the operator-supplied quota configuration for create
// or update. RawKey is accepted only for separate credentials and is never
// persisted in plaintext.
type EndpointQuotaInput struct {
	Mode    QuotaMode
	Flow    QuotaFlow
	BaseURL string
	RawKey  string
}

// ErrInvalidQuotaConfig is returned when mode/flow/field combinations fail
// validation so API and CLI callers can map the failure to HTTP 400.
var ErrInvalidQuotaConfig = errors.New("providers: invalid endpoint quota config")

// DefaultQuotaConfig returns the creation default mode and flow for a provider.
func DefaultQuotaConfig(provider string) (QuotaMode, QuotaFlow, error) {
	if err := validateProvider(provider); err != nil {
		return "", "", err
	}
	switch provider {
	case ProviderGrok:
		return QuotaDisabled, QuotaFlowGrok2APIAdmin, nil
	case ProviderTavily:
		return QuotaEndpointCredentials, QuotaFlowTavilyUsage, nil
	case ProviderFirecrawl:
		return QuotaEndpointCredentials, QuotaFlowFirecrawlCreditUsage, nil
	default:
		return "", "", ErrUnknownProvider
	}
}

// ValidateQuotaConfig normalizes and validates operator quota input.
// When requireRawKey is true (canonical create), separate_credentials must
// include a non-empty raw quota key. Metadata-only updates pass false so an
// existing encrypted key may be retained.
func ValidateQuotaConfig(provider string, input EndpointQuotaInput, requireRawKey bool) (EndpointQuotaInput, error) {
	if err := validateProvider(provider); err != nil {
		return EndpointQuotaInput{}, err
	}

	mode := QuotaMode(strings.TrimSpace(string(input.Mode)))
	flow := QuotaFlow(strings.TrimSpace(string(input.Flow)))
	baseURL := strings.TrimSpace(input.BaseURL)
	rawKey := strings.TrimSpace(input.RawKey)

	if err := validateMode(mode); err != nil {
		return EndpointQuotaInput{}, err
	}
	if err := validateFlowForProvider(provider, flow); err != nil {
		return EndpointQuotaInput{}, err
	}

	switch mode {
	case QuotaDisabled, QuotaEndpointCredentials:
		if baseURL != "" || rawKey != "" {
			return EndpointQuotaInput{}, fmt.Errorf("%w: %s mode must not set quota URL or key", ErrInvalidQuotaConfig, mode)
		}
		return EndpointQuotaInput{Mode: mode, Flow: flow}, nil

	case QuotaSeparateCredentials:
		if baseURL == "" {
			return EndpointQuotaInput{}, fmt.Errorf("%w: separate_credentials requires a quota base URL", ErrInvalidQuotaConfig)
		}
		normalized, err := NormalizeBaseURL(baseURL)
		if err != nil {
			// Preserve both sentinels so callers can map URL shape and config failures.
			return EndpointQuotaInput{}, fmt.Errorf("%w: %w", ErrInvalidQuotaConfig, err)
		}
		if requireRawKey && rawKey == "" {
			return EndpointQuotaInput{}, fmt.Errorf("%w: separate_credentials requires a quota key on create", ErrInvalidQuotaConfig)
		}
		return EndpointQuotaInput{
			Mode:    mode,
			Flow:    flow,
			BaseURL: normalized,
			RawKey:  rawKey,
		}, nil

	default:
		return EndpointQuotaInput{}, fmt.Errorf("%w: unknown quota mode %q", ErrInvalidQuotaConfig, mode)
	}
}

func validateMode(mode QuotaMode) error {
	switch mode {
	case QuotaDisabled, QuotaEndpointCredentials, QuotaSeparateCredentials:
		return nil
	default:
		return fmt.Errorf("%w: unknown quota mode %q", ErrInvalidQuotaConfig, mode)
	}
}

func validateFlowForProvider(provider string, flow QuotaFlow) error {
	want, err := defaultFlow(provider)
	if err != nil {
		return err
	}
	if flow != want {
		return fmt.Errorf("%w: flow %q is not valid for provider %q", ErrInvalidQuotaConfig, flow, provider)
	}
	return nil
}

func defaultFlow(provider string) (QuotaFlow, error) {
	switch provider {
	case ProviderGrok:
		return QuotaFlowGrok2APIAdmin, nil
	case ProviderTavily:
		return QuotaFlowTavilyUsage, nil
	case ProviderFirecrawl:
		return QuotaFlowFirecrawlCreditUsage, nil
	default:
		return "", ErrUnknownProvider
	}
}
