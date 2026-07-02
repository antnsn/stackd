import { useState, useEffect, useRef, useCallback, useLayoutEffect } from 'preact/hooks'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import { faKey } from '@fortawesome/free-solid-svg-icons'
import { formatRelative, formatDateTime } from '../utils/time'
import { apiFetch, withToken } from '../utils/auth.js'
import '@xterm/xterm/css/xterm.css'
import './AppDetail.css'




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
          <span class="detail-sep" aria-hidden="true">/</span>
          <h2 class="detail-stack-name">{stack.name}</h2>
          {stack.status && (
            <span
              class={`status-pip status-pip--${stack.status}`}
              aria-label={`Status: ${stack.status}`}
            />
          )}
        </div>
        <div class="detail-header__actions">
          {onApplyStack && (
            <button
              class={`ctrl-btn detail-sync-btn${isApplying ? ' ctrl-btn--loading' : ''}`}
              onClick={() => onApplyStack(stack.repoName, stack.name)}
              disabled={isApplying}
              aria-label={`Sync ${stack.name}`}
              title="Pull latest and re-apply this stack"
            >
              {isApplying ? <span class="ctrl-spinner" aria-hidden="true" /> : '↻'} Sync
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
      ) : stack.status === 'applying' ? (
        <div class="empty-state-inline">
          <span class="ctrl-spinner" aria-hidden="true" /> Applying…
        </div>
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
  const actionResetTimer = useRef(null)

  // Reset to smart default whenever the user switches to a different container
  useEffect(() => {
    setTab(getDefaultTab(container, lastError))
  }, [container.name])

  // Clear the pending action-feedback timer on unmount.
  useEffect(() => () => clearTimeout(actionResetTimer.current), [])

  const isRunning = container.status === 'running'
  const isStopped = container.status === 'stopped' || container.status === 'exited'

  const doAction = async (action) => {
    setActiveAction(action)
    setActionState('loading')
    try {
      const res = await apiFetch(`/api/containers/${encodeURIComponent(container.name)}/${action}`, { method: 'POST' })
      const body = await res.json()
      if (!res.ok || body.error) throw new Error(body.error || `HTTP ${res.status}`)
      setActionState({ ok: true })
      onRefresh?.()
    } catch (e) {
      setActionState({ err: e.message })
    } finally {
      clearTimeout(actionResetTimer.current)
      actionResetTimer.current = setTimeout(() => { setActionState(null); setActiveAction(null) }, 2000)
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

// ── LogStream — virtualized DOM list ──────────────────
// Renders log lines as recycled <div> rows so native browser
// text selection, Cmd/Ctrl-C copy, find-in-page, and screen
// readers all work. Only the rows currently in the viewport
// are mounted; the rest is a tall sizing spacer.

const LOG_LINE_H  = 19
const LOG_BUFFER  = 8        // extra rows above/below viewport
const LOG_MAX     = 50_000   // cap total retained lines (≈ 950k px scroll height)
const STICK_BOTTOM_PX = 6    // treat as "at bottom" if within this many px

function classifyLevel(t) {
  const l = t.toLowerCase()
  if (l.includes('error') || l.includes('fatal') || l.includes('panic') || l.includes('critical')) return 'error'
  if (l.includes('warn'))                                                                          return 'warn'
  if (l.includes('debug') || l.includes('trace'))                                                  return 'debug'
  return ''
}

function LogStream({ containerName }) {
  const wrapRef       = useRef(null)
  const linesRef      = useRef([])     // all retained {text, timeStr, level}
  const filteredRef   = useRef([])     // current filtered view (===linesRef when no filter)
  const filterTextRef = useRef('')     // latest filter text — read by flush() to avoid stale closures
  const esRef         = useRef(null)
  const bufferRef     = useRef([])
  const flushTimer    = useRef(null)
  const atBottomRef   = useRef(true)
  const copyTimerRef  = useRef(null)

  const [tick,         setTick]         = useState(0)         // bump to re-render after ref mutations
  const [totalCount,   setTotalCount]   = useState(0)
  const [visibleCount, setVisibleCount] = useState(0)
  const [streamEnded,  setStreamEnded]  = useState(false)
  const [filterText,   setFilterText]   = useState('')
  const [atBottom,     setAtBottom]     = useState(true)
  const [copied,       setCopied]       = useState(false)
  const [copyError,    setCopyError]    = useState('')
  const [scrollTop,    setScrollTop]    = useState(0)
  const [viewportH,    setViewportH]    = useState(0)

  const bump = useCallback(() => setTick(t => (t + 1) & 0xffff), [])

  const refilter = useCallback(() => {
    const f = filterTextRef.current.toLowerCase()
    filteredRef.current = f
      ? linesRef.current.filter(l => l.text.toLowerCase().includes(f))
      : linesRef.current
    setVisibleCount(filteredRef.current.length)
  }, [])

  // ── SSE stream ───────────────────────────────────────
  const startStream = useCallback(() => {
    esRef.current?.close()
    clearTimeout(flushTimer.current)
    bufferRef.current   = []
    linesRef.current    = []
    filteredRef.current = linesRef.current
    setTotalCount(0)
    setVisibleCount(0)
    setStreamEnded(false)
    atBottomRef.current = true
    setAtBottom(true)
    if (wrapRef.current) wrapRef.current.scrollTop = 0
    bump()

    const es = new EventSource(withToken(`/api/logs/${containerName}`))

    const flush = () => {
      if (bufferRef.current.length === 0) return
      const incoming = bufferRef.current.splice(0)
      const all = linesRef.current
      for (const ln of incoming) all.push(ln)
      // Apply soft cap so we never blow past browser scroll-height limits.
      if (all.length > LOG_MAX) all.splice(0, all.length - LOG_MAX)
      refilter()
      setTotalCount(all.length)
      bump()
    }

    es.onmessage = e => {
      const text    = e.data
      const timeStr = new Date().toLocaleTimeString()
      bufferRef.current.push({ text, timeStr, level: classifyLevel(text) })
      clearTimeout(flushTimer.current)
      flushTimer.current = setTimeout(flush, 16)
    }
    es.onerror = () => {
      es.close()
      flush()
      setStreamEnded(true)
    }

    esRef.current = es
  }, [containerName, refilter, bump])

  useEffect(() => {
    startStream()
    return () => {
      esRef.current?.close()
      clearTimeout(flushTimer.current)
      clearTimeout(copyTimerRef.current)
    }
  }, [containerName])

  // ── Filter: rebuild visible slice. On user-driven filter changes
  // (not the initial empty-filter mount) also reset scroll to top
  // and detach bottom-pinning so the user sees their match in place.
  useEffect(() => {
    const prev = filterTextRef.current
    filterTextRef.current = filterText
    refilter()
    if (prev !== filterText) {
      if (wrapRef.current) wrapRef.current.scrollTop = 0
      atBottomRef.current = false
      setAtBottom(false)
    }
    bump()
  }, [filterText, refilter, bump])

  // ── Viewport size tracking ───────────────────────────
  useEffect(() => {
    const wrap = wrapRef.current
    if (!wrap) return
    const ro = new ResizeObserver(([entry]) => {
      setViewportH(entry.contentRect.height)
    })
    ro.observe(wrap)
    setViewportH(wrap.clientHeight)
    return () => ro.disconnect()
  }, [])

  // ── Pin to bottom while user is at the bottom ────────
  useLayoutEffect(() => {
    if (!atBottomRef.current) return
    const wrap = wrapRef.current
    if (!wrap) return
    wrap.scrollTop = wrap.scrollHeight
  }, [tick, viewportH])

  // ── Scroll handler: update derived state + at-bottom flag.
  const handleScroll = useCallback(e => {
    const wrap = e.currentTarget
    const max  = wrap.scrollHeight - wrap.clientHeight
    const here = wrap.scrollTop
    setScrollTop(here)
    const nowAtBottom = here >= max - STICK_BOTTOM_PX
    if (atBottomRef.current !== nowAtBottom) {
      atBottomRef.current = nowAtBottom
      setAtBottom(nowAtBottom)
    }
  }, [])

  const scrollToBottom = useCallback(() => {
    const wrap = wrapRef.current
    if (!wrap) return
    wrap.scrollTop = wrap.scrollHeight
    atBottomRef.current = true
    setAtBottom(true)
  }, [])

  const copyAll = useCallback(async () => {
    const text = linesRef.current.map(l => `${l.timeStr}  ${l.text}`).join('\n')
    if (!text) return
    setCopyError('')

    const flash = (ok, msg = '') => {
      clearTimeout(copyTimerRef.current)
      if (ok) {
        setCopied(true)
        copyTimerRef.current = setTimeout(() => setCopied(false), 2000)
      } else {
        setCopyError(msg || 'Copy failed')
        copyTimerRef.current = setTimeout(() => setCopyError(''), 4000)
      }
    }

    if (window.isSecureContext && navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(text)
        flash(true)
        return
      } catch (e) {
        // Fall through to legacy path below
      }
    }

    try {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.setAttribute('readonly', '')
      ta.style.position = 'fixed'
      ta.style.top = '0'
      ta.style.left = '0'
      ta.style.opacity = '0'
      ta.style.pointerEvents = 'none'
      document.body.appendChild(ta)
      try {
        ta.focus()
        ta.select()
        ta.setSelectionRange(0, text.length)
        const ok = document.execCommand('copy')
        flash(ok, ok ? '' : 'Copy unavailable — select & ⌘C')
      } finally {
        ta.remove()
      }
    } catch (e) {
      flash(false, 'Copy unavailable — select & ⌘C')
    }
  }, [])

  // ── Compute visible window ───────────────────────────
  // Trade-off: only the visible slice is mounted, so browser Cmd-F won't reach
  // offscreen rows. This is intentional — at LOG_MAX (50k) lines a fully-mounted
  // DOM would jank. Use the in-app "Filter logs…" input above to search the full
  // buffer; native Cmd-C/drag-select still works for any visible text.
  const total  = filteredRef.current.length
  const totalH = total * LOG_LINE_H
  const first  = Math.max(0, Math.floor(scrollTop / LOG_LINE_H) - LOG_BUFFER)
  const last   = Math.min(total, Math.ceil((scrollTop + viewportH) / LOG_LINE_H) + LOG_BUFFER)
  const slice  = filteredRef.current.slice(first, last)
  const offset = first * LOG_LINE_H

  const showEmpty = totalCount === 0 && !streamEnded

  return (
    <div class="logs-wrapper">
      {streamEnded && (
        <div class="stream-banner" role="status" aria-live="assertive">
          <span>Stream ended</span>
          <button class="stream-reconnect" onClick={startStream}>
            <span aria-hidden="true">↻</span> Reconnect
          </button>
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
          <button class="log-filter-clear" onClick={() => setFilterText('')} aria-label="Clear filter">✕</button>
        )}
        {filterText ? (
          <span class="log-filter-count" aria-live="polite">{visibleCount}/{totalCount}</span>
        ) : totalCount > 0 ? (
          <span class="log-line-count">{totalCount.toLocaleString()} lines</span>
        ) : null}
        <button
          class={`log-copy-btn${copied ? ' log-copy-btn--done' : ''}${copyError ? ' log-copy-btn--error' : ''}`}
          onClick={copyAll}
          disabled={totalCount === 0}
          title={copyError || 'Copy all logs to clipboard'}
          aria-label="Copy all logs"
        >
          {copyError ? `⚠ ${copyError}` : copied ? '✓ Copied' : '⎘ Copy'}
        </button>
      </div>

      <div
        ref={wrapRef}
        class="logs-scroller"
        onScroll={handleScroll}
        aria-label={`Log output for ${containerName}`}
        role="log"
        aria-live="off"
      >
        <div class="logs-spacer" style={{ height: `${totalH}px` }}>
          <div class="logs-window" style={{ transform: `translateY(${offset}px)` }}>
            {slice.map((ln, i) => (
              <div
                key={first + i}
                class={`log-row${ln.level ? ' log-row--' + ln.level : ''}`}
              >
                <span class="log-row-time">{ln.timeStr}</span>
                <span class="log-row-text">{ln.text}</span>
              </div>
            ))}
          </div>
        </div>
        {showEmpty && (
          <div class="logs-canvas-empty" aria-live="polite">Waiting for logs…</div>
        )}
        {!atBottom && !showEmpty && (
          <button class="logs-jump-btn" onClick={scrollToBottom} aria-label="Jump to latest logs">
            ↓ Latest
          </button>
        )}
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
    apiFetch(`/api/stacks/${encodeURIComponent(repoName)}/${encodeURIComponent(stackName)}/compose`)
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

    // Guards against the async import resolving after unmount: without it the
    // .then() would open a WebSocket that never closes and call term.open()/
    // ro.observe() with a null ref (TypeError in an unhandled rejection).
    let cancelled = false
    let localWs = null
    let ro = null
    let dataDispose = null

    // Lazy-load xterm to avoid paying bundle cost until the Shell tab is opened
    Promise.all([
      import('@xterm/xterm'),
      import('@xterm/addon-fit'),
    ]).then(([{ Terminal }, { FitAddon }]) => {
      if (cancelled || !containerRef.current) return
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
      const ws = new WebSocket(withToken(`${proto}://${window.location.host}/api/exec/${containerID}`))
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws
      localWs = ws

      // All handlers below fire asynchronously and touch the xterm `term`.
      // After unmount/reconnect the cleanup sets `cancelled = true` and disposes
      // the terminal; a late ws.onclose/onerror/onmessage or observer callback
      // must NOT write to a disposed terminal (xterm throws). Guard every one.
      const alive = () => !cancelled && termRef.current === term && containerRef.current

      ws.onopen = () => {
        if (!alive()) return
        setConnected(true)
        ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
      }

      ws.onmessage = e => {
        if (!alive()) return
        if (e.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(e.data))
        } else {
          term.write(e.data)
        }
      }

      ws.onclose = () => {
        if (!alive()) return
        setConnected(false)
        term.write('\r\n\x1b[90m[session closed]\x1b[0m\r\n')
      }
      ws.onerror = () => {
        if (!alive()) return
        term.write('\r\n\x1b[31m[connection error]\x1b[0m\r\n')
      }

      dataDispose = term.onData(data => {
        if (!alive()) return
        if (ws.readyState === WebSocket.OPEN) ws.send(data)
      })

      ro = new ResizeObserver(() => {
        if (!alive()) return
        fitRef.current?.fit()
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
        }
      })
      ro.observe(containerRef.current)
    }).catch(() => { /* import failed or component torn down */ })

    return () => {
      cancelled = true
      ro?.disconnect()
      dataDispose?.dispose()
      localWs?.close()
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
