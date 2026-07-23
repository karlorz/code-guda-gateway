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
	Provider             string                  `json:"provider"`
	Attempted            int                     `json:"attempted"`
	Succeeded            int                     `json:"succeeded"`
	Failed               int                     `json:"failed"`
	SkippedCooldown      int                     `json:"skipped_cooldown"`
	SkippedDisabled      int                     `json:"skipped_disabled"`
	SkippedArchived      int                     `json:"skipped_archived"`
	SkippedNotConfigured int                     `json:"skipped_not_configured"`
	KeyResults           []KeyQuotaRefreshResult `json:"key_results"`
}

// Quota operational snapshot sources for non-HTTP outcomes.
const (
	QuotaSourceDisabled      = "quota_disabled"
	QuotaSourceNotConfigured = "quota_not_configured"
)

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

// RefreshKey fetches quota for one specific provider endpoint using its quota
// sidecar (ResolveEndpointQuota). Does not call SelectKey; the admin chose the
// exact row. On decrypt/HTTP/parse failure returns an available=false snapshot
// with nil error (matches provider-level Refresh failure philosophy), unless
// Upsert itself fails. Never mutates inference routing columns
// (last_failed_at, cooldown, enabled, archived, order).
func (r *QuotaRefresher) RefreshKey(ctx context.Context, providerKeyID int64) (ProviderKeyQuota, error) {
	display, err := r.ProviderKeys.Get(providerKeyID)
	if err != nil {
		return ProviderKeyQuota{}, err
	}
	q := r.refreshKeyFromSidecar(ctx, display)
	if r.KeyQuotas != nil {
		if err := r.KeyQuotas.Upsert(q); err != nil {
			return ProviderKeyQuota{}, err
		}
	}
	return q, nil
}

// refreshKeyFromSidecar resolves the owning endpoint's quota credentials and
// dispatches the configured flow. Disabled / not-configured return operational
// snapshots without HTTP.
func (r *QuotaRefresher) refreshKeyFromSidecar(ctx context.Context, display DisplayProviderKey) ProviderKeyQuota {
	now := r.nowUTC()
	checked := now.Format(time.RFC3339Nano)
	expires := now.Add(quotaCacheTTL).Format(time.RFC3339Nano)

	resolved, err := r.ProviderKeys.ResolveEndpointQuota(display.ID)
	switch {
	case errors.Is(err, ErrQuotaDisabled):
		return operationalProviderKeyQuota(display, QuotaSourceDisabled, "quota refresh disabled for this endpoint", checked, expires)
	case errors.Is(err, ErrQuotaNotConfigured):
		return operationalProviderKeyQuota(display, QuotaSourceNotConfigured, "quota credentials not configured for this endpoint", checked, expires)
	case err != nil:
		return keyQuotaFailure(display, "quota_resolution", checked, expires, err)
	}

	var qc QuotaCache
	switch resolved.Flow {
	case QuotaFlowGrok2APIAdmin:
		qc = r.refreshGrok2APIEndpoint(ctx, resolved, checked, expires)
	case QuotaFlowTavilyUsage:
		qc = r.refreshTavilyEndpoint(ctx, resolved, checked, expires)
	case QuotaFlowFirecrawlCreditUsage:
		qc = r.refreshFirecrawlEndpoint(ctx, resolved, checked, expires)
	default:
		return keyQuotaFailure(display, "quota_flow", checked, expires, ErrInvalidQuotaConfig)
	}
	return providerKeyQuotaFromCache(display.ID, qc)
}

