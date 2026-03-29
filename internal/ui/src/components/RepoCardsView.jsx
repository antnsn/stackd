import { useState, useMemo } from 'preact/hooks'
import './RepoCardsView.css'

const SORT_OPTIONS = [
  { value: 'name-asc',    label: 'Name A–Z' },
  { value: 'name-desc',   label: 'Name Z–A' },
  { value: 'status',      label: 'Status (errors first)' },
  { value: 'containers',  label: 'Containers (most first)' },
]

const STATUS_ORDER = { error: 0, stopped: 1, exited: 1, applying: 2, running: 3, unknown: 4 }

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
        <span class="repo-cards-title">{repo.name}</span>

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

        <div class="repo-sort">
          <label class="sort-label" for="cards-sort">Sort</label>
          <select
            id="cards-sort"
            class="sort-select"
            value={sort}
            onChange={e => setSort(e.target.value)}
          >
            {SORT_OPTIONS.map(o => (
              <option key={o.value} value={o.value}>{o.label}</option>
            ))}
          </select>
        </div>

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

function StackCard({ stack, onSelect, index }) {
  const status     = stack.status || 'unknown'
  const containers = stack.containers || []
  const running    = containers.filter(c => c.status === 'running').length
  const total      = containers.length
  const hasError   = !!stack.lastError

  return (
    <button
      class={`stack-card-main stack-card-main--${status}`}
      onClick={onSelect}
      style={{ '--card-i': Math.min(index, 8) }}
    >
      <div class="stack-card-main__header">
        <span class="stack-card-main__dot" aria-hidden="true" />
        <span class="stack-card-main__name">{stack.name}</span>
      </div>
      <div class="stack-card-main__meta">
        <span class="stack-card-main__badge">{status}</span>
        {total > 0 && (
          <span class="stack-card-main__ctrs">{running}/{total} ctr{total !== 1 ? 's' : ''}</span>
        )}
      </div>
      {hasError && (
        <p class="stack-card-main__error" title={stack.lastError}>
          {stack.lastError.slice(0, 90)}{stack.lastError.length > 90 ? '…' : ''}
        </p>
      )}
    </button>
  )
}
