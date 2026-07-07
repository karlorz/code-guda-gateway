package audit

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"code-guda-gateway/internal/providers"
)

// Detail is short structured metadata only (e.g. name=primary;id=12), never free-form
// narrative, request bodies, or prompts. Invalid shapes are replaced before storage.
const (
	auditDetailMaxLen   = 80
	auditDetailFallback = "action_recorded"
)

var auditDetailSegment = regexp.MustCompile(`^[a-z][a-z0-9_]*=[^{}\n]{1,64}$`)

// AuditEvent is an admin/key/config action to persist (detail is redacted before storage).
type AuditEvent struct {
	ActorKind  string
	ActorID    string
	Action     string
	TargetKind string
	TargetID   string
	Detail     string
	ClientIP   string
}

// StoredAuditEvent is a row from audit_events.
type StoredAuditEvent struct {
	ID               int64
	OccurredAt       string
	ActorKind        string
	ActorID          *string
	Action           string
	TargetKind       *string
	TargetID         *string
	DetailRedacted   string
	ClientIPRedacted *string
}

// ListFilter narrows List results.
type ListFilter struct {
	Action    string
	ActorKind string
	From      string
	To        string
	Limit     int
	Offset    int
}

// AuditRepo writes and lists redacted audit events.
type AuditRepo struct {
	db *sql.DB
}

// NewAuditRepo creates an audit repository.
func NewAuditRepo(db *sql.DB) *AuditRepo {
	return &AuditRepo{db: db}
}

// Record inserts a redacted audit event.
func (r *AuditRepo) Record(ev AuditEvent) error {
	if ev.Action == "" {
		return fmt.Errorf("audit: action required")
	}
	if ev.ActorKind == "" {
		ev.ActorKind = "system"
	}
	detail := sanitizeAuditDetail(ev.Detail, ev.Action)
	clientIP := redactClientIP(ev.ClientIP)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var actorID, targetKind, targetID sql.NullString
	if ev.ActorID != "" {
		actorID = sql.NullString{String: ev.ActorID, Valid: true}
	}
	if ev.TargetKind != "" {
		targetKind = sql.NullString{String: ev.TargetKind, Valid: true}
	}
	if ev.TargetID != "" {
		targetID = sql.NullString{String: ev.TargetID, Valid: true}
	}
	var clientIPNull sql.NullString
	if clientIP != "" {
		clientIPNull = sql.NullString{String: clientIP, Valid: true}
	}
	_, err := r.db.Exec(`
		INSERT INTO audit_events (
			occurred_at, actor_kind, actor_id, action, target_kind, target_id, detail_redacted, client_ip_redacted
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		now, ev.ActorKind, actorID, ev.Action, targetKind, targetID, detail, clientIPNull,
	)
	if err != nil {
		return fmt.Errorf("insert audit_events: %w", err)
	}
	return nil
}

// List returns audit events matching the filter.
func (r *AuditRepo) List(f ListFilter) ([]StoredAuditEvent, error) {
	q := `
		SELECT id, occurred_at, actor_kind, actor_id, action, target_kind, target_id, detail_redacted, client_ip_redacted
		FROM audit_events`
	var args []any
	var where []string
	if f.Action != "" {
		where = append(where, `action = ?`)
		args = append(args, f.Action)
	}
	if f.ActorKind != "" {
		where = append(where, `actor_kind = ?`)
		args = append(args, f.ActorKind)
	}
	if f.From != "" {
		where = append(where, `occurred_at >= ?`)
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, `occurred_at <= ?`)
		args = append(args, f.To)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY id`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
		if f.Offset > 0 {
			q += ` OFFSET ?`
			args = append(args, f.Offset)
		}
	}
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit_events: %w", err)
	}
	defer rows.Close()
	var out []StoredAuditEvent
	for rows.Next() {
		var s StoredAuditEvent
		var actorID, targetKind, targetID, clientIP sql.NullString
		if err := rows.Scan(
			&s.ID, &s.OccurredAt, &s.ActorKind, &actorID, &s.Action,
			&targetKind, &targetID, &s.DetailRedacted, &clientIP,
		); err != nil {
			return nil, err
		}
		s.ActorID = nullStrPtr(actorID)
		s.TargetKind = nullStrPtr(targetKind)
		s.TargetID = nullStrPtr(targetID)
		s.ClientIPRedacted = nullStrPtr(clientIP)
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListRecent returns up to limit most recent audit events in chronological order
// (oldest first within the window). Use this for bounded reads (e.g. CLI tail);
// List with filters remains for full filtered history (e.g. admin web UI).
func (r *AuditRepo) ListRecent(limit int) ([]StoredAuditEvent, error) {
	if limit < 1 {
		return nil, fmt.Errorf("audit: limit must be >= 1")
	}
	q := `
		SELECT id, occurred_at, actor_kind, actor_id, action, target_kind, target_id, detail_redacted, client_ip_redacted
		FROM audit_events
		ORDER BY id DESC
		LIMIT ?`
	rows, err := r.db.Query(q, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent audit_events: %w", err)
	}
	defer rows.Close()
	var out []StoredAuditEvent
	for rows.Next() {
		var s StoredAuditEvent
		var actorID, targetKind, targetID, clientIP sql.NullString
		if err := rows.Scan(
			&s.ID, &s.OccurredAt, &s.ActorKind, &actorID, &s.Action,
			&targetKind, &targetID, &s.DetailRedacted, &clientIP,
		); err != nil {
			return nil, err
		}
		s.ActorID = nullStrPtr(actorID)
		s.TargetKind = nullStrPtr(targetKind)
		s.TargetID = nullStrPtr(targetID)
		s.ClientIPRedacted = nullStrPtr(clientIP)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse so tail displays chronological (oldest → newest).
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func sanitizeAuditDetail(detail, action string) string {
	if detail == "" {
		return ""
	}
	if !isCategoricalAuditDetail(detail) {
		if action != "" {
			return action
		}
		return auditDetailFallback
	}
	return providers.Redact(detail)
}

func isCategoricalAuditDetail(detail string) bool {
	if len(detail) > auditDetailMaxLen {
		return false
	}
	if strings.ContainsAny(detail, "{}\n\r") {
		return false
	}
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "request body") || strings.Contains(lower, `"messages"`) ||
		strings.Contains(lower, "prompt") || strings.Contains(lower, `"content"`) {
		return false
	}
	parts := splitAuditDetailParts(detail)
	if len(parts) == 0 {
		return false
	}
	for _, p := range parts {
		if !auditDetailSegment.MatchString(p) {
			return false
		}
	}
	return true
}

func splitAuditDetailParts(detail string) []string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return nil
	}
	var parts []string
	for _, sep := range []string{";", ","} {
		if strings.Contains(detail, sep) {
			for _, chunk := range strings.Split(detail, sep) {
				if s := strings.TrimSpace(chunk); s != "" {
					parts = append(parts, s)
				}
			}
			return parts
		}
	}
	return []string{detail}
}

func redactClientIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	// store only a coarse category, never full IP
	if strings.Contains(ip, ":") {
		return "ipv6"
	}
	parts := strings.Split(ip, ".")
	if len(parts) == 4 {
		return parts[0] + ".x.x.x"
	}
	return "ip"
}

func nullStrPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}
