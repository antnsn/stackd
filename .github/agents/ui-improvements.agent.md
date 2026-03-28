---
name: "UI Improvements"
description: "Full modernisation of the stackd dashboard: restore repo/stack hierarchy, bigger logo, force git sync per repo, Docker container env vars, container tabs with logs/env/info, modern styling, and live auto-refresh."
tools: ["search", "read_file", "edit_file", "run_terminal_command"]
model: "claude-sonnet-4.6"
---

# stackd UI Improvements Agent

You are a Preact/JavaScript engineer for **stackd**. A thorough UX audit has been
completed. Your job is to implement the full modernisation of the dashboard.

**Prerequisite:** All backend agents (Phases 1–3) must have completed.

## Audit Findings Summary

Critical issues found:
1. Data model is wrong — flattens repos→stacks→containers into a flat list; all context lost
2. Force sync button missing — POST /api/sync/{repo} exists but is never called
3. Logo is only 40px height — barely visible
4. No env vars shown — needs backend + frontend work
5. No auto-refresh — data goes stale after mount
6. HTML title still says "SimpleGithubSync"
7. Container uptime (startedAt) available but unused
8. Log timestamps and level colours missing — CSS classes exist but unused
9. Silent failure if API is down — no error state

---

## Backend Changes Required First

### A. Add Env and Ports to ContainerDetail in internal/state/state.go

```go
type ContainerDetail struct {
    ID        string    `json:"id"`
    Name      string    `json:"name"`
    Image     string    `json:"image"`
    Status    string    `json:"status"`
    StartedAt time.Time `json:"startedAt"`
    Env       []string  `json:"env"`   // ["KEY=value", ...] — sensitive values masked
    Ports     []string  `json:"ports"` // ["8080:80/tcp", ...]
}
```

### B. Populate Env and Ports in internal/docker/client.go

In ListStackContainerDetails(), extract from ContainerInspect result:
- container.Config.Env → filter secrets (mask keys containing TOKEN, SECRET, KEY, PASSWORD, PASS)
- container.NetworkSettings.Ports → format as "hostPort:containerPort/proto"

Masking helper:
```go
func maskEnvVars(envs []string) []string {
    sensitiveSubstrings := []string{"TOKEN", "SECRET", "KEY", "PASSWORD", "PASS", "CREDENTIAL"}
    result := make([]string, 0, len(envs))
    for _, e := range envs {
        parts := strings.SplitN(e, "=", 2)
        if len(parts) == 2 {
            upper := strings.ToUpper(parts[0])
            masked := false
            for _, sub := range sensitiveSubstrings {
                if strings.Contains(upper, sub) {
                    result = append(result, parts[0]+"=[redacted]")
                    masked = true
                    break
                }
            }
            if !masked {
                result = append(result, e)
            }
        } else {
            result = append(result, e)
        }
    }
    return result
}
```

### C. Verify backend builds
```bash
go build ./...
```

---

## Frontend Changes

All files are in internal/ui/src/.

### Task 1 — Fix HTML title (index.html)
```html
<title>stackd</title>
<meta name="description" content="GitOps dashboard for Docker Compose deployments">
<meta name="theme-color" content="#1a1a1a">
```

### Task 2 — Rewrite App.jsx

Keep full repo→stack hierarchy. Add auto-refresh every 5s. Add force sync handler.

