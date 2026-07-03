package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// LocalHostID is the stable, well-known id of the built-in local host (seeded by
// migration 005). Its docker_host is "" which selects the local Docker socket.
const LocalHostID = "local"

// Default transport + remote socket for a host, applied when the caller leaves
// them empty. These mirror the column defaults in migration 006. The strings are
// duplicated (rather than imported from internal/docker) to keep the db package
// free of a dependency on the docker package.
const (
	defaultTransport    = "forward"
	defaultRemoteSocket = "/var/run/docker.sock"
	defaultDockerPath   = "docker"
)

// HostDB is a Docker host stackd can deploy stacks to. DockerHost is "" for the
// built-in local socket, or "ssh://user@host[:port]" for a remote daemon.
//
// Transport selects how a remote daemon is reached: "forward" (default) forwards
// the remote Docker socket over ssh; "dial-stdio" runs `docker system dial-stdio`
// on the remote. RemoteSocket is the Docker socket path on the remote host used
// by the "forward" transport.
type HostDB struct {
	ID           string
	Name         string
	DockerHost   string
	SSHKeyID     string
	Enabled      bool
	Transport    string
	RemoteSocket string
	DockerPath   string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ErrLocalHostImmutable is returned when a caller tries to delete the built-in
// local host, which must always exist.
var ErrLocalHostImmutable = errors.New("the built-in local host cannot be deleted")

// ErrHostInUse is returned when a host cannot be deleted because a repo still
// references it.
var ErrHostInUse = errors.New("host is still referenced by one or more repos")

func scanHost(rows interface {
	Scan(dest ...any) error
}) (HostDB, error) {
	var h HostDB
	var sshKeyID sql.NullString
	var enabled int
	var createdAt, updatedAt string
	err := rows.Scan(&h.ID, &h.Name, &h.DockerHost, &sshKeyID, &enabled, &h.Transport, &h.RemoteSocket, &h.DockerPath, &createdAt, &updatedAt)
	if err != nil {
		return HostDB{}, err
	}
	h.SSHKeyID = sshKeyID.String
	h.Enabled = enabled != 0
	h.CreatedAt, _ = time.Parse(time.DateTime, createdAt)
	h.UpdatedAt, _ = time.Parse(time.DateTime, updatedAt)
	return h, nil
}

func ListHosts(ctx context.Context, db *sql.DB) ([]HostDB, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, docker_host, ssh_key_id, enabled, transport, remote_socket, docker_path, created_at, updated_at
         FROM hosts ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listHosts: %w", err)
	}
	defer rows.Close()
	var hosts []HostDB
	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, fmt.Errorf("listHosts scan: %w", err)
		}
		hosts = append(hosts, h)
	}
	return hosts, rows.Err()
}

func GetHost(ctx context.Context, db *sql.DB, id string) (HostDB, error) {
	row := db.QueryRowContext(ctx,
		Rebind(`SELECT id, name, docker_host, ssh_key_id, enabled, transport, remote_socket, docker_path, created_at, updated_at
         FROM hosts WHERE id = ?`), id)
	h, err := scanHost(row)
	if err != nil {
		return HostDB{}, fmt.Errorf("getHost: %w", err)
	}
	return h, nil
}

// normalizeTransport applies the column defaults so an empty Transport /
// RemoteSocket round-trips as the documented defaults rather than "".
func normalizeTransport(h *HostDB) {
	if h.Transport == "" {
		h.Transport = defaultTransport
	}
	if h.RemoteSocket == "" {
		h.RemoteSocket = defaultRemoteSocket
	}
	if h.DockerPath == "" {
		h.DockerPath = defaultDockerPath
	}
}

func CreateHost(ctx context.Context, db *sql.DB, h HostDB) (HostDB, error) {
	if h.ID == "" {
		h.ID = newUUID()
	}
	normalizeTransport(&h)
	now := time.Now().UTC().Format(time.DateTime)
	_, err := db.ExecContext(ctx,
		Rebind(`INSERT INTO hosts (id, name, docker_host, ssh_key_id, enabled, transport, remote_socket, docker_path, created_at, updated_at)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		h.ID, h.Name, h.DockerHost, nullStr(h.SSHKeyID), boolInt(h.Enabled), h.Transport, h.RemoteSocket, h.DockerPath, now, now,
	)
	if err != nil {
		return HostDB{}, fmt.Errorf("createHost: %w", err)
	}
	h.CreatedAt, _ = time.Parse(time.DateTime, now)
	h.UpdatedAt = h.CreatedAt
	return h, nil
}

func UpdateHost(ctx context.Context, db *sql.DB, h HostDB) error {
	normalizeTransport(&h)
	now := time.Now().UTC().Format(time.DateTime)
	_, err := db.ExecContext(ctx,
		Rebind(`UPDATE hosts SET name=?, docker_host=?, ssh_key_id=?, enabled=?, transport=?, remote_socket=?, docker_path=?, updated_at=?
         WHERE id=?`),
		h.Name, h.DockerHost, nullStr(h.SSHKeyID), boolInt(h.Enabled), h.Transport, h.RemoteSocket, h.DockerPath, now, h.ID,
	)
	if err != nil {
		return fmt.Errorf("updateHost: %w", err)
	}
	return nil
}

// CountReposUsingHost returns how many repos currently reference the host.
func CountReposUsingHost(ctx context.Context, db *sql.DB, hostID string) (int, error) {
	var n int
	err := db.QueryRowContext(ctx,
		Rebind(`SELECT COUNT(*) FROM repos WHERE host_id = ?`), hostID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("countReposUsingHost: %w", err)
	}
	return n, nil
}

// DeleteHost removes a host. It refuses to delete the built-in local host, and
// refuses to delete a host that is still referenced by a repo (returns
// ErrHostInUse) so stacks never end up pointing at a missing host.
func DeleteHost(ctx context.Context, db *sql.DB, id string) error {
	if id == LocalHostID {
		return ErrLocalHostImmutable
	}
	n, err := CountReposUsingHost(ctx, db, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrHostInUse
	}
	if _, err := db.ExecContext(ctx, Rebind(`DELETE FROM hosts WHERE id=?`), id); err != nil {
		return fmt.Errorf("deleteHost: %w", err)
	}
	// Clean up any per-stack overrides that pointed at this host.
	if _, err := db.ExecContext(ctx, Rebind(`DELETE FROM stack_overrides WHERE host_id=?`), id); err != nil {
		return fmt.Errorf("deleteHost overrides: %w", err)
	}
	return nil
}
