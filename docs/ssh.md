# SSH Setup

stackd uses SSH to authenticate with git hosts when cloning and pulling private repositories. SSH keys are managed through the dashboard **Settings UI** and stored encrypted in the stackd database — no volume mounts or env vars required.

---

## Why SSH Is Needed

Git over HTTPS requires interactive credentials that cannot be automated safely. SSH key authentication is non-interactive, works with all major git hosts (GitHub, GitLab, Gitea, self-hosted), and lets you scope access to a single deploy key per repository.

For public repositories, no authentication is needed — leave the auth method set to **None** when adding the repo.

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

Add the **public key** (`id_ed25519_stackd.pub`) as a deploy key in your git host's repository settings. Read-only access is sufficient.

---

## Adding an SSH Key in the Settings UI

1. Open the stackd dashboard → **Settings → SSH Keys**
2. Click **Add SSH Key**
3. Paste the contents of your **private key** file (e.g. `cat ~/.ssh/id_ed25519_stackd`)
4. Give it a name (e.g. `github-deploy`) and save

The key is encrypted with `SECRET_KEY` before being stored in the database and is never logged in plaintext.

To use the key for a repository:

1. Go to **Settings → Repositories** → select your repo → **Edit**
2. Set **Auth Method** to **SSH Key** and select the key you just added
3. Save

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

Because stackd sets `StrictHostKeyChecking no` in its generated SSH config, unknown hosts (GitLab, self-hosted Gitea, etc.) will connect without host key verification. This is convenient but means SSH host spoofing would not be caught at the SSH layer. Mitigate this by running stackd inside a trusted network and enforcing TLS at the reverse proxy.

---

## Troubleshooting

### Permission denied (publickey)

- Confirm the **public key** is added as a deploy key in the repository settings on your git host
- Confirm the correct SSH key is selected for the repo in **Settings → Repositories**
- Test SSH auth from within a running stackd container:

```sh
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
- Verify the correct SSH key is selected in Settings → Repositories for that repo
```

---