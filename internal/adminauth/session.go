package adminauth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	// SessionCookieName is the HttpOnly admin session cookie.
	SessionCookieName = "guda_admin_session"
	sessionCookiePath = "/admin"
)

// LoginResult holds the session id and Set-Cookie header value for a successful login.
type LoginResult struct {
	SessionID string
	Cookie    *http.Cookie
}

// Login validates the raw admin token and creates a session row plus session cookie.
func (s *Service) Login(rawToken string) (*LoginResult, error) {
	ok, err := s.Verify(rawToken)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidToken
	}
	hash := hashToken(rawToken)
	sid, err := newSessionID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	expires := now.Add(s.sessionTTL)
	created := now.Format(time.RFC3339Nano)
	expiresStr := expires.Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`INSERT INTO admin_sessions (id, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		sid, hash, created, expiresStr,
	); err != nil {
		return nil, fmt.Errorf("insert admin_sessions: %w", err)
	}
	cookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    sid,
		Path:     sessionCookiePath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
		MaxAge:   int(s.sessionTTL.Seconds()),
	}
	return &LoginResult{SessionID: sid, Cookie: cookie}, nil
}

// ValidateSession returns true if sid exists and is not expired.
func (s *Service) ValidateSession(sid string) (bool, error) {
	if sid == "" {
		return false, nil
	}
	var expiresStr string
	err := s.db.QueryRow(
		`SELECT expires_at FROM admin_sessions WHERE id = ?`,
		sid,
	).Scan(&expiresStr)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("select session: %w", err)
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresStr)
	if err != nil {
		return false, fmt.Errorf("parse expires_at: %w", err)
	}
	if time.Now().UTC().After(expires) {
		return false, nil
	}
	return true, nil
}

// Logout removes the session and returns a cookie that clears the browser session.
func (s *Service) Logout(sid string) (*http.Cookie, error) {
	if sid != "" {
		if _, err := s.db.Exec(`DELETE FROM admin_sessions WHERE id = ?`, sid); err != nil {
			return nil, fmt.Errorf("delete session: %w", err)
		}
	}
	clear := &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     sessionCookiePath,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	}
	return clear, nil
}

// SessionIDFromRequest reads the admin session cookie from r.
func SessionIDFromRequest(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil || c == nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

func newSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand session id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
