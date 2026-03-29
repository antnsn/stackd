package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type SSHKeyDB struct {
	ID            string
	Name          string
	PrivateKeyEnc string
	PublicKey     string
	CreatedAt     time.Time
}

func ListSSHKeys(ctx context.Context, db *sql.DB) ([]SSHKeyDB, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, private_key_enc, public_key, created_at FROM ssh_keys ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listSSHKeys: %w", err)
	}
	defer rows.Close()
	var keys []SSHKeyDB
	for rows.Next() {
		var k SSHKeyDB
		var createdAt string
		if err := rows.Scan(&k.ID, &k.Name, &k.PrivateKeyEnc, &k.PublicKey, &createdAt); err != nil {
			return nil, fmt.Errorf("listSSHKeys scan: %w", err)
		}
		k.CreatedAt, _ = time.Parse(time.DateTime, createdAt)
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func GetSSHKey(ctx context.Context, db *sql.DB, id string) (SSHKeyDB, error) {
	var k SSHKeyDB
	var createdAt string
	err := db.QueryRowContext(ctx,
		Rebind(`SELECT id, name, private_key_enc, public_key, created_at FROM ssh_keys WHERE id=?`), id,
	).Scan(&k.ID, &k.Name, &k.PrivateKeyEnc, &k.PublicKey, &createdAt)
	if err != nil {
		return SSHKeyDB{}, fmt.Errorf("getSSHKey: %w", err)
	}
	k.CreatedAt, _ = time.Parse(time.DateTime, createdAt)
	return k, nil
}

func CreateSSHKey(ctx context.Context, db *sql.DB, k SSHKeyDB) error {
	if k.ID == "" {
		k.ID = newUUID()
	}
	now := time.Now().UTC().Format(time.DateTime)
	_, err := db.ExecContext(ctx,
		Rebind(`INSERT INTO ssh_keys (id, name, private_key_enc, public_key, created_at) VALUES (?, ?, ?, ?, ?)`),
		k.ID, k.Name, k.PrivateKeyEnc, k.PublicKey, now,
	)
	if err != nil {
		return fmt.Errorf("createSSHKey: %w", err)
	}
	return nil
}

func DeleteSSHKey(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(ctx, Rebind(`DELETE FROM ssh_keys WHERE id=?`), id)
	if err != nil {
		return fmt.Errorf("deleteSSHKey: %w", err)
	}
	return nil
}
