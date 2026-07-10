package providers_test

import (
	"errors"
	"testing"

	"code-guda-gateway/internal/providers"
)

func TestDefaultQuotaConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		provider string
		mode     providers.QuotaMode
		flow     providers.QuotaFlow
	}{
		{providers.ProviderGrok, providers.QuotaDisabled, providers.QuotaFlowGrok2APIAdmin},
		{providers.ProviderTavily, providers.QuotaEndpointCredentials, providers.QuotaFlowTavilyUsage},
		{providers.ProviderFirecrawl, providers.QuotaEndpointCredentials, providers.QuotaFlowFirecrawlCreditUsage},
	}
	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			mode, flow, err := providers.DefaultQuotaConfig(tc.provider)
			if err != nil {
				t.Fatalf("DefaultQuotaConfig(%q): %v", tc.provider, err)
			}
			if mode != tc.mode {
				t.Fatalf("mode = %q, want %q", mode, tc.mode)
			}
			if flow != tc.flow {
				t.Fatalf("flow = %q, want %q", flow, tc.flow)
			}
		})
	}

	_, _, err := providers.DefaultQuotaConfig("unknown")
	if !errors.Is(err, providers.ErrUnknownProvider) {
		t.Fatalf("unknown provider err = %v, want ErrUnknownProvider", err)
	}
}

