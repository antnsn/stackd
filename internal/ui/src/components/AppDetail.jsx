import { useState, useEffect, useRef } from 'preact/hooks'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faKey } from '@fortawesome/free-solid-svg-icons'
import { formatRelative, formatDateTime } from '../utils/time'
import '@xterm/xterm/css/xterm.css'
import './AppDetail.css'

function classifyLog(text) {
  const l = text.toLowerCase()
  if (l.includes('error') || l.includes('fatal') || l.includes('panic') || l.includes('critical')) return 'log-error'
  if (l.includes('warn')) return 'log-warn'
  if (l.includes('debug') || l.includes('trace')) return 'log-debug'
  return ''
}

// ── AppDetail ─────────────────────────────────────────

export function AppDetail({ stack, onClose, onRefresh, onForceSync, onApplyStack, isSyncing, isApplying }) {
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
        <div class="detail-header__actions">
          {onApplyStack && (
            <button
              class={`ctrl-btn detail-sync-btn${isApplying ? ' ctrl-btn--loading' : ''}`}
              onClick={() => onApplyStack(stack.repoName, stack.name)}
              disabled={isApplying}
              aria-label={`Apply ${stack.name}`}
              title="Re-apply this stack (docker compose up -d)"
            >
              {isApplying ? <span class="ctrl-spinner" aria-hidden="true" /> : '↻'} Apply
            </button>
          )}
          <button
            class="close-btn close-btn--desktop"
            onClick={onClose}
            aria-label="Close detail panel"
            title="Close"
          >
            ✕
          </button>
        </div>
      </div>

      <div class="stack-meta-grid">
        {stack.lastApply && (
          <div class="meta-item">
            <span class="meta-label">Running since</span>
            <span class="meta-value">{formatRelative(stack.lastApply)}</span>
          </div>
        )}
        {stack.infisicalMode ? (
          <div class="meta-item">
            <span class="meta-label">Secrets</span>
            <span class="meta-value meta-value--infisical">
              <FontAwesomeIcon icon={faKey} />
              Infisical
              <span class="meta-infisical-mode">
                {stack.infisicalMode === 'per-stack' ? 'per-stack' : 'global'}
              </span>
            </span>
          </div>
        ) : (
          <div class="meta-item">
            <span class="meta-label">Secrets</span>
            <span class="meta-value meta-value--muted">none</span>
          </div>
        )}
        {stack.stackDir && (
          <div class="meta-item">
            <span class="meta-label">Directory</span>
            <span class="meta-value meta-value--mono">{stack.stackDir}</span>
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
                title={c.name}
              >
                <span class={`status-dot status-dot--${c.status}`} aria-hidden="true" />
                {c.name}
              </button>
            ))}
          </div>
          {container && (
            <ContainerDetail
              container={container}
              onRefresh={onRefresh}
              repoName={stack.repoName}
              stackName={stack.name}
              lastOutput={stack.lastOutput}
              lastError={stack.lastError}
            />
          )}
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

const getDefaultTab = (container, lastError) => {
  if (lastError) return 'info'
  if (container && (container.status === 'stopped' || container.status === 'exited')) return 'info'
  return 'logs'
}