// RefreshAllKeys walks every key for a provider, skipping archived/disabled/cooling
// rows and quota sidecars that are disabled or not configured, then refreshes
// the rest. Reuses List display rows (no extra Get) before ResolveEndpointQuota.
func (r *QuotaRefresher) RefreshAllKeys(ctx context.Context, provider string) (RefreshAllKeyQuotasResult, error) {
	var out RefreshAllKeyQuotasResult
	out.Provider = provider
	keys, err := r.ProviderKeys.List(provider)
	if err != nil {
		return out, err
	}
	now := r.nowUTC()
	for _, key := range keys {
		if reason := poolKeySkipReason(key, now); reason != "" {
			switch reason {
			case "archived":
				out.SkippedArchived++
			case "disabled":
				out.SkippedDisabled++
			case "cooldown":
				out.SkippedCooldown++
			}
			out.KeyResults = append(out.KeyResults, KeyQuotaRefreshResult{ProviderKeyID: key.ID, Provider: provider, SkippedReason: &reason})
			continue
		}
		// Quota sidecar operational skips (distinct from inference enable/disable).
		if key.QuotaMode == QuotaDisabled {
			reason := "quota_disabled"
			out.SkippedDisabled++
			out.KeyResults = append(out.KeyResults, KeyQuotaRefreshResult{ProviderKeyID: key.ID, Provider: provider, SkippedReason: &reason})
			continue
		}
		if key.QuotaMode == QuotaSeparateCredentials && !key.QuotaKeyConfigured {
			reason := "quota_not_configured"
			out.SkippedNotConfigured++
			out.KeyResults = append(out.KeyResults, KeyQuotaRefreshResult{ProviderKeyID: key.ID, Provider: provider, SkippedReason: &reason})
			continue
		}
		out.Attempted++
		q := r.refreshKeyFromSidecar(ctx, key)
		if r.KeyQuotas != nil {
			if err := r.KeyQuotas.Upsert(q); err != nil {
				return out, err
			}
		}
		if !q.Available {
			out.Failed++
		} else {
			out.Succeeded++
		}
		out.KeyResults = append(out.KeyResults, KeyQuotaRefreshResult{ProviderKeyID: key.ID, Provider: provider, Attempted: true, Quota: &q})
	}
	return out, nil
}

// refreshTavilyEndpoint performs Tavily /usage using resolved endpoint quota credentials.
func (r *QuotaRefresher) refreshTavilyEndpoint(ctx context.Context, resolved ResolvedEndpointQuota, checked, expires string) QuotaCache {
	ep := SelectedEndpoint{ID: resolved.EndpointID, Provider: resolved.Provider, BaseURL: resolved.BaseURL, APIKey: resolved.APIKey}
	return r.refreshProviderHTTPWithEndpoint(ctx, ProviderTavily, "tavily_usage", "/usage", checked, expires, ep, func(id *int64, body []byte) QuotaCache {
		var payload tavilyUsageResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			return quotaMalformed(ProviderTavily, "tavily_usage", checked, expires, id)
		}
		return normalizeTavilyUsage(ProviderTavily, id, checked, expires, payload)
	})
}

// refreshFirecrawlEndpoint performs Firecrawl credit-usage using resolved credentials.
func (r *QuotaRefresher) refreshFirecrawlEndpoint(ctx context.Context, resolved ResolvedEndpointQuota, checked, expires string) QuotaCache {
	ep := SelectedEndpoint{ID: resolved.EndpointID, Provider: resolved.Provider, BaseURL: resolved.BaseURL, APIKey: resolved.APIKey}
	return r.refreshProviderHTTPWithEndpoint(ctx, ProviderFirecrawl, "firecrawl_credit_usage", "/team/credit-usage", checked, expires, ep, func(id *int64, body []byte) QuotaCache {
		var payload firecrawlCreditUsageResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			return quotaMalformed(ProviderFirecrawl, "firecrawl_credit_usage", checked, expires, id)
		}
		return normalizeFirecrawlCreditUsage(ProviderFirecrawl, id, checked, expires, payload)
	})
}

// refreshGrok2APIEndpoint performs Grok2API admin token quota using the owning
// endpoint's separate admin URL and quota key (not provider Settings).
func (r *QuotaRefresher) refreshGrok2APIEndpoint(ctx context.Context, resolved ResolvedEndpointQuota, checked, expires string) QuotaCache {
	keyID := resolved.EndpointID
	return r.fetchGrok2APIAdminTokens(ctx, resolved.BaseURL, resolved.APIKey, &keyID, checked, expires, true)
}

func operationalProviderKeyQuota(display DisplayProviderKey, source, message, checked, expires string) ProviderKeyQuota {
	msg := message
	return ProviderKeyQuota{
		ProviderKeyID:   display.ID,
		Provider:        display.Provider,
		Source:          source,
		Available:       false,
		CheckedAt:       checked,
		ExpiresAt:       expires,
		MessageRedacted: &msg,
	}
}

