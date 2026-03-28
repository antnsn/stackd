import { useState, useEffect, useRef } from 'preact/hooks'
import { formatRelative, formatDateTime } from '../utils/time'
import './AppDetail.css'

const SORT_OPTIONS = [
  { key: 'name-asc',    label: 'Name A→Z' },
  { key: 'name-desc',   label: 'Name Z→A' },
  { key: 'uptime-desc', label: 'Uptime ↑' },
  { key: 'uptime-asc',  label: 'Uptime ↓' },
]

function sortContainers(containers, order) {
  const c = [...containers]
  switch (order) {
    case 'name-asc':
      return c.sort((a, b) => a.name.localeCompare(b.name))
    case 'name-desc':
      return c.sort((a, b) => b.name.localeCompare(a.name))
    case 'uptime-desc':
      // longest running first = smallest startedAt (earliest date)
      return c.sort((a, b) => new Date(a.startedAt || 0) - new Date(b.startedAt || 0))
    case 'uptime-asc':
      // shortest running first = largest startedAt (most recent date)
      return c.sort((a, b) => new Date(b.startedAt || 0) - new Date(a.startedAt || 0))
    default:
      return c
  }
}

function classifyLog(text) {
  const l = text.toLowerCase()
  if (l.includes('error') || l.includes('fatal') || l.includes('panic') || l.includes('critical')) return 'log-error'
  if (l.includes('warn')) return 'log-warn'
  if (l.includes('debug') || l.includes('trace')) return 'log-debug'
  return ''
}

// ── AppDetail ─────────────────────────────────────────

export function AppDetail({ stack, onClose }) {
  const [sortOrder, setSortOrder] = useState(
    () => localStorage.getItem('stackd:containerSort') || 'name-asc'
  )
  const sorted = sortContainers(stack.containers || [], sortOrder)
  const [selectedContainer, setSelectedContainer] = useState(sorted[0]?.name ?? null)

  useEffect(() => {
    const s = sortContainers(stack.containers || [], sortOrder)
    setSelectedContainer(s[0]?.name ?? null)
  }, [stack.name, stack.repoName])

  const handleSortChange = (e) => {
    const v = e.target.value
    setSortOrder(v)
    localStorage.setItem('stackd:containerSort', v)
  }

  const container = sorted.find(c => c.name === selectedContainer)

  return (
    <div class="app-detail">
      <div class="detail-header">
        <div class="detail-header__title">
          <span class="detail-repo">{stack.repoName}</span>
          <span class="detail-sep" aria-hidden="true">›</span>
          <h2 class="detail-stack-name">{stack.name}</h2>
          {stack.status && (
            <span class={`stack-badge stack-badge--${stack.status}`} aria-hidden="true">
              {stack.status}
            </span>
          )}
        </div>
        <button
          class="close-btn close-btn--desktop"
          onClick={onClose}
          aria-label="Close detail panel"
          title="Close"
        >
          ✕
        </button>
      </div>

      <div class="stack-meta-grid">
        {stack.lastApply && (
          <div class="meta-item">
            <span class="meta-label">Running since</span>
            <span class="meta-value">{formatRelative(stack.lastApply)}</span>
          </div>
        )}
        {stack.stackDir && (
          <div class="meta-item">
            <span class="meta-label">Directory</span>
            <span class="meta-value meta-value--mono">{stack.stackDir}</span>
          </div>
        )}
        {stack.lastError && (
          <div class="meta-item meta-item--error">
            <span class="meta-label">Error</span>
            <span class="meta-value">{stack.lastError}</span>
          </div>
        )}
      </div>

      {sorted.length > 0 ? (
        <>
          <div class="container-tabs-bar">
            <div class="container-tabs" role="tablist" aria-label="Containers">
              {sorted.map(c => (
                <button
                  key={c.name}
                  role="tab"
                  aria-selected={c.name === selectedContainer}
                  class={`container-tab ${c.name === selectedContainer ? 'container-tab--active' : ''}`}
                  onClick={() => setSelectedContainer(c.name)}
                >
                  <span class={`status-dot status-dot--${c.status}`} aria-hidden="true" />
                  {c.name}
                </button>
              ))}
            </div>
            <div class="sort-control">
              <label for="container-sort" class="sort-label">Sort</label>
              <select
                id="container-sort"
                class="sort-select"
                value={sortOrder}
                onChange={handleSortChange}
                aria-label="Sort containers"
              >
                {SORT_OPTIONS.map(o => (
                  <option key={o.key} value={o.key}>{o.label}</option>
                ))}
              </select>
            </div>
          </div>
          {container && <ContainerDetail container={container} />}
        </>
      ) : (
        <div class="empty-state-inline">
          No containers found for this stack.
        </div>
      )}

      {/* Mobile-only bottom close bar */}
      <div class="mobile-close-bar">
        <button class="mobile-close-btn" onClick={onClose}>
          Close
        </button>
      </div>
    </div>
  )
}

