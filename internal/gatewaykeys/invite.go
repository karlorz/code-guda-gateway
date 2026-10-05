package gatewaykeys

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"code-guda-gateway/internal/idgen"
)

var (
	ErrInviteNotFound   = errors.New("invite code not found")
	ErrInviteRevoked    = errors.New("invite code revoked")
	ErrInviteExpired    = errors.New("invite code expired")
	ErrInviteExhausted  = errors.New("invite code redemptions exhausted")
	ErrInviteLabelBound = errors.New("agent label does not match invite code bind")
)

type InviteCode struct {
	ID              int64   `json:"id"`
	Code            string  `json:"code"`
	AgentLabelBind  string  `json:"agent_label_bind"`
	MaxRedemptions  int     `json:"max_redemptions"`
	RedemptionCount int     `json:"redemption_count"`
	ExpiresAt       string  `json:"expires_at"`
	RevokedAt       *string `json:"revoked_at"`
	CreatedAt       string  `json:"created_at"`
}

type InviteService struct {
	db *sql.DB
}

func NewInviteService(db *sql.DB) *InviteService {
	return &InviteService{db: db}
}

// Create generates a new invite code.
func (s *InviteService) Create(agentLabelBind string, maxRedemptions int, expiresAt string) (*InviteCode, error) {
	if maxRedemptions <= 0 {
		return nil, errors.New("max_redemptions must be positive")
	}
	exp, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		exp, err = time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			return nil, fmt.Errorf("invalid expires_at: %w", err)
		}
	}
	formattedExpiresAt := exp.UTC().Format(time.RFC3339Nano)

	// Generate a unique code
	randSuffix, err := idgen.RandomBase62(16)
	if err != nil {
		return nil, fmt.Errorf("generate code: %w", err)
	}
	code := "inv_" + randSuffix
	now := time.Now().UTC().Format(time.RFC3339Nano)

	res, err := s.db.Exec(`
		INSERT INTO invite_codes (code, agent_label_bind, max_redemptions, redemption_count, expires_at, created_at)
		VALUES (?, ?, ?, 0, ?, ?)`,
		code, strings.TrimSpace(agentLabelBind), maxRedemptions, formattedExpiresAt, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert invite_codes: %w", err)
	}
	id, _ := res.LastInsertId()
	return &InviteCode{
		ID:              id,
		Code:            code,
		AgentLabelBind:  strings.TrimSpace(agentLabelBind),
		MaxRedemptions:  maxRedemptions,
		RedemptionCount: 0,
		ExpiresAt:       formattedExpiresAt,
		CreatedAt:       now,
	}, nil
}

// List returns all invite codes ordered by newest first.
func (s *InviteService) List() ([]InviteCode, error) {
	rows, err := s.db.Query(`
		SELECT id, code, agent_label_bind, max_redemptions, redemption_count, expires_at, revoked_at, created_at
		FROM invite_codes ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list invite_codes: %w", err)
	}
	defer rows.Close()

	var out []InviteCode
	for rows.Next() {
		var item InviteCode
		var revoked sql.NullString
		if err := rows.Scan(
			&item.ID, &item.Code, &item.AgentLabelBind, &item.MaxRedemptions,
			&item.RedemptionCount, &item.ExpiresAt, &revoked, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		if revoked.Valid {
			item.RevokedAt = &revoked.String
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// Revoke revokes an invite code by ID.
func (s *InviteService) Revoke(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(`UPDATE invite_codes SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now, id)
	if err != nil {
		return fmt.Errorf("revoke invite_code: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var exists int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM invite_codes WHERE id = ?`, id).Scan(&exists)
		if exists == 0 {
			return ErrInviteNotFound
		}
	}
	return nil
}

// RedeemTx atomically redeems an invite code within an execer (tx or db).
// Validates bind if non-empty, checks revoked_at, expiration, and remaining redemptions.
// Increments redemption_count atomically.
// Returns the redeemed InviteCode.
func RedeemTx(execer queryExecer, code, agentLabel string) (*InviteCode, error) {
	trimmedCode := strings.TrimSpace(code)
	if trimmedCode == "" {
		return nil, ErrInviteNotFound
	}

	// First query the invite to give specific errors (or do within tx)
	var item InviteCode
	var revoked sql.NullString
	// Use row lock / query on the db/tx
	row := execerQueryRow(execer, `
		SELECT id, code, agent_label_bind, max_redemptions, redemption_count, expires_at, revoked_at, created_at
		FROM invite_codes WHERE code = ?`, trimmedCode)
	if err := row.Scan(&item.ID, &item.Code, &item.AgentLabelBind, &item.MaxRedemptions, &item.RedemptionCount, &item.ExpiresAt, &revoked, &item.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInviteNotFound
		}
		return nil, fmt.Errorf("query invite_code: %w", err)
	}
	if revoked.Valid {
		item.RevokedAt = &revoked.String
		return nil, ErrInviteRevoked
	}

	// Check agent_label_bind if non-empty
	if item.AgentLabelBind != "" && strings.TrimSpace(agentLabel) != item.AgentLabelBind {
		return nil, ErrInviteLabelBound
	}

	now := time.Now().UTC()
	exp, err := time.Parse(time.RFC3339Nano, item.ExpiresAt)
	if err != nil {
		exp, err = time.Parse(time.RFC3339, item.ExpiresAt)
	}
	if err == nil && !exp.After(now) {
		return nil, ErrInviteExpired
	}

	if item.RedemptionCount >= item.MaxRedemptions {
		return nil, ErrInviteExhausted
	}

	// Atomic update:
	// UPDATE invite_codes SET redemption_count=redemption_count+1 WHERE code=? AND revoked_at IS NULL AND redemption_count < max_redemptions AND expires_at > now
	nowStr := now.Format(time.RFC3339Nano)
	res, err := execer.Exec(`
		UPDATE invite_codes
		SET redemption_count = redemption_count + 1
		WHERE code = ?
		  AND revoked_at IS NULL
		  AND redemption_count < max_redemptions
		  AND expires_at > ?`,
		trimmedCode, nowStr,
	)
	if err != nil {
		return nil, fmt.Errorf("redeem invite_code: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, ErrInviteExhausted
	}

	item.RedemptionCount++
	return &item, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func execerQueryRow(execer queryExecer, query string, args ...any) rowScanner {
	if db, ok := execer.(*sql.DB); ok {
		return db.QueryRow(query, args...)
	}
	if tx, ok := execer.(*sql.Tx); ok {
		return tx.QueryRow(query, args...)
	}
	// Fallback interface if needed
	type queryRower interface {
		QueryRow(query string, args ...any) *sql.Row
	}
	if qr, ok := execer.(queryRower); ok {
		return qr.QueryRow(query, args...)
	}
	panic("execer does not support QueryRow")
}