func providerKeyQuotaFromCache(providerKeyID int64, qc QuotaCache) ProviderKeyQuota {
	return ProviderKeyQuota{
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
}

func (r *QuotaRefresher) nowUTC() time.Time {
	nowFn := r.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return nowFn().UTC()
}

func keyQuotaFailure(display DisplayProviderKey, source, checked, expires string, err error) ProviderKeyQuota {
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

	return r.fetchGrok2APIAdminTokens(ctx, base, adminKey, nil, checked, expires, false), nil
}

// fetchGrok2APIAdminTokens POSTs best-effort batch refresh then GETs /admin/api/tokens.
// When recordEvent is true and keyID is non-nil, writes MarkLastEvent only (never demotion).
func (r *QuotaRefresher) fetchGrok2APIAdminTokens(ctx context.Context, base, adminKey string, keyID *int64, checked, expires string, recordEvent bool) QuotaCache {
	adminBase := strings.TrimRight(base, "/")

	// 1. POST /admin/api/batch/refresh?all_manageable=true (best-effort).
	batchURL := adminBase + "/admin/api/batch/refresh?all_manageable=true"
	if _, _, err := r.postJSON(ctx, batchURL, adminKey, []byte(`{"tokens":[]}`)); err != nil {
		// Non-fatal: continue to read tokens.
	}

	// 2. GET /admin/api/tokens
	tokensURL := adminBase + "/admin/api/tokens"
	body, status, err := r.requestHTTP(ctx, http.MethodGet, tokensURL, adminKey)
	if recordEvent && keyID != nil {
		r.recordQuotaEvent(*keyID, status, err)
	}
	if err != nil {
		return quotaHTTPFailure(ProviderGrok, "grok2api_admin_tokens", checked, expires, keyID, status, err)
	}

	var payload grok2APITokensResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return quotaMalformed(ProviderGrok, "grok2api_admin_tokens", checked, expires, keyID)
	}
	return normalizeGrok2APITokens(ProviderGrok, keyID, checked, expires, payload)
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
	ep, err := r.ProviderKeys.SelectEndpoint(provider)
	if err != nil {
		return quotaFailure(provider, source, checked, expires, nil, err), nil
	}
	return r.refreshProviderHTTPWithEndpoint(ctx, provider, source, pathSuffix, checked, expires, ep, parse), nil
}

// refreshProviderHTTPWithEndpoint performs the quota HTTP call using the row-owned
// BaseURL and APIKey from the same SelectedEndpoint (no Settings.GetBaseURL).
func (r *QuotaRefresher) refreshProviderHTTPWithEndpoint(
	ctx context.Context,
	provider, source, pathSuffix, checked, expires string,
	ep SelectedEndpoint,
	parse func(keyID *int64, body []byte) QuotaCache,
) QuotaCache {
	keyID := ep.ID
	url, err := JoinEndpointURL(ep.BaseURL, pathSuffix, "")
	if err != nil {
		return quotaFailure(provider, source, checked, expires, &keyID, err)
	}
	body, status, err := r.getJSON(ctx, url, ep.APIKey)
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
	// Prefer per-key usage; account plan_usage is a coarser fallback for used
	// only when key.usage is absent.
	q.Used = coalesceInt64(payload.Key.Usage, payload.Account.PlanUsage)

	// Tavily /usage does not expose a direct remaining field. Derive:
	//  1. key.limit - key.usage when both present (key-scoped budget, additive
	//     across keys in KnownRemaining)
	//  2. account.plan_limit - account.plan_usage when key.limit is missing
	//     (account-scoped — same figure for every key on the plan; pool
	//     KnownRemaining counts it once via remaining_basis=account_plan)
	//  3. otherwise leave remaining nil — UI surfaces used-only
	basis := ""
	switch {
	case payload.Key.Limit != nil && payload.Key.Usage != nil:
		q.LimitValue = payload.Key.Limit
		q.Remaining = remainingFromLimitUsage(payload.Key.Limit, payload.Key.Usage)
		basis = "key"
	case payload.Key.Limit != nil:
		// Limit without usage: still expose the ceiling; remaining unknown.
		q.LimitValue = payload.Key.Limit
	case payload.Account.PlanLimit != nil && payload.Account.PlanUsage != nil:
		// Account-scoped units: used/limit/remaining all from plan fields so
		// the per-key row is internally consistent (remaining = limit − used).
		q.Used = payload.Account.PlanUsage
		q.LimitValue = payload.Account.PlanLimit
		q.Remaining = remainingFromLimitUsage(payload.Account.PlanLimit, payload.Account.PlanUsage)
		basis = "account_plan"
	case payload.Account.PlanLimit != nil:
		q.LimitValue = payload.Account.PlanLimit
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
	if basis != "" {
		q.Details["remaining_basis"] = basis
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

// remainingFromLimitUsage returns max(0, *limit − *usage) when both are set.
func remainingFromLimitUsage(limit, usage *int64) *int64 {
	if limit == nil || usage == nil {
		return nil
	}
	rem := *limit - *usage
	if rem < 0 {
		rem = 0
	}
	return &rem
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
