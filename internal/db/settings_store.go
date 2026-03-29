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

func SetSetting(ctx context.Context, db *sql.DB, key, value string) error {
	now := time.Now().UTC().Format(time.DateTime)
	_, err := db.ExecContext(ctx,
		Rebind(`UPDATE settings SET value=?, updated_at=? WHERE key=?`), value, now, key,
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