function ContainerDetail({ container, onRefresh, repoName, stackName, lastOutput, lastError }) {
  const [tab, setTab] = useState(() => getDefaultTab(container, lastError))
  const [actionState, setActionState] = useState(null)
  const [activeAction, setActiveAction] = useState(null)
  const [pendingStop, setPendingStop] = useState(false)
  const [stopTimer, setStopTimer] = useState(null)

  // Reset to smart default whenever the user switches to a different container
  useEffect(() => {
    setTab(getDefaultTab(container, lastError))
  }, [container.name])

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

  const handleStopClick = () => {
    if (pendingStop) {
      clearTimeout(stopTimer)
      setPendingStop(false)
      setStopTimer(null)
      doAction('stop')
    } else {
      setPendingStop(true)
      const t = setTimeout(() => {
        setPendingStop(false)
        setStopTimer(null)
      }, 3000)
      setStopTimer(t)
    }
  }

  useEffect(() => {
    return () => { if (stopTimer) clearTimeout(stopTimer) }
  }, [stopTimer])

  const loading = actionState === 'loading'

  return (
    <div class="container-detail">
      <div class="container-actions">
        <div class="container-actions__btns">
          <button
            class={`ctrl-btn ctrl-btn--start ${activeAction === 'start' && loading ? 'ctrl-btn--loading' : ''}`}
            onClick={() => doAction('start')}
            disabled={loading || isRunning}
            aria-label={`Start ${container.name}`}
          >
            {activeAction === 'start' && loading ? <span class="ctrl-spinner" aria-hidden="true" /> : '▶'} Start
          </button>
          <button
            class={`ctrl-btn ctrl-btn--stop ${pendingStop ? 'ctrl-btn--confirm' : ''} ${activeAction === 'stop' && loading ? 'ctrl-btn--loading' : ''}`}
            onClick={handleStopClick}
            disabled={loading || isStopped}
            aria-label={`Stop ${container.name}`}
          >
            {activeAction === 'stop' && loading ? <span class="ctrl-spinner" aria-hidden="true" /> : (pendingStop ? '?' : '■')} {pendingStop ? 'Confirm stop' : 'Stop'}
          </button>
          <button
            class={`ctrl-btn ctrl-btn--restart ${activeAction === 'restart' && loading ? 'ctrl-btn--loading' : ''}`}
            onClick={() => doAction('restart')}
            disabled={loading}
            aria-label={`Restart ${container.name}`}
          >
            {activeAction === 'restart' && loading ? <span class="ctrl-spinner" aria-hidden="true" /> : '↻'} Restart
          </button>
        </div>
        {actionState?.ok && <span class="ctrl-feedback ctrl-feedback--ok">Done ✓</span>}
        {actionState?.err && <span class="ctrl-feedback ctrl-feedback--err">{actionState.err}</span>}
      </div>
      <div class="info-tabs" role="tablist" aria-label="Container detail sections">
        {[['info', lastError ? <>Info <span aria-hidden="true">⚠</span></> : 'Info'], ['logs', 'Logs'], ['env', 'Env'], ['shell', 'Shell']].map(([t, label]) => (
          <button
            key={t}
            role="tab"
            aria-selected={tab === t}
            class={`info-tab ${tab === t ? 'info-tab--active' : ''} ${t === 'info' && lastError ? 'info-tab--error' : ''}`}
            onClick={() => setTab(t)}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === 'logs'    && <div class="tab-panel" key="logs"><LogStream key={container.name} containerName={container.name} /></div>}
      {tab === 'env'     && <div class="tab-panel" key="env"><EnvVars envs={container.env} /></div>}
      {tab === 'info'    && <div class="tab-panel" key="info"><ContainerInfo container={container} lastOutput={lastOutput} lastError={lastError} repoName={repoName} stackName={stackName} /></div>}
      {tab === 'shell'   && <div class="tab-panel tab-panel--shell" key="shell"><TerminalPanel containerID={container.id} /></div>}
    </div>
  )
}

// ── LogStream ─────────────────────────────────────────

function LogStream({ containerName }) {
  const [logs, setLogs] = useState([])
  const [streamEnded, setStreamEnded] = useState(false)
  const [filterText, setFilterText] = useState('')
  const esRef = useRef(null)
  const endRef = useRef(null)
  const mountedRef = useRef(false)

  useEffect(() => { mountedRef.current = true }, [])

  const startStream = () => {
    if (esRef.current) esRef.current.close()
    setLogs([])
    setStreamEnded(false)
    const es = new EventSource(`/api/logs/${containerName}`)
    es.onmessage = e => {
      setLogs(prev => [...prev, { id: Date.now() + Math.random(), text: e.data, time: new Date(), isNew: mountedRef.current }].slice(-200))
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

  const visibleLogs = filterText
    ? logs.filter(e => e.text.toLowerCase().includes(filterText.toLowerCase()))
    : logs

  return (
    <div class="logs-wrapper">
      {streamEnded && (
        <div class="stream-banner" role="status" aria-live="assertive">
          <span>Stream ended</span>
          <button class="stream-reconnect" onClick={startStream}><span aria-hidden="true">↻</span> Reconnect</button>
        </div>
      )}
      <div class="log-filter-bar">
        <input
          type="text"
          class="log-filter-input"
          placeholder="Filter logs…"
          value={filterText}
          onInput={e => setFilterText(e.target.value)}
          aria-label="Filter log lines"
        />
        {filterText && (
          <button class="log-filter-clear" onClick={() => setFilterText('')} aria-label="Clear filter">
            ✕
          </button>
        )}
        {filterText && (
          <span class="log-filter-count" aria-live="polite">
            {visibleLogs.length}/{logs.length}
          </span>
        )}
      </div>
      <div class="logs-content" role="log" aria-live="polite" aria-label={`Logs for ${containerName}`}>
        {!logs.length && !streamEnded && <div class="logs-empty">Waiting for logs…</div>}
        {visibleLogs.map((entry) => (
          <div key={entry.id} class={`log-line ${classifyLog(entry.text)} ${entry.isNew ? 'log-line--new' : ''}`}>
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
          <div key={key} class="env-item" role="listitem">
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

function ContainerInfo({ container, lastOutput, lastError, repoName, stackName }) {
  const infoRef = useRef(null)

  useEffect(() => {
    if (lastError && infoRef.current) {
      infoRef.current.scrollTop = 0
    }
  }, [lastError])

  const metaRows = (
    <>
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
          <span class="info-value" title={formatDateTime(container.startedAt)}>
            {formatRelative(container.startedAt)}
          </span>
        </div>
      )}
      {container.ports?.length > 0 && (
        <div class="info-row">
          <span class="info-label">Ports</span>
          <span class="info-value info-value--mono">{container.ports.join(', ')}</span>
        </div>
      )}
    </>
  )

  return (
    <div class="container-info" ref={infoRef}>
      {lastError ? (
        <>
          <div class="info-section-divider">Last compose run</div>
          <pre class="info-output info-output--error">{lastOutput}</pre>
          <div class="info-section-divider" style={{ marginTop: '8px' }} />
          {metaRows}
        </>
      ) : (
        <>
          {metaRows}
          {lastOutput && (
            <>
              <div class="info-section-divider">Last compose run</div>
              <pre class="info-output info-output--ok">{lastOutput}</pre>
            </>
          )}
        </>
      )}
      <ComposeViewer repoName={repoName} stackName={stackName} lastError={lastError} />
    </div>
  )
}

// ── ComposeViewer ─────────────────────────────────────

function ComposeViewer({ repoName, stackName, lastError }) {
  const [content, setContent] = useState(null)
  const [error, setError] = useState(null)
  const [expanded, setExpanded] = useState(!lastError)

  useEffect(() => {
    setContent(null)
    setError(null)
    fetch(`/api/stacks/${encodeURIComponent(repoName)}/${encodeURIComponent(stackName)}/compose`)
      .then(r => r.ok ? r.text() : Promise.reject(r.statusText))
      .then(setContent)
      .catch(e => setError(String(e)))
  }, [repoName, stackName])

  return (
    <>
      <div
        class="info-section-divider"
        role="button"
        tabIndex={0}
        onClick={() => setExpanded(e => !e)}
        onKeyDown={e => (e.key === 'Enter' || e.key === ' ') && setExpanded(ex => !ex)}
        style="cursor:pointer;display:flex;justify-content:space-between;align-items:center;user-select:none"
        aria-expanded={expanded}
      >
        compose.yml <span aria-hidden="true" class={`compose-chevron${expanded ? '' : ' compose-chevron--closed'}`}>▾</span>
      </div>
      <div class={`compose-body${expanded ? ' compose-body--open' : ''}`}>
        <div class="compose-body-inner">
          {error ? <div class="compose-error">Could not load compose file: {error}</div>
          : !content ? <div class="compose-loading">Loading…</div>
          : <pre class="compose-viewer">{content}</pre>}
        </div>
      </div>
    </>
  )
}

// ── TerminalPanel ─────────────────────────────────────

function TerminalPanel({ containerID }) {
  const containerRef = useRef(null)
  const termRef      = useRef(null)
  const wsRef        = useRef(null)
  const fitRef       = useRef(null)
  const [connected, setConnected] = useState(false)
  const [key, setKey] = useState(0) // bump to force reconnect

  useEffect(() => {
    if (!containerRef.current) return

    let cleanup = () => {}

    // Lazy-load xterm to avoid paying bundle cost until the Shell tab is opened
    Promise.all([
      import('@xterm/xterm'),
      import('@xterm/addon-fit'),
    ]).then(([{ Terminal }, { FitAddon }]) => {
      // Reuse existing terminal instance across reconnects, create on first open
      let term = termRef.current
      if (!term) {
        term = new Terminal({
          cursorBlink: true,
          fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', monospace",
          fontSize: 13,
          theme: {
            background:  '#0d1117',
            foreground:  '#e6edf3',
            cursor:      '#6c63ff',
            black:       '#0d1117',
            brightBlack: '#484f58',
            red:         '#f85149',
            green:       '#3fb950',
            yellow:      '#d29922',
            blue:        '#58a6ff',
            magenta:     '#bc8cff',
            cyan:        '#39c5cf',
            white:       '#b1bac4',
            brightWhite: '#e6edf3',
          },
        })
        const fit = new FitAddon()
        term.loadAddon(fit)
        term.open(containerRef.current)
        fit.fit()
        termRef.current = term
        fitRef.current  = fit
      } else {
        fitRef.current?.fit()
      }

      const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
      const ws = new WebSocket(`${proto}://${window.location.host}/api/exec/${containerID}`)
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws

      ws.onopen = () => {
        setConnected(true)
        ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
      }

      ws.onmessage = e => {
        if (e.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(e.data))
        } else {
          term.write(e.data)
        }
      }

      ws.onclose = () => {
        setConnected(false)
        term.write('\r\n\x1b[90m[session closed]\x1b[0m\r\n')
      }
      ws.onerror = () => term.write('\r\n\x1b[31m[connection error]\x1b[0m\r\n')

      const dataDispose = term.onData(data => {
        if (ws.readyState === WebSocket.OPEN) ws.send(data)
      })

      const ro = new ResizeObserver(() => {
        fitRef.current?.fit()
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
        }
      })
      ro.observe(containerRef.current)

      cleanup = () => {
        ro.disconnect()
        dataDispose.dispose()
        ws.close()
      }
    })

    return () => {
      cleanup()
    }
  }, [containerID, key])

  // Full dispose when component unmounts
  useEffect(() => {
    return () => {
      wsRef.current?.close()
      termRef.current?.dispose()
      termRef.current = null
    }
  }, [])

  return (
    <div class="terminal-wrapper">
      <div class="terminal-toolbar">
        <span class={`terminal-status ${connected ? 'terminal-status--connected' : 'terminal-status--disconnected'}`}>
          {connected ? 'connected' : 'disconnected'}
        </span>
        <button
          class="terminal-reconnect-btn"
          onClick={() => { wsRef.current?.close(); setKey(k => k + 1) }}
          title="Reconnect"
        >
          ↺ reconnect
        </button>
      </div>
      <div ref={containerRef} class="terminal-panel" />
    </div>
  )
}
