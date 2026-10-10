package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// StackOverride pins a single stack (identified by its directory name within a
// repo) to a specific host, overriding the repo's host.
type StackOverride struct {
	RepoID    string
	StackName string
	HostID    string
}

// GetStackOverride returns the host override for (repoID, stackName). ok is
// false when no override exists (the stack inherits its repo's host).
func GetStackOverride(ctx context.Context, db *sql.DB, repoID, stackName string) (hostID string, ok bool, err error) {
	var h string
	qerr := db.QueryRowContext(ctx,
		Rebind(`SELECT host_id FROM stack_overrides WHERE repo_id=? AND stack_name=?`),
		repoID, stackName,
	).Scan(&h)
	if errors.Is(qerr, sql.ErrNoRows) {
		return "", false, nil
	}
	if qerr != nil {
		return "", false, fmt.Errorf("getStackOverride: %w", qerr)
	}
	return h, true, nil
}

// SetStackOverride upserts a per-stack host override.
func SetStackOverride(ctx context.Context, db *sql.DB, repoID, stackName, hostID string) error {
	_, err := db.ExecContext(ctx,
		Rebind(`INSERT INTO stack_overrides (repo_id, stack_name, host_id) VALUES (?, ?, ?)
		        ON CONFLICT(repo_id, stack_name) DO UPDATE SET host_id=excluded.host_id`),
		repoID, stackName, hostID,
	)
	if err != nil {
		return fmt.Errorf("setStackOverride: %w", err)
	}
	return nil
}

// DeleteStackOverride removes a per-stack override so the stack falls back to
// its repo's host. Deleting a non-existent override is a no-op.
func DeleteStackOverride(ctx context.Context, db *sql.DB, repoID, stackName string) error {
	_, err := db.ExecContext(ctx,
		Rebind(`DELETE FROM stack_overrides WHERE repo_id=? AND stack_name=?`),
		repoID, stackName,
	)
	if err != nil {
		return fmt.Errorf("deleteStackOverride: %w", err)
	}
	return nil
}

// ListStackOverridesByRepo returns all overrides for a repo.
func ListStackOverridesByRepo(ctx context.Context, db *sql.DB, repoID string) ([]StackOverride, error) {
	rows, err := db.QueryContext(ctx,
		Rebind(`SELECT repo_id, stack_name, host_id FROM stack_overrides WHERE repo_id=? ORDER BY stack_name`),
		repoID,
	)
	if err != nil {
		return nil, fmt.Errorf("listStackOverridesByRepo: %w", err)
	}
	defer rows.Close()
	var out []StackOverride
	for rows.Next() {
		var o StackOverride
		if err := rows.Scan(&o.RepoID, &o.StackName, &o.HostID); err != nil {
			return nil, fmt.Errorf("listStackOverridesByRepo scan: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ResolveHostID applies the host precedence rules for a stack:
//
//	override → repo host → local
//
// It is pure so the precedence can be unit-tested without a database. An empty
// overrideHostID means "no override"; an empty repoHostID falls back to the
// built-in local host.
func ResolveHostID(overrideHostID, repoHostID string) string {
	if overrideHostID != "" {
		return overrideHostID
	}
	if repoHostID != "" {
		return repoHostID
	}
	return LocalHostID
}

// ResolveStackHostID resolves the effective host id for a stack by loading its
// override (if any) and applying ResolveHostID against the repo's host.
func ResolveStackHostID(ctx context.Context, db *sql.DB, repoID, stackName, repoHostID string) (string, error) {
	override, ok, err := GetStackOverride(ctx, db, repoID, stackName)
	if err != nil {
		return "", err
	}
	if !ok {
		override = ""
	}
	return ResolveHostID(override, repoHostID), nil
}