// ── ContainerDetail ───────────────────────────────────

function ContainerDetail({ container }) {
  const [tab, setTab] = useState('logs')

  return (
    <div class="container-detail">
      <div class="info-tabs" role="tablist" aria-label="Container detail sections">
        {[['logs', 'Logs'], ['env', 'Env'], ['info', 'Info']].map(([t, label]) => (
          <button
            key={t}
            role="tab"
            aria-selected={tab === t}
            class={`info-tab ${tab === t ? 'info-tab--active' : ''}`}
            onClick={() => setTab(t)}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === 'logs' && <LogStream key={container.name} containerName={container.name} />}
      {tab === 'env'  && <EnvVars envs={container.env} />}
      {tab === 'info' && <ContainerInfo container={container} />}
    </div>
  )
}

// ── LogStream ─────────────────────────────────────────

function LogStream({ containerName }) {
  const [logs, setLogs] = useState([])
  const [streamEnded, setStreamEnded] = useState(false)
  const esRef = useRef(null)
  const endRef = useRef(null)

  const startStream = () => {
    if (esRef.current) esRef.current.close()
    setLogs([])
    setStreamEnded(false)
    const es = new EventSource(`/api/logs/${containerName}`)
    es.onmessage = e => {
      setLogs(prev => [...prev, { text: e.data, time: new Date() }].slice(-200))
    }
    es.onerror = () => {
      es.close()
      setStreamEnded(true)
    }
    esRef.current = es
  }

  useEffect(() => {
    startStream()
    return () => esRef.current?.close()
  }, [containerName])

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [logs])

  return (
    <div class="logs-wrapper">
      {streamEnded && (
        <div class="stream-banner" role="status" aria-live="assertive">
          <span>Stream ended</span>
          <button class="stream-reconnect" onClick={startStream}>↻ Reconnect</button>
        </div>
      )}
      <div class="logs-content" role="log" aria-live="polite" aria-label={`Logs for ${containerName}`}>
        {!logs.length && !streamEnded && <div class="logs-empty">Waiting for logs…</div>}
        {logs.map((entry, i) => (
          <div key={i} class={`log-line ${classifyLog(entry.text)}`}>
            <span class="log-time" aria-hidden="true">{entry.time.toLocaleTimeString()}</span>
            <span class="log-text">{entry.text}</span>
          </div>
        ))}
        <div ref={endRef} aria-hidden="true" />
      </div>
    </div>
  )
}

// ── EnvVars ───────────────────────────────────────────

function EnvVars({ envs }) {
  if (!envs?.length) {
    return (
      <div class="env-list">
        <div class="empty-state-inline">No environment variables available.</div>
      </div>
    )
  }
  return (
    <div class="env-list" role="list" aria-label="Environment variables">
      {envs.map((e, i) => {
        const eq = e.indexOf('=')
        const key = eq >= 0 ? e.slice(0, eq) : e
        const val = eq >= 0 ? e.slice(eq + 1) : ''
        const isRedacted = val === '[redacted]'
        return (
          <div key={i} class="env-item" role="listitem">
            <span class="env-key">{key}</span>
            <span
              class={`env-value ${isRedacted ? 'env-value--redacted' : ''}`}
              aria-label={isRedacted ? `${key}: redacted` : undefined}
            >
              {isRedacted ? '••••••' : val}
            </span>
          </div>
        )
      })}
    </div>
  )
}

// ── ContainerInfo ─────────────────────────────────────

function ContainerInfo({ container }) {
  return (
    <div class="container-info">
      {container.id && (
        <div class="info-row">
          <span class="info-label">Container ID</span>
          <span class="info-value info-value--mono">{container.id.slice(0, 12)}</span>
        </div>
      )}
      <div class="info-row">
        <span class="info-label">Image</span>
        <span class="info-value info-value--mono">{container.image || '—'}</span>
      </div>
      <div class="info-row">
        <span class="info-label">Status</span>
        <span class="info-value">{container.status || '—'}</span>
      </div>
      {container.startedAt && container.startedAt !== '0001-01-01T00:00:00Z' && (
        <div class="info-row">
          <span class="info-label">Started</span>
          <span class="info-value">
            {formatRelative(container.startedAt)}
            <span aria-hidden="true"> · </span>
            <span style={{ color: 'var(--text-secondary)', fontSize: '12px' }}>
              {formatDateTime(container.startedAt)}
            </span>
          </span>
        </div>
      )}
      {container.ports?.length > 0 && (
        <div class="info-row">
          <span class="info-label">Ports</span>
          <span class="info-value info-value--mono">{container.ports.join(', ')}</span>
        </div>
      )}
    </div>
  )
}
