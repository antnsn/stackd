import { useState, useEffect, useRef } from 'preact/hooks'
import { formatRelative, formatDateTime } from '../utils/time'
import './AppDetail.css'

function classifyLog(text) {
  const l = text.toLowerCase()
  if (l.includes('error') || l.includes('fatal') || l.includes('panic') || l.includes('critical')) return 'log-error'
  if (l.includes('warn')) return 'log-warn'
  if (l.includes('debug') || l.includes('trace')) return 'log-debug'
  return ''
}

// ── AppDetail ─────────────────────────────────────────

export function AppDetail({ stack, onClose, onRefresh }) {
  const containers = stack.containers || []
  const [selectedContainer, setSelectedContainer] = useState(containers[0]?.name ?? null)

  useEffect(() => {
    setSelectedContainer((stack.containers || [])[0]?.name ?? null)
  }, [stack.name, stack.repoName])

  const container = containers.find(c => c.name === selectedContainer)

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
            <pre class="meta-value meta-value--error-detail">{stack.lastError}</pre>
          </div>
        )}
      </div>

      {containers.length > 0 ? (
        <>
          <div class="container-tabs" role="tablist" aria-label="Containers">
            {containers.map(c => (
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
            <button
              role="tab"
              aria-selected={selectedContainer === '__compose__'}
              class={`container-tab container-tab--compose ${selectedContainer === '__compose__' ? 'container-tab--active' : ''}`}
              onClick={() => setSelectedContainer('__compose__')}
            >
              compose.yml
            </button>
          </div>
          {selectedContainer === '__compose__'
            ? <ComposeViewer repoName={stack.repoName} stackName={stack.name} />
            : container && <ContainerDetail container={container} onRefresh={onRefresh} />
          }
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

function ContainerDetail({ container, onRefresh }) {
  const [tab, setTab] = useState('logs')
  const [actionState, setActionState] = useState(null) // 'loading'|{ok}|{err}
  const [activeAction, setActiveAction] = useState(null)

  const isRunning = container.status === 'running'
  const isStopped = container.status === 'stopped' || container.status === 'exited'

  const doAction = async (action) => {
    setActiveAction(action)
    setActionState('loading')
    try {
      const res = await fetch(`/api/containers/${encodeURIComponent(container.name)}/${action}`, { method: 'POST' })
      const body = await res.json()
      if (!res.ok || body.error) throw new Error(body.error || `HTTP ${res.status}`)
      setActionState({ ok: true })
      onRefresh?.()
    } catch (e) {
      setActionState({ err: e.message })
    } finally {
      setTimeout(() => { setActionState(null); setActiveAction(null) }, 2000)
    }
  }

  const loading = actionState === 'loading'

  return (
    <div class="container-detail">
      <div class="container-actions">
        <div class="container-actions__btns">
          <button
            class={`ctrl-btn ctrl-btn--start ${activeAction === 'start' && loading ? 'ctrl-btn--loading' : ''}`}
            onClick={() => doAction('start')}
            disabled={loading || isRunning}
            aria-label="Start container"
            touch-action="manipulation"
          >
            {activeAction === 'start' && loading ? <span class="ctrl-spinner" aria-hidden="true" /> : '▶'} Start
          </button>
          <button
            class={`ctrl-btn ctrl-btn--stop ${activeAction === 'stop' && loading ? 'ctrl-btn--loading' : ''}`}
            onClick={() => doAction('stop')}
            disabled={loading || isStopped}
            aria-label="Stop container"
            touch-action="manipulation"
          >
            {activeAction === 'stop' && loading ? <span class="ctrl-spinner" aria-hidden="true" /> : '■'} Stop
          </button>
          <button
            class={`ctrl-btn ctrl-btn--restart ${activeAction === 'restart' && loading ? 'ctrl-btn--loading' : ''}`}
            onClick={() => doAction('restart')}
            disabled={loading}
            aria-label="Restart container"
            touch-action="manipulation"
          >
            {activeAction === 'restart' && loading ? <span class="ctrl-spinner" aria-hidden="true" /> : '↻'} Restart
          </button>
        </div>
        {actionState?.ok && <span class="ctrl-feedback ctrl-feedback--ok">Done ✓</span>}
        {actionState?.err && <span class="ctrl-feedback ctrl-feedback--err">{actionState.err}</span>}
      </div>
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
          <button class="stream-reconnect" onClick={startStream}><span aria-hidden="true">↻</span> Reconnect</button>
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

// ── ComposeViewer ─────────────────────────────────────

function ComposeViewer({ repoName, stackName }) {
  const [content, setContent] = useState(null)
  const [error, setError] = useState(null)

  useEffect(() => {
    setContent(null)
    setError(null)
    fetch(`/api/stacks/${encodeURIComponent(repoName)}/${encodeURIComponent(stackName)}/compose`)
      .then(r => r.ok ? r.text() : Promise.reject(r.statusText))
      .then(setContent)
      .catch(e => setError(String(e)))
  }, [repoName, stackName])

  if (error) return <div class="compose-error">Could not load compose file: {error}</div>
  if (!content) return <div class="compose-loading">Loading…</div>

  return (
    <pre class="compose-viewer">{content}</pre>
  )
}
