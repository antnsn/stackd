package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"time"
)

type RepoDB struct {
	ID           string
	Name         string
	URL          string
	Branch       string
	Remote       string
	AuthType     string
	SSHKeyID     string
	PATEnc       string
	StacksDir    string
	SyncInterval int
	Enabled      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func newUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func scanRepo(rows interface {
	Scan(dest ...any) error
}) (RepoDB, error) {
	var r RepoDB
	var sshKeyID, patEnc sql.NullString
	var enabled int
	var createdAt, updatedAt string
	err := rows.Scan(
		&r.ID, &r.Name, &r.URL, &r.Branch, &r.Remote,
		&r.AuthType, &sshKeyID, &patEnc, &r.StacksDir,
		&r.SyncInterval, &enabled, &createdAt, &updatedAt,
	)
	if err != nil {
		return RepoDB{}, err
	}
	r.SSHKeyID = sshKeyID.String
	r.PATEnc = patEnc.String
	r.Enabled = enabled != 0
	r.CreatedAt, _ = time.Parse(time.DateTime, createdAt)
	r.UpdatedAt, _ = time.Parse(time.DateTime, updatedAt)
	return r, nil
}

func ListRepos(ctx context.Context, db *sql.DB) ([]RepoDB, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, url, branch, remote, auth_type, ssh_key_id, pat_enc,
                stacks_dir, sync_interval, enabled, created_at, updated_at
         FROM repos ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listRepos: %w", err)
	}
	defer rows.Close()
	var repos []RepoDB
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, fmt.Errorf("listRepos scan: %w", err)
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

func GetRepo(ctx context.Context, db *sql.DB, id string) (RepoDB, error) {
	row := db.QueryRowContext(ctx,
		Rebind(`SELECT id, name, url, branch, remote, auth_type, ssh_key_id, pat_enc,
                stacks_dir, sync_interval, enabled, created_at, updated_at
         FROM repos WHERE id = ?`), id)
	r, err := scanRepo(row)
	if err != nil {
		return RepoDB{}, fmt.Errorf("getRepo: %w", err)
	}
	return r, nil
}

func CreateRepo(ctx context.Context, db *sql.DB, r RepoDB) error {
	if r.ID == "" {
		r.ID = newUUID()
	}
	now := time.Now().UTC().Format(time.DateTime)
	_, err := db.ExecContext(ctx,
		Rebind(`INSERT INTO repos (id, name, url, branch, remote, auth_type, ssh_key_id, pat_enc,
                            stacks_dir, sync_interval, enabled, created_at, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		r.ID, r.Name, r.URL, r.Branch, r.Remote,
		r.AuthType, nullStr(r.SSHKeyID), nullStr(r.PATEnc),
		r.StacksDir, r.SyncInterval, boolInt(r.Enabled), now, now,
	)
	if err != nil {
		return fmt.Errorf("createRepo: %w", err)
	}
	return nil
}

func UpdateRepo(ctx context.Context, db *sql.DB, r RepoDB) error {
	now := time.Now().UTC().Format(time.DateTime)
	_, err := db.ExecContext(ctx,
		Rebind(`UPDATE repos SET name=?, url=?, branch=?, remote=?, auth_type=?,
                          ssh_key_id=?, pat_enc=?, stacks_dir=?, sync_interval=?,
                          enabled=?, updated_at=?
         WHERE id=?`),
		r.Name, r.URL, r.Branch, r.Remote, r.AuthType,
		nullStr(r.SSHKeyID), nullStr(r.PATEnc),
		r.StacksDir, r.SyncInterval, boolInt(r.Enabled), now, r.ID,
	)
	if err != nil {
		return fmt.Errorf("updateRepo: %w", err)
	}
	return nil
}

func DeleteRepo(ctx context.Context, db *sql.DB, id string) error {
	_, err := db.ExecContext(ctx, Rebind(`DELETE FROM repos WHERE id=?`), id)
	if err != nil {
		return fmt.Errorf("deleteRepo: %w", err)
	}
	return nil
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
