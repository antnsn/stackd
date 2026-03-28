# Configuration

stackd is configured through environment variables, an optional `stackd.yaml` file, or a combination of both. **Environment variables always take precedence over `stackd.yaml`, which overrides built-in defaults.**

---

## Environment Variables

### Git & Sync

| Variable | Default | Description |
|---|---|---|
| `PULL_ONLY` | `false` | Only pull from remote; never `git add`, `commit`, or `push` |
| `SYNC_INTERVAL_SECONDS` | `60` | How often (in seconds) to poll each repository |
| `GIT_USER_NAME` | `githubSync` | Git `user.name` used for automated commits |
| `GIT_USER_EMAIL` | `githubsync@localhost` | Git `user.email` used for automated commits |
| `BRANCH_DEFAULT` | `main` | Default git branch for all repos when no per-repo override is set |
| `BRANCH_<REPO>` | `BRANCH_DEFAULT` | Override branch for a specific repo (e.g. `BRANCH_HOMELAB=master`) |
| `REMOTE_<REPO>` | `origin` | Override git remote name for a specific repo (e.g. `REMOTE_HOMELAB=upstream`) |

`<REPO>` is the uppercase name of the directory mounted under `REPOS_DIR`. For a repo at `/repos/homelab`, the suffix is `HOMELAB`.

### Stacks

| Variable | Default | Description |
|---|---|---|
| `STACKS_DIR_<REPO>` | _(none)_ | Path inside the container to the stacks directory for this repo (e.g. `STACKS_DIR_DOCKERS=/repos/dockers/linuxServer/stacks`) |
| `POST_SYNC_<REPO>` | _(none)_ | Shell command to run after a successful pull for this repo, before stacks are applied |

### Dashboard

| Variable | Default | Description |
|---|---|---|
| `DASHBOARD_ENABLED` | `false` | Set to `true` to enable the web dashboard |
| `DASHBOARD_PORT` | `8080` | Port the dashboard listens on inside the container |
| `DASHBOARD_TOKEN` | _(none)_ | Bearer token to protect all `/api/*` endpoints. If unset, auth is disabled |

### Security

| Variable | Default | Description |
|---|---|---|
| `SYNC_RATE_LIMIT_SECONDS` | `5` | Minimum seconds between manual sync requests per repo (prevents hammering) |

### Infisical

| Variable | Default | Description |
|---|---|---|
| `INFISICAL_ENABLED` | `false` | Set to `true` to wrap deploys with `infisical run --` for secrets injection |
| `INFISICAL_TOKEN` | _(none)_ | Global Infisical service token (fallback when no per-stack `infisical.toml` exists) |
| `INFISICAL_ENV` | `prod` | Infisical environment name (e.g. `dev`, `staging`, `prod`) |
| `INFISICAL_URL` | _(none)_ | Base URL of a self-hosted Infisical instance (e.g. `https://infisical.example.com`) |
| `INFISICAL_TOKEN_<REPO>` | _(none)_ | Per-repo Infisical token override (e.g. `INFISICAL_TOKEN_HOMELAB=st.v3.xxx`) |

### Logging & Observability

| Variable | Default | Description |
|---|---|---|
| `LOG_FORMAT` | `json` | Log output format: `json` or `text` |
| `LOG_LEVEL` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |

### Other

| Variable | Default | Description |
|---|---|---|
| `TZ` | _(system)_ | Timezone for timestamps (e.g. `Europe/Oslo`, `America/New_York`) |
| `SSH_KEY_PATH` | `/root/.ssh/id_rsa` | Path to the SSH private key inside the container |
| `STACKD_CONFIG` | _(none)_ | Path to an optional `stackd.yaml` config file; if unset, stackd looks for `/repos/stackd.yaml` |
| `REPOS_DIR` | `/repos` | Root directory where repository directories are mounted |

---

## stackd.yaml Configuration File

For setups with many repos or complex per-repo config, a `stackd.yaml` file can replace most environment variables. Place it at `/repos/stackd.yaml` or point to it with `STACKD_CONFIG`.

### Full Annotated Example

```yaml
# stackd.yaml

# Global pull-only mode — never push commits back to remote
pullOnly: true

# Poll interval in seconds
syncIntervalSeconds: 60

# Git identity used for automated commits (only relevant when pullOnly: false)
gitUser:
  name: "githubSync"
  email: "githubsync@localhost"

# Per-repository configuration
repos:
  - name: homelab             # must match the directory name under REPOS_DIR
    stacksDir: /repos/homelab/synology/stacks
    branch: master            # track a different branch than the default
    remote: origin
    postSyncCmd: "echo 'homelab synced'"
    pullOnly: true            # per-repo override (inherits global if omitted)
    infisicalEnv: prod        # Infisical environment for this repo

  - name: work
    stacksDir: /repos/work/docker/stacks
    branch: main
    remote: origin
    postSyncCmd: "curl -s https://hooks.example.com/deploy"
```

### Schema Reference

| Field | Type | Default | Description |
|---|---|---|---|
| `pullOnly` | bool | `false` | Global pull-only mode |
| `syncIntervalSeconds` | int | `60` | Poll interval in seconds |
| `gitUser.name` | string | `githubSync` | Git author name |
| `gitUser.email` | string | `githubsync@localhost` | Git author email |
| `repos[].name` | string | _(required)_ | Must match the mounted directory name |
| `repos[].stacksDir` | string | _(none)_ | Path to Docker Compose stacks directory |
| `repos[].branch` | string | `main` | Git branch to track |
| `repos[].remote` | string | `origin` | Git remote name |
| `repos[].postSyncCmd` | string | _(none)_ | Shell command to run after pull |
| `repos[].pullOnly` | bool | global value | Per-repo pull-only override |
| `repos[].infisicalEnv` | string | `INFISICAL_ENV` | Per-repo Infisical environment |

---

## Precedence Rules

When the same setting is specified in multiple places, the following order applies (highest wins):

1. **Environment variables** — always override everything else
2. **`stackd.yaml`** — overrides built-in defaults
3. **Built-in defaults** — used when neither env var nor config file sets a value

For example, if `stackd.yaml` sets `syncIntervalSeconds: 120` and the environment has `SYNC_INTERVAL_SECONDS=30`, the effective interval is **30 seconds**.
