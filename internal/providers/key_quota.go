package providers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ProviderKeyQuota is a normalized, redacted per-key quota snapshot.
type ProviderKeyQuota struct {
	ProviderKeyID   int64          `json:"provider_key_id"`
	Provider        string         `json:"provider"`
	Source          string         `json:"source"`
	Available       bool           `json:"available"`
	Used            *int64         `json:"used,omitempty"`
	LimitValue      *int64         `json:"limit_value,omitempty"`
	Remaining       *int64         `json:"remaining,omitempty"`
	PeriodStart     *string        `json:"period_start,omitempty"`
	PeriodEnd       *string        `json:"period_end,omitempty"`
	CheckedAt       string         `json:"checked_at"`
	ExpiresAt       string         `json:"expires_at"`
	MessageRedacted *string        `json:"message_redacted,omitempty"`
	Details         map[string]any `json:"details,omitempty"`
}

// PoolListOptions controls pagination for ProviderPool.
type PoolListOptions struct {
	Limit  int
	Offset int
}

// PageInfo describes a paginated result set.
type PageInfo struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Total  int `json:"total"`
}

// PoolKeyStatus is the derived readiness of one key in a provider pool.
type PoolKeyStatus string

const (
	// PoolKeyStatusAvailable means the key is enabled, not archived/cooled, and has a quota row.
	// Presence of a quota row is the gate; quota.Available itself is not used for status.
	PoolKeyStatusAvailable    PoolKeyStatus = "available"
	PoolKeyStatusCooling      PoolKeyStatus = "cooling"
	PoolKeyStatusDisabled     PoolKeyStatus = "disabled"
	PoolKeyStatusArchived     PoolKeyStatus = "archived"
	PoolKeyStatusNotRefreshed PoolKeyStatus = "not_refreshed"
)

// ProviderPoolSummary aggregates key/quota counts for a provider.
type ProviderPoolSummary struct {
	Provider          string `json:"provider"`
	KeyCount          int    `json:"key_count"`
	EnabledKeyCount   int    `json:"enabled_key_count"`
	AvailableKeyCount int    `json:"available_key_count"`
	CoolingKeyCount   int    `json:"cooling_key_count"`
	RefreshedKeyCount int    `json:"refreshed_key_count"`
	KnownRemaining    *int64 `json:"known_remaining,omitempty"`
}

// ProviderPoolRow is one key plus derived status and optional quota.
type ProviderPoolRow struct {
	Key    DisplayProviderKey `json:"key"`
	Status PoolKeyStatus      `json:"status"`
	Quota  *ProviderKeyQuota  `json:"quota,omitempty"`
}

// ProviderPool is a paginated pool view for one provider.
type ProviderPool struct {
	Provider string              `json:"provider"`
	Summary  ProviderPoolSummary `json:"summary"`
	Items    []ProviderPoolRow   `json:"items"`
	Page     PageInfo            `json:"page"`
}

// KeyQuotaRepo stores redacted per-key quota snapshots.
type KeyQuotaRepo struct {
	db *sql.DB
}

// NewKeyQuotaRepo creates a per-key quota repository.
func NewKeyQuotaRepo(db *sql.DB) *KeyQuotaRepo {
	return &KeyQuotaRepo{db: db}
}

// cooldownActive reports whether a cooldown_until timestamp is still in the future.
func cooldownActive(until *string, now time.Time) bool {
	if until == nil || *until == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, *until)
	if err != nil {
		return false
	}
	return t.After(now)
}

