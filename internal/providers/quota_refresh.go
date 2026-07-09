package providers

import (
	"bytes"
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

// maxQuotaResponseBytes caps how much of an upstream quota response we read
// into memory. 1 MiB was too small for grok2api /admin/api/tokens with
// thousands of SSO tokens (~3 MB observed), causing truncated JSON parse
// failures. 16 MiB bounds memory while accommodating large token pools.
const maxQuotaResponseBytes = 16 << 20

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
	KeyQuotas    *KeyQuotaRepo
	Now          func() time.Time
	MasterKey    []byte
}

// KeyQuotaRefreshResult is the per-key outcome of RefreshKey / RefreshAllKeys.
type KeyQuotaRefreshResult struct {
	ProviderKeyID int64             `json:"provider_key_id"`
	Provider      string            `json:"provider"`
	Attempted     bool              `json:"attempted"`
	SkippedReason *string           `json:"skipped_reason,omitempty"`
	Quota         *ProviderKeyQuota `json:"quota,omitempty"`
}

// RefreshAllKeyQuotasResult aggregates RefreshAllKeys outcomes for one provider.
type RefreshAllKeyQuotasResult struct {
	Provider        string                  `json:"provider"`
	Attempted       int                     `json:"attempted"`
	Succeeded       int                     `json:"succeeded"`
	Failed          int                     `json:"failed"`
	SkippedCooldown int                     `json:"skipped_cooldown"`
	SkippedDisabled int                     `json:"skipped_disabled"`
	SkippedArchived int                     `json:"skipped_archived"`
	KeyResults      []KeyQuotaRefreshResult `json:"key_results"`
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
		return r.refreshGrok2API(ctx, checked, expires)
	default:
		return QuotaCache{}, ErrUnknownProvider
	}
}

// RefreshKey fetches quota for one specific provider key and upserts provider_key_quota_cache.
// Does not call SelectKey; the admin chose the exact row.
// On decrypt/HTTP/parse failure returns an available=false snapshot with nil error
// (matches provider-level Refresh failure philosophy), unless Upsert itself fails.
func (r *QuotaRefresher) RefreshKey(ctx context.Context, providerKeyID int64) (ProviderKeyQuota, error) {
	display, err := r.ProviderKeys.Get(providerKeyID)
	if err != nil {
		return ProviderKeyQuota{}, err
	}
	raw, err := r.ProviderKeys.RawKey(providerKeyID)
	if err != nil {
		q := keyQuotaFailure(display, "quota_refresh", err)
		if r.KeyQuotas != nil {
			_ = r.KeyQuotas.Upsert(q)
		}
		return q, nil
	}
	qc := r.refreshProviderWithRawKey(ctx, display.Provider, providerKeyID, raw)
	q := providerKeyQuotaFromCache(providerKeyID, qc)
	if r.KeyQuotas != nil {
		if err := r.KeyQuotas.Upsert(q); err != nil {
			return ProviderKeyQuota{}, err
		}
	}
	return q, nil
}

// RefreshAllKeys walks every key for a provider, skipping archived/disabled/cooling rows,
// and RefreshKey's the rest.
func (r *QuotaRefresher) RefreshAllKeys(ctx context.Context, provider string) (RefreshAllKeyQuotasResult, error) {
	var out RefreshAllKeyQuotasResult
	out.Provider = provider
	keys, err := r.ProviderKeys.List(provider)
	if err != nil {
		return out, err
	}
	nowFn := r.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
	for _, key := range keys {
		switch {
		case key.ArchivedAt != nil:
			out.SkippedArchived++
			reason := "archived"
			out.KeyResults = append(out.KeyResults, KeyQuotaRefreshResult{ProviderKeyID: key.ID, Provider: provider, SkippedReason: &reason})
			continue
		case !key.Enabled:
			out.SkippedDisabled++
			reason := "disabled"
			out.KeyResults = append(out.KeyResults, KeyQuotaRefreshResult{ProviderKeyID: key.ID, Provider: provider, SkippedReason: &reason})
			continue
		case cooldownActive(key.CooldownUntil, now):
			out.SkippedCooldown++
			reason := "cooldown"
			out.KeyResults = append(out.KeyResults, KeyQuotaRefreshResult{ProviderKeyID: key.ID, Provider: provider, SkippedReason: &reason})
			continue
		}
		out.Attempted++
		q, err := r.RefreshKey(ctx, key.ID)
		if err != nil || !q.Available {
			out.Failed++
		} else {
			out.Succeeded++
		}
		out.KeyResults = append(out.KeyResults, KeyQuotaRefreshResult{ProviderKeyID: key.ID, Provider: provider, Attempted: true, Quota: &q})
	}
	return out, nil
}

