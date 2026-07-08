package providers

import (
	"database/sql"
	"fmt"
)

// QuotaCache is a normalized, redacted provider quota snapshot.
type QuotaCache struct {
	Provider        string  `json:"provider"`
	ProviderKeyID   *int64  `json:"provider_key_id,omitempty"`
	Source          string  `json:"source"`
	Available       bool    `json:"available"`
	Used            *int64  `json:"used,omitempty"`
	LimitValue      *int64  `json:"limit_value,omitempty"`
	Remaining       *int64  `json:"remaining,omitempty"`
	PeriodStart     *string `json:"period_start,omitempty"`
	PeriodEnd       *string `json:"period_end,omitempty"`
	CheckedAt       string  `json:"checked_at"`
	ExpiresAt       string  `json:"expires_at"`
	MessageRedacted *string        `json:"message_redacted,omitempty"`
	Details         map[string]any `json:"details,omitempty"`
}

// QuotaRepo stores redacted provider quota snapshots.
type QuotaRepo struct {
	db *sql.DB
}

func NewQuotaRepo(db *sql.DB) *QuotaRepo {
	return &QuotaRepo{db: db}
}

func (r *QuotaRepo) Get(provider string) (*QuotaCache, error) {
	if err := validateProvider(provider); err != nil {
		return nil, err
	}
	row := r.db.QueryRow(`
		SELECT provider, provider_key_id, source, available, used, limit_value, remaining,
			period_start, period_end, checked_at, expires_at, message_redacted
		FROM provider_quota_cache WHERE provider = ?`, provider)
	q, err := scanQuota(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &q, nil
}

func (r *QuotaRepo) Upsert(q QuotaCache) error {
	if err := validateProvider(q.Provider); err != nil {
		return err
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
	_, err := r.db.Exec(`
		INSERT INTO provider_quota_cache (
			provider, provider_key_id, source, available, used, limit_value, remaining,
			period_start, period_end, checked_at, expires_at, message_redacted
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider) DO UPDATE SET
			provider_key_id = excluded.provider_key_id,
			source = excluded.source,
			available = excluded.available,
			used = excluded.used,
			limit_value = excluded.limit_value,
			remaining = excluded.remaining,
			period_start = excluded.period_start,
			period_end = excluded.period_end,
			checked_at = excluded.checked_at,
			expires_at = excluded.expires_at,
			message_redacted = excluded.message_redacted`,
		q.Provider, q.ProviderKeyID, q.Source, available, q.Used, q.LimitValue, q.Remaining,
		q.PeriodStart, q.PeriodEnd, q.CheckedAt, q.ExpiresAt, msg)
	if err != nil {
		return fmt.Errorf("upsert provider quota cache: %w", err)
	}
	return nil
}

func (r *QuotaRepo) List() ([]QuotaCache, error) {
	rows, err := r.db.Query(`
		SELECT provider, provider_key_id, source, available, used, limit_value, remaining,
			period_start, period_end, checked_at, expires_at, message_redacted
		FROM provider_quota_cache ORDER BY provider`)
	if err != nil {
		return nil, fmt.Errorf("list provider quota cache: %w", err)
	}
	defer rows.Close()
	var out []QuotaCache
	for rows.Next() {
		q, err := scanQuotaRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

type quotaScanner interface {
	Scan(dest ...any) error
}

func scanQuota(row quotaScanner) (QuotaCache, error) {
	var q QuotaCache
	var providerKeyID, used, limitValue, remaining sql.NullInt64
	var periodStart, periodEnd, message sql.NullString
	var available int
	if err := row.Scan(
		&q.Provider, &providerKeyID, &q.Source, &available, &used, &limitValue, &remaining,
		&periodStart, &periodEnd, &q.CheckedAt, &q.ExpiresAt, &message,
	); err != nil {
		return QuotaCache{}, err
	}
	q.ProviderKeyID = nullInt64Ptr(providerKeyID)
	q.Available = available != 0
	q.Used = nullInt64Ptr(used)
	q.LimitValue = nullInt64Ptr(limitValue)
	q.Remaining = nullInt64Ptr(remaining)
	q.PeriodStart = nullStrPtr(periodStart)
	q.PeriodEnd = nullStrPtr(periodEnd)
	q.MessageRedacted = nullStrPtr(message)
	return q, nil
}

func scanQuotaRows(rows *sql.Rows) (QuotaCache, error) {
	return scanQuota(rows)
}

func nullInt64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
