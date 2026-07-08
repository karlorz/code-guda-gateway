package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const quotaCacheTTL = 5 * time.Minute

type tavilyUsageResponse struct {
	Key struct {
		Usage         *int64 `json:"usage"`
		Limit         *int64 `json:"limit"`
		SearchUsage   *int64 `json:"search_usage"`
		ExtractUsage  *int64 `json:"extract_usage"`
		CrawlUsage    *int64 `json:"crawl_usage"`
		MapUsage      *int64 `json:"map_usage"`
		ResearchUsage *int64 `json:"research_usage"`
	} `json:"key"`
	Account struct {
		CurrentPlan string `json:"current_plan"`
		PlanUsage   *int64 `json:"plan_usage"`
		PlanLimit   *int64 `json:"plan_limit"`
		PaygoUsage  *int64 `json:"paygo_usage"`
		PaygoLimit  *int64 `json:"paygo_limit"`
	} `json:"account"`
}

type firecrawlCreditUsageResponse struct {
	Success bool `json:"success"`
	Data    *struct {
		RemainingCredits    *int64  `json:"remainingCredits"`
		RemainingCreditsAlt *int64  `json:"remaining_credits"`
		PlanCredits         *int64  `json:"planCredits"`
		PlanCreditsAlt      *int64  `json:"plan_credits"`
		BillingPeriodStart  *string `json:"billingPeriodStart"`
		BillingPeriodEnd    *string `json:"billingPeriodEnd"`
	} `json:"data"`
	CreditsTotal     *int64 `json:"credits_total"`
	CreditsUsed      *int64 `json:"credits_used"`
	CreditsRemaining *int64 `json:"credits_remaining"`
}

type grok2APITokensResponse struct {
	Tokens []struct {
		Quota map[string]struct {
			Remaining *int64 `json:"remaining"`
			Total     *int64 `json:"total"`
		} `json:"quota"`
	} `json:"tokens"`
}

// QuotaRefresher fetches upstream quota/usage and normalizes into QuotaCache.
type QuotaRefresher struct {
	HTTPClient   *http.Client
	ProviderKeys *KeyRepo
	Settings     *SettingsRepo
	Quotas       *QuotaRepo
	Now          func() time.Time
}

func (r *QuotaRefresher) Refresh(ctx context.Context, provider string) (QuotaCache, error) {
	if err := validateProvider(provider); err != nil {
		return QuotaCache{}, err
	}
	nowFn := r.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
	checked := now.Format(time.RFC3339Nano)
	expires := now.Add(quotaCacheTTL).Format(time.RFC3339Nano)

	switch provider {
	case ProviderTavily:
		return r.refreshTavily(ctx, checked, expires)
	case ProviderFirecrawl:
		return r.refreshFirecrawl(ctx, checked, expires)
	case ProviderGrok:
		return grokAdminRequiredQuota(checked, expires), nil
	default:
		return QuotaCache{}, ErrUnknownProvider
	}
}

func grokAdminRequiredQuota(checked, expires string) QuotaCache {
	msg := "grok2api admin key required for quota refresh"
	return QuotaCache{
		Provider:        ProviderGrok,
		Source:          "grok2api_admin_required",
		Available:       false,
		CheckedAt:       checked,
		ExpiresAt:       expires,
		MessageRedacted: &msg,
	}
}

func (r *QuotaRefresher) client() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (r *QuotaRefresher) refreshTavily(ctx context.Context, checked, expires string) (QuotaCache, error) {
	return r.refreshProviderHTTP(ctx, ProviderTavily, "tavily_usage", "/usage", checked, expires, func(keyID *int64, body []byte) QuotaCache {
		var payload tavilyUsageResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			return quotaMalformed(ProviderTavily, "tavily_usage", checked, expires, keyID)
		}
		return normalizeTavilyUsage(ProviderTavily, keyID, checked, expires, payload)
	})
}

func (r *QuotaRefresher) refreshFirecrawl(ctx context.Context, checked, expires string) (QuotaCache, error) {
	return r.refreshProviderHTTP(ctx, ProviderFirecrawl, "firecrawl_credit_usage", "/team/credit-usage", checked, expires, func(keyID *int64, body []byte) QuotaCache {
		var payload firecrawlCreditUsageResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			return quotaMalformed(ProviderFirecrawl, "firecrawl_credit_usage", checked, expires, keyID)
		}
		return normalizeFirecrawlCreditUsage(ProviderFirecrawl, keyID, checked, expires, payload)
	})
}

func (r *QuotaRefresher) refreshProviderHTTP(
	ctx context.Context,
	provider, source, pathSuffix, checked, expires string,
	parse func(keyID *int64, body []byte) QuotaCache,
) (QuotaCache, error) {
	keyID, rawKey, err := r.ProviderKeys.SelectKey(provider)
	if err != nil {
		return quotaFailure(provider, source, checked, expires, nil, err), nil
	}
	base, err := r.Settings.GetBaseURL(provider)
	if err != nil {
		return quotaFailure(provider, source, checked, expires, &keyID, err), nil
	}
	url := strings.TrimRight(base, "/") + pathSuffix
	body, status, err := r.getJSON(ctx, url, rawKey)
	r.recordQuotaEvent(keyID, status, err)
	if err != nil {
		return quotaHTTPFailure(provider, source, checked, expires, &keyID, status, err), nil
	}
	return parse(&keyID, body), nil
}