// refreshProviderWithRawKey runs the provider-specific quota endpoint using a known raw key.
// For Tavily/Firecrawl this hits the per-key usage endpoint with that bearer.
// For Grok, per-key raw keys are not used for quota (admin token path); we attribute the
// admin-token pool snapshot to the chosen key id so a ProviderKeyQuota row still lands.
func (r *QuotaRefresher) refreshProviderWithRawKey(ctx context.Context, provider string, keyID int64, rawKey string) QuotaCache {
	nowFn := r.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
	checked := now.Format(time.RFC3339Nano)
	expires := now.Add(quotaCacheTTL).Format(time.RFC3339Nano)

	switch provider {
	case ProviderTavily:
		return r.refreshProviderHTTPWithKey(ctx, ProviderTavily, "tavily_usage", "/usage", checked, expires, keyID, rawKey, func(id *int64, body []byte) QuotaCache {
			var payload tavilyUsageResponse
			if err := json.Unmarshal(body, &payload); err != nil {
				return quotaMalformed(ProviderTavily, "tavily_usage", checked, expires, id)
			}
			return normalizeTavilyUsage(ProviderTavily, id, checked, expires, payload)
		})
	case ProviderFirecrawl:
		return r.refreshProviderHTTPWithKey(ctx, ProviderFirecrawl, "firecrawl_credit_usage", "/team/credit-usage", checked, expires, keyID, rawKey, func(id *int64, body []byte) QuotaCache {
			var payload firecrawlCreditUsageResponse
			if err := json.Unmarshal(body, &payload); err != nil {
				return quotaMalformed(ProviderFirecrawl, "firecrawl_credit_usage", checked, expires, id)
			}
			return normalizeFirecrawlCreditUsage(ProviderFirecrawl, id, checked, expires, payload)
		})
	case ProviderGrok:
		// Grok quota is admin-token based, not per provider_keys raw key.
		// Reuse the existing admin path and attribute the result to this key id.
		qc, _ := r.refreshGrok2API(ctx, checked, expires)
		qc.ProviderKeyID = &keyID
		return qc
	default:
		return quotaFailure(provider, "quota_refresh", checked, expires, &keyID, ErrUnknownProvider)
	}
}

func providerKeyQuotaFromCache(providerKeyID int64, qc QuotaCache) ProviderKeyQuota {
	q := ProviderKeyQuota{
		ProviderKeyID:   providerKeyID,
		Provider:        qc.Provider,
		Source:          qc.Source,
		Available:       qc.Available,
		Used:            qc.Used,
		LimitValue:      qc.LimitValue,
		Remaining:       qc.Remaining,
		PeriodStart:     qc.PeriodStart,
		PeriodEnd:       qc.PeriodEnd,
		CheckedAt:       qc.CheckedAt,
		ExpiresAt:       qc.ExpiresAt,
		MessageRedacted: qc.MessageRedacted,
		Details:         qc.Details,
	}
	if q.MessageRedacted != nil {
		redacted := Redact(*q.MessageRedacted)
		q.MessageRedacted = &redacted
	}
	return q
}

func keyQuotaFailure(display DisplayProviderKey, source string, err error) ProviderKeyQuota {
	now := time.Now().UTC()
	checked := now.Format(time.RFC3339Nano)
	expires := now.Add(quotaCacheTTL).Format(time.RFC3339Nano)
	qc := quotaFailure(display.Provider, source, checked, expires, &display.ID, err)
	return providerKeyQuotaFromCache(display.ID, qc)
}