// Upsert inserts or replaces a per-key quota snapshot.
// MessageRedacted is always run through Redact before storage.
func (r *KeyQuotaRepo) Upsert(q ProviderKeyQuota) error {
	if err := validateProvider(q.Provider); err != nil {
		return err
	}
	if q.ProviderKeyID <= 0 {
		return fmt.Errorf("upsert provider key quota: provider_key_id required")
	}
	available := 0
	if q.Available {
		available = 1
	}
	var msg any
	if q.MessageRedacted != nil {
		redacted := Redact(*q.MessageRedacted)
		msg = redacted
	}
	var details any
	if q.Details != nil {
		b, err := json.Marshal(q.Details)
		if err != nil {
			return fmt.Errorf("marshal details_json: %w", err)
		}
		details = string(b)
	}
	_, err := r.db.Exec(`
		INSERT INTO provider_key_quota_cache (
			provider_key_id, provider, source, available, used, limit_value, remaining,
			period_start, period_end, checked_at, expires_at, message_redacted, details_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider_key_id) DO UPDATE SET
			provider = excluded.provider,
			source = excluded.source,
			available = excluded.available,
			used = excluded.used,
			limit_value = excluded.limit_value,
			remaining = excluded.remaining,
			period_start = excluded.period_start,
			period_end = excluded.period_end,
			checked_at = excluded.checked_at,
			expires_at = excluded.expires_at,
			message_redacted = excluded.message_redacted,
			details_json = excluded.details_json`,
		q.ProviderKeyID, q.Provider, q.Source, available, q.Used, q.LimitValue, q.Remaining,
		q.PeriodStart, q.PeriodEnd, q.CheckedAt, q.ExpiresAt, msg, details,
	)
	if err != nil {
		return fmt.Errorf("upsert provider key quota cache: %w", err)
	}
	return nil
}

