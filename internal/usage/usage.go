package usage

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// UsageIncrement identifies one aggregate counter bucket to bump.
type UsageIncrement struct {
	Day          string
	GatewayKeyID *int64
	Provider     string
	RouteFamily  string
	StatusClass  string
}

// UsageDaily is an aggregate usage row.
type UsageDaily struct {
	Day           string
	GatewayKeyID  *int64
	Provider      string
	RouteFamily   string
	StatusClass   string
	RequestCount  int
}

// ListFilter narrows ListDaily results.
type ListFilter struct {
	Day string
}

// UsageRepo maintains daily aggregate usage counters.
type UsageRepo struct {
	db *sql.DB
}

// NewUsageRepo creates a usage repository.
func NewUsageRepo(db *sql.DB) *UsageRepo {
	return &UsageRepo{db: db}
}

// Increment upserts the request_count for the usage bucket.
func (r *UsageRepo) Increment(req UsageIncrement) error {
	if req.Day == "" {
		req.Day = DayUTC(time.Now())
	}
	if req.Provider == "" || req.RouteFamily == "" || req.StatusClass == "" {
		return fmt.Errorf("usage: provider, route_family, and status_class required")
	}
	var keyID sql.NullInt64
	if req.GatewayKeyID != nil {
		keyID = sql.NullInt64{Int64: *req.GatewayKeyID, Valid: true}
	}
	_, err := r.db.Exec(`
		INSERT INTO usage_daily (day, gateway_key_id, provider, route_family, status_class, request_count)
		VALUES (?, ?, ?, ?, ?, 1)
		ON CONFLICT(day, gateway_key_id, provider, route_family, status_class)
		DO UPDATE SET request_count = request_count + 1`,
		req.Day, keyID, req.Provider, req.RouteFamily, req.StatusClass,
	)
	if err != nil {
		return fmt.Errorf("increment usage_daily: %w", err)
	}
	return nil
}

// ListDaily returns usage rows for the given day (all days if Day empty).
func (r *UsageRepo) ListDaily(f ListFilter) ([]UsageDaily, error) {
	q := `
		SELECT day, gateway_key_id, provider, route_family, status_class, request_count
		FROM usage_daily`
	var args []any
	if f.Day != "" {
		q += ` WHERE day = ?`
		args = append(args, f.Day)
	}
	q += ` ORDER BY day, gateway_key_id, provider, route_family, status_class`
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list usage_daily: %w", err)
	}
	defer rows.Close()
	var out []UsageDaily
	for rows.Next() {
		var u UsageDaily
		var keyID sql.NullInt64
		if err := rows.Scan(&u.Day, &keyID, &u.Provider, &u.RouteFamily, &u.StatusClass, &u.RequestCount); err != nil {
			return nil, err
		}
		if keyID.Valid {
			v := keyID.Int64
			u.GatewayKeyID = &v
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DayUTC formats t as YYYY-MM-DD in UTC.
func DayUTC(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// RouteFamilyFromPath derives grok/tavily/firecrawl/unknown from the gateway path.
func RouteFamilyFromPath(path string) string {
	switch {
	case strings.HasPrefix(path, "/grok/"):
		return "grok"
	case strings.HasPrefix(path, "/tavily/"):
		return "tavily"
	case strings.HasPrefix(path, "/firecrawl/"):
		return "firecrawl"
	default:
		return "unknown"
	}
}

// StatusClassFromHTTP maps an HTTP status to a usage bucket.
func StatusClassFromHTTP(status int) string {
	if status == http.StatusTooManyRequests {
		return "429"
	}
	switch {
	case status >= 200 && status < 300:
		return "2xx"
	case status >= 400 && status < 500:
		return "4xx"
	case status >= 500:
		return "5xx"
	default:
		return "other"
	}
}

// StatusClassFromNetworkError is the bucket for upstream network failures.
func StatusClassFromNetworkError() string {
	return "network_error"
}