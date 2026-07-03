// Package hostres resolves a stack (or repo) to the Docker host it should run
// on and hands back a ready docker.Client for that host. It centralises the
// host-precedence rules (override → repo → local), the decryption and on-disk
// management of per-host SSH keys, and the construction of the environment a
// `docker compose` subprocess needs to talk to a remote daemon over ssh://.
package hostres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"stackd/internal/crypto"
	"stackd/internal/db"
	"stackd/internal/docker"
)

// HostInfo is the resolved, decryption-free view of a host used for badging in
// the UI and for building clients / compose env. It never carries key material.
type HostInfo struct {
	ID         string
	Name       string
	DockerHost string // "" = local socket
	SSHKeyID   string
	Enabled    bool
}

// IsLocal reports whether this host uses the local Docker socket.
func (h HostInfo) IsLocal() bool { return h.DockerHost == "" }

// ErrHostDisabled is returned when a stack/repo resolves to a remote host that
// the operator has disabled. The local host is never disable-able.
var ErrHostDisabled = errors.New("host is disabled")

// checkEnabled blocks remote work for a disabled host so the enabled toggle has
// operational effect (no deploy, no logs/exec/actions). Local is always usable.
func (h HostInfo) checkEnabled() error {
	if !h.IsLocal() && !h.Enabled {
		return fmt.Errorf("%s: %w", h.Name, ErrHostDisabled)
	}
	return nil
}

// Resolver builds docker clients for hosts, caching them through a
// docker.Registry. It owns a private directory (typically <cloneDir>/.ssh) into
// which it writes decrypted per-host keys (0600) and per-host ssh config for
// compose.
type Resolver struct {
	db         *sql.DB
	reg        *docker.Registry
	cryptoKey  []byte
	sshDir     string // private dir (0700) for keys + known_hosts + compose HOME dirs
	knownHosts string // managed known_hosts file shared with git
}

// New constructs a Resolver. sshDir is the private ssh directory (already
// created 0700 by main); knownHosts is the managed known_hosts file path.
func New(sqlDB *sql.DB, reg *docker.Registry, cryptoKey []byte, sshDir, knownHosts string) *Resolver {
	return &Resolver{
		db:         sqlDB,
		reg:        reg,
		cryptoKey:  cryptoKey,
		sshDir:     sshDir,
		knownHosts: knownHosts,
	}
}

// LoadHost fetches a host by id and returns its decryption-free info.
func (r *Resolver) LoadHost(ctx context.Context, hostID string) (HostInfo, error) {
	if hostID == "" {
		hostID = db.LocalHostID
	}
	h, err := db.GetHost(ctx, r.db, hostID)
	if err != nil {
		return HostInfo{}, fmt.Errorf("load host %q: %w", hostID, err)
	}
	return HostInfo{
		ID:         h.ID,
		Name:       h.Name,
		DockerHost: h.DockerHost,
		SSHKeyID:   h.SSHKeyID,
		Enabled:    h.Enabled,
	}, nil
}

// ResolveStackHostID applies override → repo → local precedence for a stack.
func (r *Resolver) ResolveStackHostID(ctx context.Context, repoID, stackName, repoHostID string) (string, error) {
	return db.ResolveStackHostID(ctx, r.db, repoID, stackName, repoHostID)
}

// ResolveStackHost resolves and loads the effective host for a stack in one call.
func (r *Resolver) ResolveStackHost(ctx context.Context, repoID, stackName, repoHostID string) (HostInfo, error) {
	hostID, err := r.ResolveStackHostID(ctx, repoID, stackName, repoHostID)
	if err != nil {
		return HostInfo{}, err
	}
	return r.LoadHost(ctx, hostID)
}

// writeHostKey decrypts the host's SSH key and writes it to a stable per-host
// path (0600), overwriting any previous copy so keys never accumulate. It
// returns the key file path.
func (r *Resolver) writeHostKey(ctx context.Context, info HostInfo) (string, error) {
	if info.SSHKeyID == "" {
		return "", fmt.Errorf("host %q has no ssh key configured", info.Name)
	}
	keyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	sshKey, err := db.GetSSHKey(keyCtx, r.db, info.SSHKeyID)
	cancel()
	if err != nil {
		return "", fmt.Errorf("get ssh key for host %q: %w", info.Name, err)
	}
	privKey, err := crypto.Decrypt(r.cryptoKey, sshKey.PrivateKeyEnc)
	if err != nil {
		return "", fmt.Errorf("decrypt ssh key for host %q: %w", info.Name, err)
	}
	// OpenSSH rejects a private key lacking a trailing newline (see setupGitAuth).
	if !strings.HasSuffix(privKey, "\n") {
		privKey += "\n"
	}
	path := filepath.Join(r.sshDir, "host-"+info.ID+".key")
	if err := os.WriteFile(path, []byte(privKey), 0600); err != nil {
		return "", fmt.Errorf("write ssh key for host %q: %w", info.Name, err)
	}
	return path, nil
}

