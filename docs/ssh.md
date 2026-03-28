# SSH Setup

stackd uses SSH to authenticate with git hosts when cloning and pulling private repositories. This page explains how to configure SSH keys and troubleshoot common issues.

---

## Why SSH Is Needed

Git over HTTPS requires interactive credentials that cannot be automated safely. SSH key authentication is non-interactive, works with all major git hosts (GitHub, GitLab, Gitea, self-hosted), and lets you scope access to a single deploy key per repository.

---

## Supported Key Types

| Key Type | Recommended | Notes |
|---|---|---|
| Ed25519 | ✅ Yes | Smallest, fastest, most secure. Use this. |
| RSA (4096-bit) | ✓ Supported | Widely compatible fallback |
| ECDSA | ✓ Supported | Less common |
| DSA | ❌ No | Disabled by modern OpenSSH |

Generate an Ed25519 key:

```sh
ssh-keygen -t ed25519 -C "stackd-deploy" -f ~/.ssh/id_ed25519_stackd
```

Add the **public key** (`id_ed25519_stackd.pub`) as a deploy key in your git host's repository settings.

---

## Volume Mount and Configuration

Mount your SSH directory into the container and tell stackd where the key lives:

```yaml
services:
  stackd:
    image: ghcr.io/antnsn/stackd:latest
    environment:
      - SSH_KEY_PATH=/root/.ssh/id_ed25519
    volumes:
      - /home/user/.ssh:/root/.ssh:ro
```

`SSH_KEY_PATH` defaults to `/root/.ssh/id_rsa` if not set.

---

## How stackd Handles known_hosts

On every startup, stackd automatically:

1. Runs `ssh-keyscan github.com` to fetch GitHub's current host keys
2. Writes them to a private known_hosts file at `/tmp/stackd-ssh/known_hosts`
3. Writes an SSH `config` file that sets `IdentityFile`, `UserKnownHostsFile`, and `StrictHostKeyChecking no`
4. Sets `GIT_SSH_COMMAND` so all git operations use this config

You do **not** need to pre-populate `~/.ssh/known_hosts` manually — stackd handles it on startup.

---

## Adding Other Git Hosts

For GitLab, Gitea, or self-hosted hosts, extend `known_hosts` before stackd starts, or use a startup script:

```sh
# On the host, add keys for your git server
ssh-keyscan gitlab.com >> /home/user/.ssh/known_hosts
ssh-keyscan git.internal.example.com >> /home/user/.ssh/known_hosts
```

Because stackd sets `StrictHostKeyChecking no` in its generated SSH config, unknown hosts will not block connections — but explicitly adding keys is better practice for production.

For multiple SSH keys (e.g. one per git host), write a custom `~/.ssh/config` on the host and mount it into the container alongside the keys.

---

## Troubleshooting

### Permission denied (publickey)

- Confirm the public key is added as a deploy key in the repository settings
- Verify key permissions: `chmod 600 ~/.ssh/id_ed25519`
- Confirm the key path in `SSH_KEY_PATH` matches the filename inside the container

```sh
# Test SSH auth from within a running stackd container
docker exec -it stackd ssh -T git@github.com
```

Expected output: `Hi username! You've successfully authenticated…`

### Host key verification failed

This should not occur because stackd sets `StrictHostKeyChecking no`. If it does:

- Check that `ssh-keyscan github.com` succeeds from the container's network
- Ensure the container has outbound connectivity on port 22

### Repository not found / access denied

- The deploy key must have **read** access to the repository
- Confirm the git remote URL uses SSH format: `git@github.com:org/repo.git` (not `https://`)
