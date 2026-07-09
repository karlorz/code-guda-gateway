package proxy

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"code-guda-gateway/internal/providers"
)

// DefaultAttemptLogRetention is the number of newest attempt rows kept when
// retention is unset or invalid.
const DefaultAttemptLogRetention = 1000

// AttemptLog is one upstream attempt made while proxying a gateway request.
// MessageRedacted is stored only after providers.Redact; raw request/response
// bodies and secrets must never be written here.
type AttemptLog struct {
	ID                     int64   `json:"id"`
	OccurredAt             string  `json:"occurred_at"`
	RequestID              string  `json:"request_id"`
	Provider               string  `json:"provider"`
	RouteFamily            string  `json:"route_family"`
	Path                   string  `json:"path"`
	AttemptIndex           int     `json:"attempt_index"`
	ProviderKeyID          *int64  `json:"provider_key_id,omitempty"`
	ProviderKeyName        *string `json:"provider_key_name,omitempty"`
	ProviderKeyFingerprint *string `json:"provider_key_fingerprint,omitempty"`
	UpstreamStatus         *int    `json:"upstream_status,omitempty"`
	StatusClass            string  `json:"status_class"`
	Reason                 *string `json:"reason,omitempty"`
	CooldownUntil          *string `json:"cooldown_until,omitempty"`
	Terminal               bool    `json:"terminal"`
	MessageRedacted        *string `json:"message_redacted,omitempty"`
}

// AttemptLogFilter selects a page of attempt logs.
type AttemptLogFilter struct {
	RequestID string
	Limit     int
	Offset    int
}

// AttemptLogPage is a paginated newest-first window of attempt logs.
type AttemptLogPage struct {
	Items []AttemptLog `json:"items"`
	Page  struct {
		Limit  int `json:"limit"`
		Offset int `json:"offset"`
		Total  int `json:"total"`
	} `json:"page"`
}

// AttemptLogRepo persists best-effort proxy attempt rows for the debug UI.
type AttemptLogRepo struct {
	db        *sql.DB
	retention int
}

// NewAttemptLogRepo builds a repo that prunes to the newest retention rows.
// retention < 1 uses DefaultAttemptLogRetention.
func NewAttemptLogRepo(db *sql.DB, retention int) *AttemptLogRepo {
	if retention < 1 {
		retention = DefaultAttemptLogRetention
	}
	return &AttemptLogRepo{db: db, retention: retention}
}

// Enabled reports that attempt logging is on when a repo is configured.
// Implements AttemptRecorder.
func (r *AttemptLogRepo) Enabled() bool { return r != nil && r.db != nil }