```jsx
import { useState, useEffect } from 'preact/hooks'
import { AppGrid } from './components/AppGrid'
import { AppDetail } from './components/AppDetail'

export function App() {
  const [repos, setRepos] = useState([])
  const [infisical, setInfisical] = useState(null)
  const [selectedStack, setSelectedStack] = useState(null)
  const [error, setError] = useState(null)
  const [syncingRepos, setSyncingRepos] = useState(new Set())

  const fetchStatus = () => {
    fetch('/api/status')
      .then(r => r.json())
      .then(data => { setRepos(data.repos || []); setInfisical(data.infisical); setError(null) })
      .catch(err => setError(err.message))
  }

  useEffect(() => {
    fetchStatus()
    const interval = setInterval(fetchStatus, 5000)
    return () => clearInterval(interval)
  }, [])

  const handleForceSync = (repoName) => {
    setSyncingRepos(prev => new Set([...prev, repoName]))
    fetch(`/api/sync/${repoName}`, { method: 'POST' })
      .finally(() => setTimeout(() => {
        setSyncingRepos(prev => { const s = new Set(prev); s.delete(repoName); return s })
      }, 3000))
  }

  return (
    <div class="app-shell">
      <header class="app-header">
        <div class="app-header__brand">
          <img src="/logo.svg" alt="stackd" class="app-logo" />
        </div>
        <div class="app-header__meta">
          {infisical?.enabled && (
            <span class="infisical-badge">🔒 Infisical: {infisical.env}</span>
          )}
        </div>
      </header>

      {error && (
        <div class="error-banner">
          <span>⚠ Failed to load: {error}</span>
          <button onClick={fetchStatus}>Retry</button>
        </div>
      )}

      <div class="app-container">
        <div class="grid-panel">
          <AppGrid
            repos={repos}
            selectedStack={selectedStack}
            syncingRepos={syncingRepos}
            onSelectStack={setSelectedStack}
            onForceSync={handleForceSync}
          />
        </div>
        {selectedStack && (
          <div class="detail-panel">
            <AppDetail stack={selectedStack} onClose={() => setSelectedStack(null)} />
          </div>
        )}
      </div>
    </div>
  )
}
```

### Task 3 — Rewrite AppGrid.jsx with repo/stack/container hierarchy

Show Repo groups → Stack cards → container count. Force sync button per repo.

```jsx
import { formatRelative } from '../utils/time'

export function AppGrid({ repos, selectedStack, syncingRepos, onSelectStack, onForceSync }) {
  if (!repos.length) {
    return (
      <div class="empty-state">
        <p>No repositories found.</p>
        <p class="empty-state__hint">Mount git repos into REPOS_DIR to get started.</p>
      </div>
    )
  }
  return (
    <div class="app-grid">
      {repos.map(repo => (
        <RepoGroup key={repo.name} repo={repo} selectedStack={selectedStack}
          isSyncing={syncingRepos.has(repo.name)} onSelectStack={onSelectStack} onForceSync={onForceSync} />
      ))}
    </div>
  )
}

function RepoGroup({ repo, selectedStack, isSyncing, onSelectStack, onForceSync }) {
  return (
    <div class="repo-group">
      <div class="repo-header">
        <div class="repo-header__left">
          <span class={`repo-status-dot repo-status-dot--${repo.status}`} />
          <span class="repo-name">{repo.name}</span>
          {repo.lastSha && <span class="repo-sha">{repo.lastSha.slice(0, 7)}</span>}
        </div>
        <div class="repo-header__right">
          {repo.lastSync && <span class="repo-last-sync">{formatRelative(repo.lastSync)}</span>}
          <button class={`sync-btn ${isSyncing ? 'sync-btn--spinning' : ''}`}
            onClick={() => onForceSync(repo.name)} title="Force git sync" disabled={isSyncing}>↻</button>
        </div>
      </div>
      {repo.lastError && <div class="repo-error">{repo.lastError}</div>}
      <div class="stack-list">
        {(repo.stacks || []).map(stack => (
          <StackCard key={stack.name} stack={stack} repoName={repo.name}
            isSelected={selectedStack?.name === stack.name && selectedStack?.repoName === repo.name}
            onSelect={() => onSelectStack({ ...stack, repoName: repo.name })} />
        ))}
        {(!repo.stacks || !repo.stacks.length) && <div class="empty-stacks">No stacks configured</div>}
      </div>
    </div>
  )
}

function StackCard({ stack, isSelected, onSelect }) {
  const status = deriveStackStatus(stack)
  const running = (stack.containers || []).filter(c => c.status === 'running').length
  const total = (stack.containers || []).length
  return (
    <div class={`stack-card stack-card--${status} ${isSelected ? 'stack-card--selected' : ''}`} onClick={onSelect}>
      <div class="stack-card__header">
        <span class="stack-card__name">{stack.name}</span>
        <span class={`stack-badge stack-badge--${status}`}>{status}</span>
      </div>
      <div class="stack-card__meta">
        <span class="container-count">{running}/{total} running</span>
        {stack.lastApply && <span class="stack-last-apply">{formatRelative(stack.lastApply)}</span>}
      </div>
      {stack.lastError && <div class="stack-error">{stack.lastError}</div>}
    </div>
  )
}

function deriveStackStatus(stack) {
  if (stack.status === 'error') return 'error'
  if (stack.status === 'applying') return 'applying'
  const containers = stack.containers || []
  if (!containers.length) return 'unknown'
  if (containers.every(c => c.status === 'running')) return 'running'
  if (containers.some(c => c.status === 'running')) return 'degraded'
  return 'stopped'
}
```