func TestValidateQuotaConfig(t *testing.T) {
	t.Parallel()

	t.Run("accept disabled with no URL or key", func(t *testing.T) {
		got, err := providers.ValidateQuotaConfig(providers.ProviderGrok, providers.EndpointQuotaInput{
			Mode: providers.QuotaDisabled,
			Flow: providers.QuotaFlowGrok2APIAdmin,
		}, false)
		if err != nil {
			t.Fatalf("ValidateQuotaConfig: %v", err)
		}
		if got.Mode != providers.QuotaDisabled || got.Flow != providers.QuotaFlowGrok2APIAdmin {
			t.Fatalf("got %+v", got)
		}
		if got.BaseURL != "" || got.RawKey != "" {
			t.Fatalf("disabled must clear URL/key, got %+v", got)
		}
	})

	t.Run("accept endpoint_credentials with provider-compatible flow", func(t *testing.T) {
		cases := []struct {
			provider string
			flow     providers.QuotaFlow
		}{
			{providers.ProviderTavily, providers.QuotaFlowTavilyUsage},
			{providers.ProviderFirecrawl, providers.QuotaFlowFirecrawlCreditUsage},
			{providers.ProviderGrok, providers.QuotaFlowGrok2APIAdmin},
		}
		for _, tc := range cases {
			got, err := providers.ValidateQuotaConfig(tc.provider, providers.EndpointQuotaInput{
				Mode: providers.QuotaEndpointCredentials,
				Flow: tc.flow,
			}, false)
			if err != nil {
				t.Fatalf("%s: %v", tc.provider, err)
			}
			if got.Mode != providers.QuotaEndpointCredentials || got.Flow != tc.flow {
				t.Fatalf("%s got %+v", tc.provider, got)
			}
			if got.BaseURL != "" || got.RawKey != "" {
				t.Fatalf("%s shared mode must clear URL/key, got %+v", tc.provider, got)
			}
		}
	})

	t.Run("accept separate_credentials with normalized URL and raw key", func(t *testing.T) {
		got, err := providers.ValidateQuotaConfig(providers.ProviderGrok, providers.EndpointQuotaInput{
			Mode:    providers.QuotaSeparateCredentials,
			Flow:    providers.QuotaFlowGrok2APIAdmin,
			BaseURL: "https://grok2api.example/admin/",
			RawKey:  "admin-key-value",
		}, true)
		if err != nil {
			t.Fatalf("ValidateQuotaConfig: %v", err)
		}
		if got.BaseURL != "https://grok2api.example/admin" {
			t.Fatalf("normalized URL = %q", got.BaseURL)
		}
		if got.RawKey != "admin-key-value" {
			t.Fatalf("raw key not preserved")
		}
	})

	t.Run("reject userinfo query fragment quota URLs", func(t *testing.T) {
		bad := []string{
			"https://user:pass@example.com/v1",
			"https://example.com/v1?api_key=secret",
			"https://example.com/v1#part",
		}
		for _, raw := range bad {
			_, err := providers.ValidateQuotaConfig(providers.ProviderGrok, providers.EndpointQuotaInput{
				Mode:    providers.QuotaSeparateCredentials,
				Flow:    providers.QuotaFlowGrok2APIAdmin,
				BaseURL: raw,
				RawKey:  "admin-key-value",
			}, true)
			if err == nil {
				t.Fatalf("expected error for %q", raw)
			}
			if !errors.Is(err, providers.ErrInvalidBaseURL) && !errors.Is(err, providers.ErrInvalidQuotaConfig) {
				t.Fatalf("%q err = %v, want ErrInvalidBaseURL or ErrInvalidQuotaConfig", raw, err)
			}
		}
	})

	t.Run("reject incompatible provider flow pairs", func(t *testing.T) {
		_, err := providers.ValidateQuotaConfig(providers.ProviderGrok, providers.EndpointQuotaInput{
			Mode: providers.QuotaEndpointCredentials,
			Flow: providers.QuotaFlowTavilyUsage,
		}, false)
		if !errors.Is(err, providers.ErrInvalidQuotaConfig) {
			t.Fatalf("err = %v, want ErrInvalidQuotaConfig", err)
		}
		_, err = providers.ValidateQuotaConfig(providers.ProviderTavily, providers.EndpointQuotaInput{
			Mode: providers.QuotaEndpointCredentials,
			Flow: providers.QuotaFlowGrok2APIAdmin,
		}, false)
		if !errors.Is(err, providers.ErrInvalidQuotaConfig) {
			t.Fatalf("err = %v, want ErrInvalidQuotaConfig", err)
		}
	})

	t.Run("reject separate create without raw key", func(t *testing.T) {
		_, err := providers.ValidateQuotaConfig(providers.ProviderGrok, providers.EndpointQuotaInput{
			Mode:    providers.QuotaSeparateCredentials,
			Flow:    providers.QuotaFlowGrok2APIAdmin,
			BaseURL: "https://grok2api.example",
		}, true)
		if !errors.Is(err, providers.ErrInvalidQuotaConfig) {
			t.Fatalf("err = %v, want ErrInvalidQuotaConfig", err)
		}
	})

	t.Run("reject separate URL or key fields for disabled and shared modes", func(t *testing.T) {
		cases := []providers.EndpointQuotaInput{
			{Mode: providers.QuotaDisabled, Flow: providers.QuotaFlowGrok2APIAdmin, BaseURL: "https://example.com"},
			{Mode: providers.QuotaDisabled, Flow: providers.QuotaFlowGrok2APIAdmin, RawKey: "should-not-be-set"},
			{Mode: providers.QuotaEndpointCredentials, Flow: providers.QuotaFlowTavilyUsage, BaseURL: "https://example.com"},
			{Mode: providers.QuotaEndpointCredentials, Flow: providers.QuotaFlowTavilyUsage, RawKey: "should-not-be-set"},
		}
		providersFor := []string{
			providers.ProviderGrok,
			providers.ProviderGrok,
			providers.ProviderTavily,
			providers.ProviderTavily,
		}
		for i, input := range cases {
			_, err := providers.ValidateQuotaConfig(providersFor[i], input, false)
			if !errors.Is(err, providers.ErrInvalidQuotaConfig) {
				t.Fatalf("case %d err = %v, want ErrInvalidQuotaConfig", i, err)
			}
		}
	})

	t.Run("reject unknown provider", func(t *testing.T) {
		_, err := providers.ValidateQuotaConfig("unknown", providers.EndpointQuotaInput{
			Mode: providers.QuotaDisabled,
			Flow: providers.QuotaFlowGrok2APIAdmin,
		}, false)
		if !errors.Is(err, providers.ErrUnknownProvider) {
			t.Fatalf("err = %v, want ErrUnknownProvider", err)
		}
	})

	t.Run("reject separate without URL", func(t *testing.T) {
		_, err := providers.ValidateQuotaConfig(providers.ProviderGrok, providers.EndpointQuotaInput{
			Mode:   providers.QuotaSeparateCredentials,
			Flow:   providers.QuotaFlowGrok2APIAdmin,
			RawKey: "admin-key-value",
		}, true)
		if !errors.Is(err, providers.ErrInvalidQuotaConfig) && !errors.Is(err, providers.ErrInvalidBaseURL) {
			t.Fatalf("err = %v, want ErrInvalidQuotaConfig or ErrInvalidBaseURL", err)
		}
	})

	t.Run("allow separate update without raw key when not required", func(t *testing.T) {
		got, err := providers.ValidateQuotaConfig(providers.ProviderGrok, providers.EndpointQuotaInput{
			Mode:    providers.QuotaSeparateCredentials,
			Flow:    providers.QuotaFlowGrok2APIAdmin,
			BaseURL: "https://grok2api.example/",
		}, false)
		if err != nil {
			t.Fatalf("ValidateQuotaConfig: %v", err)
		}
		if got.BaseURL != "https://grok2api.example" {
			t.Fatalf("normalized URL = %q", got.BaseURL)
		}
		if got.RawKey != "" {
			t.Fatalf("raw key should remain empty on metadata-only edit")
		}
	})
}