// Get returns one per-key quota snapshot, or nil when absent.
func (r *KeyQuotaRepo) Get(providerKeyID int64) (*ProviderKeyQuota, error) {
	row := r.db.QueryRow(`
		SELECT provider_key_id, provider, source, available, used, limit_value, remaining,
			period_start, period_end, checked_at, expires_at, message_redacted, details_json
		FROM provider_key_quota_cache WHERE provider_key_id = ?`, providerKeyID)
	q, err := scanKeyQuota(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// ListByProvider returns all per-key quota snapshots for a provider keyed by provider_key_id.
func (r *KeyQuotaRepo) ListByProvider(provider string) (map[int64]ProviderKeyQuota, error) {
	if err := validateProvider(provider); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(`
		SELECT provider_key_id, provider, source, available, used, limit_value, remaining,
			period_start, period_end, checked_at, expires_at, message_redacted, details_json
		FROM provider_key_quota_cache WHERE provider = ?`, provider)
	if err != nil {
		return nil, fmt.Errorf("list provider key quota cache: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]ProviderKeyQuota)
	for rows.Next() {
		q, err := scanKeyQuota(rows)
		if err != nil {
			return nil, err
		}
		out[q.ProviderKeyID] = q
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ProviderPool builds a paginated pool view with derived status and summary counts.
//
// Status precedence per key:
//  1. archived  (ArchivedAt != nil)
//  2. disabled  (!Enabled)
//  3. cooling   (cooldownActive)
//  4. available (quota row present) — presence of a row, not quota.Available
//  5. not_refreshed (no quota row)
//
// Summary counts are computed over ALL keys for the provider (not just the page).
// KnownRemaining sums Remaining across keys whose derived status is available
// and whose Remaining pointer is non-nil.
func (r *KeyQuotaRepo) ProviderPool(keys *KeyRepo, provider string, opts PoolListOptions) (ProviderPool, error) {
	if err := validateProvider(provider); err != nil {
		return ProviderPool{}, err
	}
	if keys == nil {
		return ProviderPool{}, fmt.Errorf("provider pool: key repo required")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	allKeys, err := keys.List(provider)
	if err != nil {
		return ProviderPool{}, err
	}
	quotaByKey, err := r.ListByProvider(provider)
	if err != nil {
		return ProviderPool{}, err
	}

	now := time.Now().UTC()
	summary := ProviderPoolSummary{
		Provider: provider,
		KeyCount: len(allKeys),
	}
	var knownRemaining int64
	var hasKnownRemaining bool

	// Derive status for every key so summary counts are correct independent of pagination.
	type derived struct {
		key    DisplayProviderKey
		status PoolKeyStatus
		quota  *ProviderKeyQuota
	}
	all := make([]derived, 0, len(allKeys))
	for _, k := range allKeys {
		var qPtr *ProviderKeyQuota
		if q, ok := quotaByKey[k.ID]; ok {
			cp := q
			qPtr = &cp
		}
		status := derivePoolKeyStatus(k, qPtr != nil, now)
		all = append(all, derived{key: k, status: status, quota: qPtr})

		if k.Enabled && k.ArchivedAt == nil {
			summary.EnabledKeyCount++
		}
		if qPtr != nil {
			summary.RefreshedKeyCount++
		}
		switch status {
		case PoolKeyStatusAvailable:
			summary.AvailableKeyCount++
			if qPtr != nil && qPtr.Remaining != nil {
				knownRemaining += *qPtr.Remaining
				hasKnownRemaining = true
			}
		case PoolKeyStatusCooling:
			summary.CoolingKeyCount++
		}
	}
	if hasKnownRemaining {
		summary.KnownRemaining = &knownRemaining
	}

	// Paginate key rows (ORDER BY id from List already).
	total := len(all)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	pageSlice := all[start:end]
	items := make([]ProviderPoolRow, 0, len(pageSlice))
	for _, d := range pageSlice {
		items = append(items, ProviderPoolRow{
			Key:    d.key,
			Status: d.status,
			Quota:  d.quota,
		})
	}

	return ProviderPool{
		Provider: provider,
		Summary:  summary,
		Items:    items,
		Page: PageInfo{
			Limit:  limit,
			Offset: offset,
			Total:  total,
		},
	}, nil
}

// derivePoolKeyStatus applies the precedence rules for a single key.
// hasQuota is true when a provider_key_quota_cache row exists for this key.
// A present quota row marks the key available regardless of quota.Available,
// because the key itself is usable for selection; quota.Available is a usage
// signal, not a key-lifecycle status.
func derivePoolKeyStatus(k DisplayProviderKey, hasQuota bool, now time.Time) PoolKeyStatus {
	if k.ArchivedAt != nil {
		return PoolKeyStatusArchived
	}
	if !k.Enabled {
		return PoolKeyStatusDisabled
	}
	if cooldownActive(k.CooldownUntil, now) {
		return PoolKeyStatusCooling
	}
	if hasQuota {
		return PoolKeyStatusAvailable
	}
	return PoolKeyStatusNotRefreshed
}

type keyQuotaScanner interface {
	Scan(dest ...any) error
}

func scanKeyQuota(row keyQuotaScanner) (ProviderKeyQuota, error) {
	var q ProviderKeyQuota
	var used, limitValue, remaining sql.NullInt64
	var periodStart, periodEnd, message, detailsJSON sql.NullString
	var available int
	if err := row.Scan(
		&q.ProviderKeyID, &q.Provider, &q.Source, &available, &used, &limitValue, &remaining,
		&periodStart, &periodEnd, &q.CheckedAt, &q.ExpiresAt, &message, &detailsJSON,
	); err != nil {
		return ProviderKeyQuota{}, err
	}
	q.Available = available != 0
	q.Used = nullInt64Ptr(used)
	q.LimitValue = nullInt64Ptr(limitValue)
	q.Remaining = nullInt64Ptr(remaining)
	q.PeriodStart = nullStrPtr(periodStart)
	q.PeriodEnd = nullStrPtr(periodEnd)
	q.MessageRedacted = nullStrPtr(message)
	if detailsJSON.Valid && detailsJSON.String != "" {
		var details map[string]any
		if err := json.Unmarshal([]byte(detailsJSON.String), &details); err != nil {
			return ProviderKeyQuota{}, fmt.Errorf("unmarshal details_json: %w", err)
		}
		q.Details = details
	}
	return q, nil
}
