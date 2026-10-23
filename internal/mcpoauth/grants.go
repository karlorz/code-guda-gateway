package mcpoauth

import "database/sql"

// The old process can issue grants between the schema migration and its restart.
// Reconcile those rows atomically before the new process serves any requests.
func bindLegacyGrants(db *sql.DB, resource string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []struct {
		query string
		args  []any
	}{
		{`UPDATE oauth_grants SET resource = ? WHERE resource = '' AND gateway_key_id IN (SELECT id FROM gateway_keys WHERE oauth_owned = 1)`, []any{resource}},
		{`UPDATE oauth_codes SET resource = ? WHERE resource = ''`, []any{resource}},
		{`UPDATE oauth_grants SET family_id = 'legacy-' || gateway_key_id WHERE family_id = ''`, nil},
		{`INSERT OR IGNORE INTO oauth_refresh_history SELECT refresh_hash, family_id, gateway_key_id, refresh_expires_at FROM oauth_grants`, nil},
		{`DELETE FROM oauth_refresh_history WHERE julianday(expires_at) < julianday('now')`, nil},
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement.query, statement.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}