func (r *QuotaRefresher) getJSON(ctx context.Context, url, bearer string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, resp.StatusCode, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func (r *QuotaRefresher) recordQuotaEvent(keyID int64, status int, err error) {
	if r.ProviderKeys == nil {
		return
	}
	ev := LastEvent{Source: "quota_refresh", StatusClass: "2xx"}
	if err != nil {
		ev.StatusClass = "error"
		if status != 0 {
			ev.HTTPStatus = &status
			ev.Message = Redact(fmt.Sprintf("quota refresh failed: %v", err))
		} else {
			ev.Message = "quota refresh failed"
		}
	} else {
		ev.Message = "quota refresh ok"
	}
	_ = r.ProviderKeys.MarkLastEvent(keyID, ev)
}

func quotaCacheShell(provider, source, checked, expires string, keyID *int64) QuotaCache {
	q := QuotaCache{
		Provider:  provider,
		Source:    source,
		Available: true,
		CheckedAt: checked,
		ExpiresAt: expires,
	}
	if keyID != nil {
		q.ProviderKeyID = keyID
	}
	return q
}

func normalizeTavilyUsage(provider string, keyID *int64, checked, expires string, payload tavilyUsageResponse) QuotaCache {
	q := quotaCacheShell(provider, "tavily_usage", checked, expires, keyID)
	q.Used = payload.Key.Usage
	q.LimitValue = payload.Key.Limit
	if payload.Key.Limit != nil && payload.Key.Usage != nil {
		rem := *payload.Key.Limit - *payload.Key.Usage
		q.Remaining = &rem
	}
	q.Details = map[string]any{
		"key_search_usage":    payload.Key.SearchUsage,
		"key_extract_usage":   payload.Key.ExtractUsage,
		"key_crawl_usage":     payload.Key.CrawlUsage,
		"key_map_usage":       payload.Key.MapUsage,
		"key_research_usage":  payload.Key.ResearchUsage,
		"account_plan":        payload.Account.CurrentPlan,
		"account_plan_usage":  payload.Account.PlanUsage,
		"account_plan_limit":  payload.Account.PlanLimit,
		"account_paygo_usage": payload.Account.PaygoUsage,
		"account_paygo_limit": payload.Account.PaygoLimit,
	}
	return q
}

func normalizeFirecrawlCreditUsage(provider string, keyID *int64, checked, expires string, payload firecrawlCreditUsageResponse) QuotaCache {
	q := quotaCacheShell(provider, "firecrawl_credit_usage", checked, expires, keyID)

	var remaining, limit *int64
	var used *int64
	if payload.Data != nil {
		remaining = coalesceInt64(payload.Data.RemainingCredits, payload.Data.RemainingCreditsAlt)
		limit = coalesceInt64(payload.Data.PlanCredits, payload.Data.PlanCreditsAlt)
		if payload.Data.BillingPeriodStart != nil {
			q.PeriodStart = payload.Data.BillingPeriodStart
		}
		if payload.Data.BillingPeriodEnd != nil {
			q.PeriodEnd = payload.Data.BillingPeriodEnd
		}
	}
	if remaining == nil {
		remaining = payload.CreditsRemaining
	}
	if limit == nil {
		limit = payload.CreditsTotal
	}
	used = payload.CreditsUsed
	if used == nil && limit != nil && remaining != nil {
		v := *limit - *remaining
		used = &v
	}
	q.Remaining = remaining
	q.LimitValue = limit
	q.Used = used
	return q
}

func normalizeGrok2APITokens(provider string, keyID *int64, checked, expires string, payload grok2APITokensResponse) QuotaCache {
	var totalRem, totalLim int64
	for _, tok := range payload.Tokens {
		for _, mode := range tok.Quota {
			if mode.Remaining != nil {
				totalRem += *mode.Remaining
			}
			if mode.Total != nil {
				totalLim += *mode.Total
			}
		}
	}
	used := totalLim - totalRem
	q := QuotaCache{
		Provider:   provider,
		Source:     "grok2api_admin_tokens",
		Available:  true,
		CheckedAt:  checked,
		ExpiresAt:  expires,
		Remaining:  &totalRem,
		LimitValue: &totalLim,
		Used:       &used,
	}
	if keyID != nil {
		q.ProviderKeyID = keyID
	}
	return q
}

func coalesceInt64(a, b *int64) *int64 {
	if a != nil {
		return a
	}
	return b
}

func quotaFailure(provider, source, checked, expires string, keyID *int64, err error) QuotaCache {
	msg := "quota refresh failed"
	if errors.Is(err, ErrNoEnabledKey) {
		msg = "no enabled provider key available"
	}
	return QuotaCache{
		Provider:        provider,
		ProviderKeyID:   keyID,
		Source:          source,
		Available:       false,
		CheckedAt:       checked,
		ExpiresAt:       expires,
		MessageRedacted: &msg,
	}
}

func quotaHTTPFailure(provider, source, checked, expires string, keyID *int64, status int, err error) QuotaCache {
	msg := "quota refresh failed"
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		msg = "credential rejected by upstream"
	case http.StatusNotFound:
		msg = "quota endpoint not found"
	default:
		if err != nil && strings.Contains(err.Error(), "timeout") {
			msg = "quota refresh timed out"
		}
	}
	redacted := Redact(msg)
	return QuotaCache{
		Provider:        provider,
		ProviderKeyID:   keyID,
		Source:          source,
		Available:       false,
		CheckedAt:       checked,
		ExpiresAt:       expires,
		MessageRedacted: &redacted,
	}
}

func quotaMalformed(provider, source, checked, expires string, keyID *int64) QuotaCache {
	msg := "quota response was not understood"
	return QuotaCache{
		Provider:        provider,
		ProviderKeyID:   keyID,
		Source:          source,
		Available:       false,
		CheckedAt:       checked,
		ExpiresAt:       expires,
		MessageRedacted: &msg,
	}
}