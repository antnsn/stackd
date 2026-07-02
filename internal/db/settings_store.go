package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func GetSetting(ctx context.Context, db *sql.DB, key string) (value string, sensitive bool, err error) {
	var sensitiveInt int
	var nullValue sql.NullString
	err = db.QueryRowContext(ctx,
		Rebind(`SELECT value, sensitive FROM settings WHERE key=?`), key,
	).Scan(&nullValue, &sensitiveInt)
	if err != nil {
		return "", false, fmt.Errorf("getSetting %q: %w", key, err)
	}
	return nullValue.String, sensitiveInt != 0, nil
}

// sensitiveKeys are settings whose values are secrets and must be stored with
// sensitive=1 so GetAllSettings masks them.
var sensitiveKeys = map[string]bool{
	"dashboard_token": true,
	"infisical_token": true,
}

// SetSetting upserts a setting. Using INSERT ... ON CONFLICT DO UPDATE (portable
// across sqlite and postgres) means it works even when the row does not yet
// exist — the previous UPDATE-only implementation silently no-op'd for missing
// keys (e.g. a generated dashboard_token on a DB seeded before migration 004).
// The sensitive flag is only applied on insert; existing rows keep their flag.
func SetSetting(ctx context.Context, db *sql.DB, key, value string) error {
	now := time.Now().UTC().Format(time.DateTime)
	sensitive := 0
	if sensitiveKeys[key] {
		sensitive = 1
	}
	_, err := db.ExecContext(ctx,
		Rebind(`INSERT INTO settings (key, value, updated_at, sensitive) VALUES (?, ?, ?, ?)
		        ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`),
		key, value, now, sensitive,
	)
	if err != nil {
		return fmt.Errorf("setSetting %q: %w", key, err)
	}
	return nil
}

func GetAllSettings(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value, sensitive FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("getAllSettings: %w", err)
	}
	defer rows.Close()
	result := make(map[string]string)
	for rows.Next() {
		var k string
		var nullValue sql.NullString
		var sensitiveInt int
		if err := rows.Scan(&k, &nullValue, &sensitiveInt); err != nil {
			return nil, fmt.Errorf("getAllSettings scan: %w", err)
		}
		if sensitiveInt != 0 {
			result[k] = ""
		} else {
			result[k] = nullValue.String
		}
	}
	return result, rows.Err()
}