// Record inserts one attempt row (redacting MessageRedacted) and prunes older
// rows past retention. Required fields: RequestID, Provider, RouteFamily, Path,
// StatusClass.
func (r *AttemptLogRepo) Record(row AttemptLog) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("attempt log repo: not configured")
	}
	if strings.TrimSpace(row.RequestID) == "" {
		return fmt.Errorf("attempt log: request_id required")
	}
	if strings.TrimSpace(row.Provider) == "" {
		return fmt.Errorf("attempt log: provider required")
	}
	if strings.TrimSpace(row.RouteFamily) == "" {
		return fmt.Errorf("attempt log: route_family required")
	}
	if strings.TrimSpace(row.Path) == "" {
		return fmt.Errorf("attempt log: path required")
	}
	if strings.TrimSpace(row.StatusClass) == "" {
		return fmt.Errorf("attempt log: status_class required")
	}
	if strings.TrimSpace(row.OccurredAt) == "" {
		row.OccurredAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	var msg any
	if row.MessageRedacted != nil {
		redacted := providers.Redact(*row.MessageRedacted)
		msg = redacted
	}
	var keyID any
	if row.ProviderKeyID != nil {
		keyID = *row.ProviderKeyID
	}
	var keyName any
	if row.ProviderKeyName != nil {
		keyName = *row.ProviderKeyName
	}
	var keyFP any
	if row.ProviderKeyFingerprint != nil {
		keyFP = *row.ProviderKeyFingerprint
	}
	var upstream any
	if row.UpstreamStatus != nil {
		upstream = *row.UpstreamStatus
	}
	var reason any
	if row.Reason != nil {
		reason = *row.Reason
	}
	var coolUntil any
	if row.CooldownUntil != nil {
		coolUntil = *row.CooldownUntil
	}
	terminal := 0
	if row.Terminal {
		terminal = 1
	}

	_, err := r.db.Exec(`
		INSERT INTO proxy_attempt_logs (
			occurred_at, request_id, provider, route_family, path, attempt_index,
			provider_key_id, provider_key_name, provider_key_fingerprint,
			upstream_status, status_class, reason, cooldown_until, terminal, message_redacted
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.OccurredAt, row.RequestID, row.Provider, row.RouteFamily, row.Path, row.AttemptIndex,
		keyID, keyName, keyFP,
		upstream, row.StatusClass, reason, coolUntil, terminal, msg,
	)
	if err != nil {
		return fmt.Errorf("insert attempt log: %w", err)
	}

	// Keep only the newest N rows by id.
	_, err = r.db.Exec(`
		DELETE FROM proxy_attempt_logs
		WHERE id NOT IN (
			SELECT id FROM proxy_attempt_logs ORDER BY id DESC LIMIT ?
		)`, r.retention)
	if err != nil {
		return fmt.Errorf("prune attempt logs: %w", err)
	}
	return nil
}

// List returns a page of attempt logs ordered by id descending (newest-first).
// The retained window already keeps only the newest N rows; List surfaces the
// most recent attempts first for live retry-sequence debugging.
func (r *AttemptLogRepo) List(filter AttemptLogFilter) (AttemptLogPage, error) {
	var page AttemptLogPage
	if r == nil || r.db == nil {
		return page, fmt.Errorf("attempt log repo: not configured")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	where := ""
	args := make([]any, 0, 3)
	if strings.TrimSpace(filter.RequestID) != "" {
		where = " WHERE request_id = ?"
		args = append(args, filter.RequestID)
	}

	var total int
	countQ := `SELECT COUNT(*) FROM proxy_attempt_logs` + where
	if err := r.db.QueryRow(countQ, args...).Scan(&total); err != nil {
		return page, fmt.Errorf("count attempt logs: %w", err)
	}

	// ORDER BY id DESC returns newest attempts first (live retry sequences).
	// Retention prune already keeps only the newest N rows.
	q := `
		SELECT id, occurred_at, request_id, provider, route_family, path, attempt_index,
			provider_key_id, provider_key_name, provider_key_fingerprint,
			upstream_status, status_class, reason, cooldown_until, terminal, message_redacted
		FROM proxy_attempt_logs` + where + `
		ORDER BY id DESC
		LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return page, fmt.Errorf("list attempt logs: %w", err)
	}
	defer rows.Close()

	items := make([]AttemptLog, 0, limit)
	for rows.Next() {
		item, err := scanAttemptLog(rows)
		if err != nil {
			return page, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}

	page.Items = items
	page.Page.Limit = limit
	page.Page.Offset = offset
	page.Page.Total = total
	return page, nil
}

type attemptLogScanner interface {
	Scan(dest ...any) error
}

func scanAttemptLog(row attemptLogScanner) (AttemptLog, error) {
	var a AttemptLog
	var keyID sql.NullInt64
	var keyName, keyFP, reason, coolUntil, msg sql.NullString
	var upstream sql.NullInt64
	var terminal int
	if err := row.Scan(
		&a.ID, &a.OccurredAt, &a.RequestID, &a.Provider, &a.RouteFamily, &a.Path, &a.AttemptIndex,
		&keyID, &keyName, &keyFP,
		&upstream, &a.StatusClass, &reason, &coolUntil, &terminal, &msg,
	); err != nil {
		return AttemptLog{}, fmt.Errorf("scan attempt log: %w", err)
	}
	if keyID.Valid {
		v := keyID.Int64
		a.ProviderKeyID = &v
	}
	a.ProviderKeyName = nullStrPtr(keyName)
	a.ProviderKeyFingerprint = nullStrPtr(keyFP)
	if upstream.Valid {
		v := int(upstream.Int64)
		a.UpstreamStatus = &v
	}
	a.Reason = nullStrPtr(reason)
	a.CooldownUntil = nullStrPtr(coolUntil)
	a.Terminal = terminal != 0
	a.MessageRedacted = nullStrPtr(msg)
	return a, nil
}

func nullStrPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}

// DebugSettingReader reads the proxy-debug-attempts setting.
type DebugSettingReader interface {
	GetProxyDebugAttempts() (bool, error)
}

// SettingsAttemptRecorder gates attempt logging on the admin debug setting.
type SettingsAttemptRecorder struct {
	Settings DebugSettingReader
	Logs     *AttemptLogRepo
}

// NewSettingsAttemptRecorder wires a settings reader to an attempt log repo.
func NewSettingsAttemptRecorder(settings DebugSettingReader, logs *AttemptLogRepo) *SettingsAttemptRecorder {
	return &SettingsAttemptRecorder{Settings: settings, Logs: logs}
}

// Enabled reports whether proxy attempt debug logging is currently on.
func (r *SettingsAttemptRecorder) Enabled() bool {
	if r == nil || r.Settings == nil {
		return false
	}
	ok, err := r.Settings.GetProxyDebugAttempts()
	return err == nil && ok
}

// Record delegates to the underlying attempt log repo.
func (r *SettingsAttemptRecorder) Record(row AttemptLog) error {
	if r == nil || r.Logs == nil {
		return fmt.Errorf("attempt recorder: not configured")
	}
	return r.Logs.Record(row)
}