### Task 4 — Rewrite AppDetail.jsx with container tabs (Logs / Env / Info)

```jsx
import { useState, useEffect, useRef } from 'preact/hooks'
import { formatRelative, formatDateTime } from '../utils/time'

export function AppDetail({ stack, onClose }) {
  const [selectedContainer, setSelectedContainer] = useState(stack.containers?.[0]?.name ?? null)
  useEffect(() => setSelectedContainer(stack.containers?.[0]?.name ?? null), [stack.name])
  const container = stack.containers?.find(c => c.name === selectedContainer)

  return (
    <div class="app-detail">
      <div class="detail-header">
        <div class="detail-header__title">
          <span class="detail-repo">{stack.repoName}</span>
          <span class="detail-sep">/</span>
          <h2 class="detail-stack-name">{stack.name}</h2>
          <span class={`stack-badge stack-badge--${stack.status}`}>{stack.status}</span>
        </div>
        <button class="close-btn" onClick={onClose}>✕</button>
      </div>

      <div class="stack-meta-grid">
        {stack.lastApply && <MetaItem label="Last deployed" value={formatRelative(stack.lastApply)} />}
        {stack.stackDir && <MetaItem label="Directory" value={stack.stackDir} mono />}
        {stack.lastError && <MetaItem label="Error" value={stack.lastError} error />}
      </div>

      {stack.containers?.length > 0 && (
        <>
          <div class="container-tabs">
            {stack.containers.map(c => (
              <button key={c.name}
                class={`container-tab ${c.name === selectedContainer ? 'container-tab--active' : ''}`}
                onClick={() => setSelectedContainer(c.name)}>
                <span class={`status-dot status-dot--${c.status}`} />{c.name}
              </button>
            ))}
          </div>
          {container && <ContainerDetail container={container} />}
        </>
      )}
    </div>
  )
}

function MetaItem({ label, value, mono, error }) {
  return (
    <div class={`meta-item ${error ? 'meta-item--error' : ''}`}>
      <span class="meta-label">{label}</span>
      <span class={`meta-value ${mono ? 'meta-value--mono' : ''}`}>{value}</span>
    </div>
  )
}

function ContainerDetail({ container }) {
  const [tab, setTab] = useState('logs')
  return (
    <div class="container-detail">
      <div class="info-tabs">
        {[['logs','📋 Logs'],['env','⚙ Env'],['info','ℹ Info']].map(([t, label]) => (
          <button key={t} class={`info-tab ${tab === t ? 'info-tab--active' : ''}`} onClick={() => setTab(t)}>{label}</button>
        ))}
      </div>
      {tab === 'logs' && <LogStream container={container.name} />}
      {tab === 'env' && <EnvVars envs={container.env} />}
      {tab === 'info' && <ContainerInfo container={container} />}
    </div>
  )
}

function LogStream({ container }) {
  const [logs, setLogs] = useState([])
  const endRef = useRef(null)
  useEffect(() => {
    setLogs([])
    const es = new EventSource(`/api/logs/${container}`)
    es.onmessage = e => setLogs(prev => [...prev, { text: e.data, time: new Date() }].slice(-200))
    es.onerror = () => es.close()
    return () => es.close()
  }, [container])
  useEffect(() => endRef.current?.scrollIntoView({ behavior: 'smooth' }), [logs])
  return (
    <div class="logs-content">
      {!logs.length && <div class="logs-empty">Waiting for logs…</div>}
      {logs.map((entry, i) => (
        <div key={i} class={`log-line ${classifyLog(entry.text)}`}>
          <span class="log-time">{entry.time.toLocaleTimeString()}</span>
          <span class="log-text">{entry.text}</span>
        </div>
      ))}
      <div ref={endRef} />
    </div>
  )
}

function EnvVars({ envs }) {
  if (!envs?.length) return <div class="empty-state">No environment variables.</div>
  return (
    <div class="env-list">
      {envs.map((e, i) => {
        const eq = e.indexOf('=')
        const key = eq >= 0 ? e.slice(0, eq) : e
        const val = eq >= 0 ? e.slice(eq + 1) : ''
        return (
          <div key={i} class="env-item">
            <span class="env-key">{key}</span>
            <span class={`env-value ${val === '[redacted]' ? 'env-value--redacted' : ''}`}>
              {val === '[redacted]' ? '••••••' : val}
            </span>
          </div>
        )
      })}
    </div>
  )
}

function ContainerInfo({ container }) {
  const { formatRelative, formatDateTime } = require('../utils/time')
  return (
    <div class="container-info">
      <InfoRow label="Container ID" value={container.id?.slice(0, 12)} mono />
      <InfoRow label="Image" value={container.image} mono />
      <InfoRow label="Status" value={container.status} />
      {container.startedAt && (
        <InfoRow label="Started" value={`${formatRelative(container.startedAt)} · ${formatDateTime(container.startedAt)}`} />
      )}
      {container.ports?.length > 0 && <InfoRow label="Ports" value={container.ports.join(', ')} mono />}
    </div>
  )
}

function InfoRow({ label, value, mono }) {
  return (
    <div class="info-row">
      <span class="info-label">{label}</span>
      <span class={`info-value ${mono ? 'info-value--mono' : ''}`}>{value || '—'}</span>
    </div>
  )
}

function classifyLog(text) {
  const l = text.toLowerCase()
  if (l.includes('error') || l.includes('fatal') || l.includes('panic')) return 'log-error'
  if (l.includes('warn')) return 'log-warn'
  if (l.includes('debug')) return 'log-debug'
  return ''
}
```