// spec builds the docker.HostSpec for a host, materialising its key file when
// remote.
func (r *Resolver) spec(ctx context.Context, info HostInfo) (docker.HostSpec, error) {
	if info.IsLocal() {
		return docker.HostSpec{ID: info.ID}, nil
	}
	keyPath, err := r.writeHostKey(ctx, info)
	if err != nil {
		return docker.HostSpec{}, err
	}
	return docker.HostSpec{
		ID:             info.ID,
		DockerHost:     info.DockerHost,
		KeyPath:        keyPath,
		KnownHostsFile: r.knownHosts,
	}, nil
}

// Client returns a cached-or-freshly-built docker.Client for the host. When a
// client is already cached it skips key decryption / file writes entirely, so
// hot paths (per-tick container refresh) stay cheap for remote hosts.
func (r *Resolver) Client(ctx context.Context, info HostInfo) (*docker.Client, error) {
	if err := info.checkEnabled(); err != nil {
		return nil, err
	}
	if c := r.reg.Cached(info.ID); c != nil {
		return c, nil
	}
	spec, err := r.spec(ctx, info)
	if err != nil {
		return nil, err
	}
	return r.reg.Get(ctx, spec)
}

// ClientForHostID resolves a host id to a client and its info.
func (r *Resolver) ClientForHostID(ctx context.Context, hostID string) (*docker.Client, HostInfo, error) {
	info, err := r.LoadHost(ctx, hostID)
	if err != nil {
		return nil, HostInfo{}, err
	}
	cli, err := r.Client(ctx, info)
	if err != nil {
		return nil, info, err
	}
	return cli, info, nil
}

// ClientForStack resolves the effective host for a stack and returns its client
// plus the resolved host info (for badging).
func (r *Resolver) ClientForStack(ctx context.Context, repoID, stackName, repoHostID string) (*docker.Client, HostInfo, error) {
	info, err := r.ResolveStackHost(ctx, repoID, stackName, repoHostID)
	if err != nil {
		return nil, HostInfo{}, err
	}
	cli, err := r.Client(ctx, info)
	if err != nil {
		return nil, info, err
	}
	return cli, info, nil
}

// ComposeEnv returns the extra environment a `docker compose` subprocess needs
// to target the given host, plus a cleanup func. For a local host it returns
// nil (compose talks to the local socket exactly as before). For a remote host
// it materialises the key and a private HOME containing an .ssh/config so the
// system ssh that `docker compose` invokes over DOCKER_HOST=ssh://… finds the
// right IdentityFile and known_hosts.
//
// NOTE: host-path bind mounts in a remote stack's compose file resolve on the
// REMOTE daemon's filesystem, not on the stackd host — operators must ensure
// those paths exist on the target host.
func (r *Resolver) ComposeEnv(ctx context.Context, info HostInfo) (env []string, cleanup func(), err error) {
	cleanup = func() {}
	if info.IsLocal() {
		return nil, cleanup, nil
	}
	if err := info.checkEnabled(); err != nil {
		return nil, cleanup, err
	}
	keyPath, err := r.writeHostKey(ctx, info)
	if err != nil {
		return nil, cleanup, err
	}
	user, host, port, err := docker.ParseSSHHost(info.DockerHost)
	if err != nil {
		return nil, cleanup, err
	}
	homeDir := filepath.Join(r.sshDir, "host-"+info.ID+"-home")
	sshCfgDir := filepath.Join(homeDir, ".ssh")
	if err := os.MkdirAll(sshCfgDir, 0700); err != nil {
		return nil, cleanup, fmt.Errorf("create compose ssh dir for host %q: %w", info.Name, err)
	}
	cfg := BuildSSHConfig(host, user, port, keyPath, r.knownHosts)
	cfgPath := filepath.Join(sshCfgDir, "config")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0600); err != nil {
		return nil, cleanup, fmt.Errorf("write compose ssh config for host %q: %w", info.Name, err)
	}
	env = []string{
		"DOCKER_HOST=" + info.DockerHost,
		"HOME=" + homeDir,
	}
	return env, cleanup, nil
}

// BuildSSHConfig renders the ~/.ssh/config block that lets the system ssh (as
// spawned by `docker compose` over DOCKER_HOST=ssh://…) authenticate to a
// remote host non-interactively. It is pure so it can be unit-tested. Empty
// user/port are omitted (ssh uses its defaults, e.g. the URL-supplied values).
func BuildSSHConfig(host, user, port, keyPath, knownHosts string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Host %s\n", host)
	if user != "" {
		fmt.Fprintf(&b, "    User %s\n", user)
	}
	if port != "" {
		fmt.Fprintf(&b, "    Port %s\n", port)
	}
	if keyPath != "" {
		fmt.Fprintf(&b, "    IdentityFile %s\n", keyPath)
		fmt.Fprintf(&b, "    IdentitiesOnly yes\n")
	}
	if knownHosts != "" {
		fmt.Fprintf(&b, "    UserKnownHostsFile %s\n", knownHosts)
	}
	fmt.Fprintf(&b, "    StrictHostKeyChecking accept-new\n")
	fmt.Fprintf(&b, "    BatchMode yes\n")
	fmt.Fprintf(&b, "    ConnectTimeout 10\n")
	return b.String()
}
