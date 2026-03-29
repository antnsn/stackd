import { useState, useMemo, useRef, useEffect } from 'preact/hooks'
import { formatRelative } from '../utils/time.js'
import './RepoCardsView.css'

const KNOWN_REGISTRIES = [
  'lscr.io/linuxserver/',
  'ghcr.io/',
  'docker.io/library/',
  'registry-1.docker.io/library/',
  'index.docker.io/library/',
]

function shortImage(image) {
  if (!image) return ''
  for (const prefix of KNOWN_REGISTRIES) {
    if (image.startsWith(prefix)) return image.slice(prefix.length)
  }
  return image
}

function ctrDotMod(container) {
  const { status, health } = container
  if (health === 'unhealthy')                      return 'degraded'
  if (health === 'starting')                       return 'applying'
  if (status === 'running')                        return 'running'
  if (status === 'exited' || status === 'stopped') return 'stopped'
  return 'unknown'
}

const SORT_OPTIONS = [
  { value: 'name-asc',    label: 'Name A–Z' },
  { value: 'name-desc',   label: 'Name Z–A' },
  { value: 'status',      label: 'Status (errors first)' },
  { value: 'containers',  label: 'Containers (most first)' },
]

const STATUS_ORDER = { error: 0, stopped: 1, exited: 1, applying: 2, ok: 3, running: 3, unknown: 4 }

export function RepoCardsView({ repo, onSelectStack, isSyncing, onSync, syncStatus }) {
  const [search, setSearch]   = useState('')
  const [sort, setSort]       = useState('name-asc')

  const stacks = repo?.stacks || []

  const filtered = useMemo(() => {
    let list = stacks.slice()
    const q = search.trim().toLowerCase()
    if (q) list = list.filter(s => s.name.toLowerCase().includes(q))
    switch (sort) {
      case 'name-desc':
        list.sort((a, b) => b.name.localeCompare(a.name)); break
      case 'status':
        list.sort((a, b) => (STATUS_ORDER[a.status] ?? 9) - (STATUS_ORDER[b.status] ?? 9)); break
      case 'containers':
        list.sort((a, b) => (b.containers?.length ?? 0) - (a.containers?.length ?? 0)); break
      default:
        list.sort((a, b) => a.name.localeCompare(b.name))
    }
    return list
  }, [stacks, search, sort])

  if (!repo) return null

  return (
    <div class="repo-cards-view">
      <div class="repo-cards-topbar">
        <h2 class="repo-cards-title">{repo.name}</h2>

        <div class="repo-search">
          <span class="repo-search__icon" aria-hidden="true">⌕</span>
          <input
            class="repo-search__input"
            type="search"
            placeholder="Search stacks…"
            value={search}
            onInput={e => setSearch(e.target.value)}
            aria-label="Search stacks"
          />
          {search && (
            <button class="repo-search__clear" onClick={() => setSearch('')} aria-label="Clear search">×</button>
          )}
        </div>

        <SortDropdown value={sort} onChange={setSort} options={SORT_OPTIONS} />

        {search && (
          <span class="repo-filter-count">{filtered.length} of {stacks.length}</span>
        )}

        <div class="repo-cards-topbar__actions">
          {syncStatus?.message && (
            <span class={`repo-sync-msg repo-sync-msg--${syncStatus.state}`}>
              {syncStatus.message}
            </span>
          )}
          <button
            class={`repo-sync-btn${isSyncing ? ' repo-sync-btn--spinning' : ''}`}
            onClick={() => onSync(repo.name)}
            disabled={isSyncing}
            aria-label={`Sync ${repo.name}`}
          >
            <span aria-hidden="true" class="repo-sync-btn__icon">↻</span>
            {isSyncing ? 'Syncing…' : 'Sync'}
          </button>
        </div>
      </div>

      {filtered.length === 0 ? (
        <div class="repo-cards-empty">
          {search ? `No stacks match "${search}"` : 'No stacks in this repo'}
        </div>
      ) : (
        <div class="repo-cards-grid">
          {filtered.map((stack, i) => (
            <StackCard
              key={stack.name}
              stack={stack}
              onSelect={() => onSelectStack({ ...stack, repoName: repo.name })}
              index={i}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function SortDropdown({ value, onChange, options }) {
  const [open, setOpen] = useState(false)
  const ref = useRef(null)
  const current = options.find(o => o.value === value)

  useEffect(() => {
    const handler = e => { if (ref.current && !ref.current.contains(e.target)) setOpen(false) }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [])

  const handleKey = e => {
    if (e.key === 'Escape') setOpen(false)
    if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setOpen(o => !o) }
  }

  return (
    <div class={`sort-dropdown${open ? ' sort-dropdown--open' : ''}`} ref={ref}>
      <button
        class="sort-dropdown__trigger"
        onClick={() => setOpen(o => !o)}
        onKeyDown={handleKey}
        aria-haspopup="listbox"
        aria-expanded={open}
      >
        {current?.label}
        <span class="sort-dropdown__chevron" aria-hidden="true">▾</span>
      </button>
      {open && (
        <ul class="sort-dropdown__menu" role="listbox">
          {options.map(o => (
            <li
              key={o.value}
              class={`sort-dropdown__item${o.value === value ? ' sort-dropdown__item--active' : ''}`}
              role="option"
              aria-selected={o.value === value}
              onClick={() => { onChange(o.value); setOpen(false) }}
            >
              {o.label}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

const MAX_CTR_ROWS = 3

function StackCard({ stack, onSelect, index }) {
  const rawStatus  = stack.status || 'unknown'
  // API returns 'ok' for healthy stacks — normalise to CSS modifier names
  const STATUS_MAP  = { ok: 'running' }
  const status      = STATUS_MAP[rawStatus] || rawStatus
  const containers  = stack.containers || []
  const visible     = containers.slice(0, MAX_CTR_ROWS)
  const overflow    = containers.length - MAX_CTR_ROWS
  const hasError    = !!stack.lastError

  return (
    <button
      class={`stack-card-main stack-card-main--${status}`}
      onClick={onSelect}
      style={{ '--card-i': Math.min(index, 8) }}
    >
      <div class="stack-card-main__header">
        <span class="stack-card-main__dot" aria-hidden="true" />
        <span class="stack-card-main__name">{stack.name}</span>
        <span class="stack-card-main__badge">{status}</span>
      </div>

      {containers.length > 0 && (
        <div class="stack-card-main__containers">
          {visible.map(c => (
            <ContainerRow key={c.id || c.name} container={c} />
          ))}
          {overflow > 0 && (
            <div class="stack-card-main__overflow">+{overflow} more</div>
          )}
        </div>
      )}

      {hasError && (
        <p class="stack-card-main__error" title={stack.lastError}>
          {stack.lastError.slice(0, 80)}{stack.lastError.length > 80 ? '…' : ''}
        </p>
      )}
    </button>
  )
}

function ContainerRow({ container }) {
  const dotMod = ctrDotMod(container)
  const image  = shortImage(container.image)
  const age    = container.startedAt ? formatRelative(container.startedAt) : '—'

  return (
    <div class={`ctr-row ctr-row--${dotMod}`}>
      <span class="ctr-row__dot" aria-label={dotMod} title={dotMod} />
      <span class="ctr-row__name" title={container.name}>{container.name}</span>
      <span class="ctr-row__image" title={container.image}>{image}</span>
      <span class="ctr-row__age">{age}</span>
    </div>
  )
}