### Task 5 — Create src/utils/time.js

```js
export function formatRelative(dateStr) {
  if (!dateStr) return ''
  const diff = Date.now() - new Date(dateStr).getTime()
  const secs = Math.floor(diff / 1000)
  if (secs < 60) return `${secs}s ago`
  const mins = Math.floor(secs / 60)
  if (mins < 60) return `${mins}m ago`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours}h ago`
  return `${Math.floor(hours / 24)}d ago`
}

export function formatDateTime(dateStr) {
  if (!dateStr) return ''
  return new Date(dateStr).toLocaleString()
}
```

### Task 6 — Modernise CSS (index.css + app.css + component CSS)

Key CSS variables in index.css:
```css
:root {
  --bg-primary: #0d1117;
  --bg-secondary: #161b22;
  --bg-tertiary: #21262d;
  --border-color: #30363d;
  --text-primary: #e6edf3;
  --text-secondary: #8b949e;
  --accent: #00d4ff;
  --status-running: #3fb950;
  --status-stopped: #f85149;
  --status-degraded: #d29922;
  --status-applying: #58a6ff;
  --status-error: #f85149;
  --status-unknown: #8b949e;
}

@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }
.sync-btn--spinning { animation: spin 1s linear infinite; display: inline-block; }

@media (max-width: 768px) {
  .app-container { flex-direction: column; }
  .grid-panel { max-height: 45vh; border-bottom: 1px solid var(--border-color); overflow-y: auto; }
}
```

app-logo height: 36px (larger, crisp). Write all required classes for every new component.

### Task 7 — Build + commit

```bash
cd internal/ui && npm install && npm run build && cd ../..
go build ./...
git add -A
git commit -m "feat: modernise dashboard UI

- Restore repo/stack/container hierarchy in AppGrid
- Add force git sync button per repo (calls POST /api/sync/{repo})
- Make logo larger (36px, prominent in header)
- Add container detail tabs: Logs, Env vars, Info
- Show Docker env vars (sensitive values masked)
- Show container ports, uptime, image in Info tab
- Add log timestamps and error/warn colour coding
- Add 5s auto-refresh with error banner + Retry
- Fix HTML title to 'stackd'
- Add responsive mobile layout

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"
```

## Acceptance Criteria

- [ ] HTML title is "stackd"
- [ ] Logo visible at 36px in header
- [ ] Repo → Stack → Container hierarchy in sidebar
- [ ] Each repo shows: name, short SHA, last sync time, ↻ force sync button
- [ ] Force sync calls POST /api/sync/{repo} with spin animation
- [ ] Clicking a stack opens AppDetail
- [ ] AppDetail has container tabs, each with Logs / Env / Info sub-tabs
- [ ] Env tab shows env vars; sensitive keys show ••••••
- [ ] Info tab shows ID, image, status, uptime, ports
- [ ] Logs show timestamps + error/warn colours
- [ ] Status auto-refreshes every 5 seconds
- [ ] Error banner with Retry button when API unreachable
- [ ] npm run build passes
- [ ] go build ./... passes
