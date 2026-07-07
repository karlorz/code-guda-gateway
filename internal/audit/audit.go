package audit

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"code-guda-gateway/internal/providers"
)

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
	Action string
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
	detail := redactAuditDetail(ev.Detail)
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
	if f.Action != "" {
		q += ` WHERE action = ?`
		args = append(args, f.Action)
	}
	q += ` ORDER BY id`
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

func redactAuditDetail(detail string) string {
	if detail == "" {
		return ""
	}
	lower := strings.ToLower(detail)
	if strings.Contains(lower, "request body") || strings.Contains(lower, `"messages"`) ||
		strings.Contains(lower, "prompt") || strings.Contains(lower, `"content"`) {
		return "action_recorded"
	}
	return providers.Redact(detail)
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