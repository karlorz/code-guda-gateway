package mcpoauth

import (
	"database/sql"
	"errors"
	"time"
)

// AuthDecision is the outcome of OAuth grant check for a gateway key.
type AuthDecision int

const (
	// AuthDecisionManual indicates the key has no oauth_grants row (manual static key).
	AuthDecisionManual AuthDecision = iota
	// AuthDecisionOAuthValid indicates the key is an OAuth key and access_expires_at has not passed.
	AuthDecisionOAuthValid
	// AuthDecisionOAuthExpired indicates the key is an OAuth key whose access_expires_at has passed.
	AuthDecisionOAuthExpired
)

// CheckOAuthGrant inspects the oauth_grants table for the given gatewayKeyID.
// - If no row exists: returns AuthDecisionManual, nil
// - If row exists and now > access_expires_at: returns AuthDecisionOAuthExpired, nil
// - If row exists and now <= access_expires_at: returns AuthDecisionOAuthValid, nil
func CheckOAuthGrant(db *sql.DB, gatewayKeyID int64) (AuthDecision, error) {
	var accessExpiresAtStr string
	err := db.QueryRow(`SELECT access_expires_at FROM oauth_grants WHERE gateway_key_id = ?`, gatewayKeyID).Scan(&accessExpiresAtStr)
	if errors.Is(err, sql.ErrNoRows) {
		return AuthDecisionManual, nil
	}
	if err != nil {
		return AuthDecisionManual, err
	}

	exp, err := time.Parse(time.RFC3339Nano, accessExpiresAtStr)
	if err != nil {
		return AuthDecisionOAuthExpired, nil
	}

	if time.Now().UTC().After(exp) {
		return AuthDecisionOAuthExpired, nil
	}
	return AuthDecisionOAuthValid, nil
}
