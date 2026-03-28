# Post-Sync Hooks

Post-sync hooks let you run an arbitrary shell command after stackd successfully pulls a repository, immediately before stacks are applied.

---

## What Hooks Are and When They Run

The hook runs **after** a successful `git pull` (when the HEAD SHA has changed) and **before** `docker compose up -d` is applied to any stacks. This makes hooks useful for:

- Running additional compose files not managed by stackd
- Sending deployment notifications
- Restarting ancillary services
- Writing audit timestamps

If the hook fails (non-zero exit code), the failure is **logged as a warning** but stack applies still proceed. Hooks are best-effort — they do not gate deploys.

---

## Configuration

Set `POST_SYNC_<REPO>` to any shell command. `<REPO>` is the uppercase directory name of the repository:

```yaml
environment:
  - POST_SYNC_DOCKERS=echo "dockers repo synced"
```

The command is executed via `sh -c '<command>'`, so you can use pipes, redirects, and shell builtins. Stdout and stderr are captured and written to the stackd log at `info` level.

---

## Examples

### Run an Extra Compose File

Apply an additional compose file that lives outside the managed stacks directory:

```yaml
- POST_SYNC_DOCKERS=docker compose -f /repos/dockers/extra/monitoring.yaml up -d
```

### Send a Webhook Notification

Notify a chat system or CI pipeline after a successful sync:

```yaml
- POST_SYNC_HOMELAB=curl -s -X POST -H "Content-Type: application/json" \
    -d '{"text":"homelab repo synced"}' \
    https://hooks.slack.com/services/xxx/yyy/zzz
```

### Restart a Specific Container

Force-restart a container that doesn't pick up compose changes automatically:

```yaml
- POST_SYNC_WORK=docker restart nginx-proxy
```

### Write a Timestamp File

Record the last sync time for external monitoring:

```yaml
- POST_SYNC_DOCKERS=date -u +"%Y-%m-%dT%H:%M:%SZ" > /repos/dockers/.last-sync
```

---

## Error Handling

If the hook command exits with a non-zero status, stackd:

1. Logs a warning: `post-sync hook failed` with `repo`, `cmd`, and `err` fields
2. **Continues** to apply stacks as normal

This means a failing webhook or notification command will never block your deployment. If you need a hook failure to block deploys, wrap your command to always exit 0:

```yaml
- POST_SYNC_DOCKERS=my-critical-script.sh || true
```