func (r *QuotaRefresher) refreshGrok2API(ctx context.Context, checked, expires string) (QuotaCache, error) {
	mode, err := r.Settings.GetGrokQuotaMode()
	if err != nil {
		return quotaFailure(ProviderGrok, "grok2api_admin_required", checked, expires, nil, err), nil
	}
	if mode != "grok2api_admin" {
		return grokAdminRequiredQuota(checked, expires), nil
	}
	adminKey, err := r.Settings.GetGrok2APIAdminKey(r.MasterKey)
	if err != nil {
		return quotaFailure(ProviderGrok, "grok2api_admin_required", checked, expires, nil, err), nil
	}
	if adminKey == "" {
		return grokAdminRequiredQuota(checked, expires), nil
	}

	base, err := r.Settings.GetGrok2APIAdminBaseURL()
	if err != nil {
		return quotaFailure(ProviderGrok, "grok2api_admin_tokens", checked, expires, nil, err), nil
	}
	if base == "" {
		base, err = r.Settings.GetBaseURL(ProviderGrok)
		if err != nil {
			return quotaFailure(ProviderGrok, "grok2api_admin_tokens", checked, expires, nil, err), nil
		}
	}

	adminBase := strings.TrimRight(base, "/")

	// 1. POST /admin/api/batch/refresh?all_manageable=true (best-effort: forces
	// upstream quota refresh but may time out or reject; we still read tokens below).
	batchURL := adminBase + "/admin/api/batch/refresh?all_manageable=true"
	if _, _, err := r.postJSON(ctx, batchURL, adminKey, []byte(`{"tokens":[]}`)); err != nil {
		// Non-fatal: continue to read cached tokens from /admin/api/tokens.
	}

	// 2. GET /admin/api/tokens
	tokensURL := adminBase + "/admin/api/tokens"
	body, status, err := r.requestHTTP(ctx, http.MethodGet, tokensURL, adminKey)
	if err != nil {
		return quotaHTTPFailure(ProviderGrok, "grok2api_admin_tokens", checked, expires, nil, status, err), nil
	}

	// 3. Normalize
	var payload grok2APITokensResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return quotaMalformed(ProviderGrok, "grok2api_admin_tokens", checked, expires, nil), nil
	}
	return normalizeGrok2APITokens(ProviderGrok, nil, checked, expires, payload), nil
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
	return r.refreshProviderHTTPWithKey(ctx, provider, source, pathSuffix, checked, expires, keyID, rawKey, parse), nil
}

// refreshProviderHTTPWithKey performs the quota HTTP call with a known raw key (no SelectKey).
func (r *QuotaRefresher) refreshProviderHTTPWithKey(
	ctx context.Context,
	provider, source, pathSuffix, checked, expires string,
	keyID int64,
	rawKey string,
	parse func(keyID *int64, body []byte) QuotaCache,
) QuotaCache {
	base, err := r.Settings.GetBaseURL(provider)
	if err != nil {
		return quotaFailure(provider, source, checked, expires, &keyID, err)
	}
	url := strings.TrimRight(base, "/") + pathSuffix
	body, status, err := r.getJSON(ctx, url, rawKey)
	r.recordQuotaEvent(keyID, status, err)
	if err != nil {
		return quotaHTTPFailure(provider, source, checked, expires, &keyID, status, err)
	}
	return parse(&keyID, body)
}

func (r *QuotaRefresher) requestHTTP(ctx context.Context, method, url, bearer string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxQuotaResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, resp.StatusCode, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	return body, resp.StatusCode, nil
}

func (r *QuotaRefresher) getJSON(ctx context.Context, url, bearer string) ([]byte, int, error) {
	return r.requestHTTP(ctx, http.MethodGet, url, bearer)
}

func (r *QuotaRefresher) postJSON(ctx context.Context, url, bearer string, body []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxQuotaResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return respBody, resp.StatusCode, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	return respBody, resp.StatusCode, nil
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
	// Firecrawl documents planCredits as plan-period credits only (excludes
	// one-time packs / coupons). remainingCredits is the full pool. When
	// remaining exceeds planCredits, limit-used math goes negative.
	if remaining != nil && limit != nil && *remaining > *limit {
		extra := *remaining - *limit
		q.Details = map[string]any{
			"plan_credits":            *limit,
			"extra_credits_remaining": extra,
			"plan_credits_note":       "planCredits excludes one-time credit packs per Firecrawl API",
		}
		q.LimitValue = nil
		q.Used = clampUsedNonNegative(used)
		q.Remaining = remaining
		return q
	}
	q.Remaining = remaining
	q.LimitValue = limit
	q.Used = clampUsedNonNegative(used)
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

func clampUsedNonNegative(used *int64) *int64 {
	if used != nil && *used < 0 {
		zero := int64(0)
		return &zero
	}
	return used
}

func quotaFailure(provider, source, checked, expires string, keyID *int64, err error) QuotaCache {
	msg := "quota refresh failed"
	if errors.Is(err, ErrNoEnabledKey) {
		msg = "no enabled provider key available"
	} else if err != nil && strings.Contains(err.Error(), "decrypt") {
		msg = "stored provider key could not be decrypted (check GUDA_MASTER_KEY_PATH)"
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
