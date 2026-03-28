import { useState, useEffect, useRef } from 'preact/hooks'
import './AppDetail.css'

function formatRelative(dateStr) {
  if (!dateStr) return ''
  const diff = Date.now() - new Date(dateStr).getTime()
  const s = Math.floor(diff / 1000)
  if (s < 60) return `${s}s ago`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}

function formatDateTime(dateStr) {
  if (!dateStr) return ''
  return new Date(dateStr).toLocaleString()
}

function classifyLog(text) {
  const l = text.toLowerCase()
  if (l.includes('error') || l.includes('fatal') || l.includes('panic') || l.includes('critical')) return 'log-error'
  if (l.includes('warn')) return 'log-warn'
  if (l.includes('debug') || l.includes('trace')) return 'log-debug'
  return ''
}

export function AppDetail({ stack, onClose }) {
  const [selectedContainer, setSelectedContainer] = useState(
    stack.containers?.[0]?.name ?? null
  )

  useEffect(() => {
    setSelectedContainer(stack.containers?.[0]?.name ?? null)
  }, [stack.name, stack.repoName])

  const container = stack.containers?.find(c => c.name === selectedContainer)

  return (
    <div class="app-detail">
      <div class="detail-header">
        <div class="detail-header__title">
          <span class="detail-repo">{stack.repoName}</span>
          <span class="detail-sep">/</span>
          <h2 class="detail-stack-name">{stack.name}</h2>
          {stack.status && (
            <span class={`stack-badge stack-badge--${stack.status}`}>{stack.status}</span>
          )}
        </div>
        <button class="close-btn" onClick={onClose} title="Close">✕</button>
      </div>

      <div class="stack-meta-grid">
        {stack.lastApply && (
          <div class="meta-item">
            <span class="meta-label">Last deployed</span>
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

      {stack.containers?.length > 0 ? (
        <>
          <div class="container-tabs">
            {stack.containers.map(c => (
              <button
                key={c.name}
                class={`container-tab ${c.name === selectedContainer ? 'container-tab--active' : ''}`}
                onClick={() => setSelectedContainer(c.name)}
              >
                <span class={`status-dot status-dot--${c.status}`} />
                {c.name}
              </button>
            ))}
          </div>
          {container && <ContainerDetail container={container} />}
        </>
      ) : (
        <div class="empty-state" style={{ padding: '40px', textAlign: 'center', color: 'var(--text-secondary)' }}>
          No containers found for this stack.
        </div>
      )}
    </div>
  )
}

function ContainerDetail({ container }) {
  const [tab, setTab] = useState('logs')

  return (
    <div class="container-detail">
      <div class="info-tabs">
        {[['logs', '📋 Logs'], ['env', '⚙ Env'], ['info', 'ℹ Info']].map(([t, label]) => (
          <button
            key={t}
            class={`info-tab ${tab === t ? 'info-tab--active' : ''}`}
            onClick={() => setTab(t)}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === 'logs' && <LogStream key={container.name} containerName={container.name} />}
      {tab === 'env' && <EnvVars envs={container.env} />}
      {tab === 'info' && <ContainerInfo container={container} />}
    </div>
  )
}

function LogStream({ containerName }) {
  const [logs, setLogs] = useState([])
  const endRef = useRef(null)

  useEffect(() => {
    setLogs([])
    const es = new EventSource(`/api/logs/${containerName}`)
    es.onmessage = e => {
      setLogs(prev => [...prev, { text: e.data, time: new Date() }].slice(-200))
    }
    es.onerror = () => es.close()
    return () => es.close()
  }, [containerName])

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [logs])

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
  if (!envs?.length) {
    return (
      <div class="env-list">
        <div style={{ color: 'var(--text-secondary)', padding: '20px', textAlign: 'center' }}>
          No environment variables available.
        </div>
      </div>
    )
  }
  return (
    <div class="env-list">
      {envs.map((e, i) => {
        const eq = e.indexOf('=')
        const key = eq >= 0 ? e.slice(0, eq) : e
        const val = eq >= 0 ? e.slice(eq + 1) : ''
        const isRedacted = val === '[redacted]'
        return (
          <div key={i} class="env-item">
            <span class="env-key">{key}</span>
            <span class={`env-value ${isRedacted ? 'env-value--redacted' : ''}`}>
              {isRedacted ? '••••••' : val}
            </span>
          </div>
        )
      })}
    </div>
  )
}

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
          <span class="info-value">{formatRelative(container.startedAt)} · {formatDateTime(container.startedAt)}</span>
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
