# Post-Sync Hooks

> **Note:** The `POST_SYNC_<REPO>` environment variable from earlier versions of stackd has been removed. Per-repo post-sync shell commands are not currently supported as a built-in feature.

If you need to run commands after a git pull or stack apply, the following alternatives work well.

---

## Alternatives

### React to the Activity Feed

Subscribe to `GET /api/activity` from an external script and react to `done` events:

```sh
curl -s -N \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: text/event-stream" \
  http://localhost:8080/api/activity | while IFS= read -r line; do
    if echo "$line" | grep -q '"type":"done"'; then
      echo "Deploy complete — trigger your hook here"
    fi
  done
```

This pattern works for webhooks, Slack notifications, CI triggers, or any other side-effect you previously relied on post-sync hooks for.

### Container Entrypoint Hooks

If your stack needs setup steps before the service starts, add them to the container's entrypoint or a custom init script in the compose file:

```yaml
services:
  myapp:
    image: myapp:latest
    entrypoint: ["/bin/sh", "-c", "run-migrations.sh && exec myapp"]
```

### Docker Compose Profiles

Use [Docker Compose profiles](https://docs.docker.com/compose/profiles/) to run one-off jobs (migrations, seed data) only when explicitly triggered, separate from the main services that stackd manages.

---

## Feature Requests

If per-repo post-sync hooks are important to your workflow, please open an issue at [github.com/antnsn/stackd/issues](https://github.com/antnsn/stackd/issues). The database-driven configuration model makes per-repo hook commands a natural addition as a first-class repo setting.
```

---