import { useState } from 'preact/hooks'
import { formatRelative } from '../utils/time'
import './AppGrid.css'

const SORT_OPTIONS = [
  { key: 'name-asc',     label: 'Name A→Z' },
  { key: 'name-desc',    label: 'Name Z→A' },
  { key: 'uptime-long',  label: 'Running longest' },
  { key: 'uptime-short', label: 'Started recently' },
]

function sortStacks(stacks, order) {
  const s = [...stacks]
  switch (order) {
    case 'name-asc':
      return s.sort((a, b) => a.name.localeCompare(b.name))
    case 'name-desc':
      return s.sort((a, b) => b.name.localeCompare(a.name))
    case 'uptime-long':
      // oldest lastApply first = longest running
      return s.sort((a, b) => new Date(a.lastApply || 0) - new Date(b.lastApply || 0))
    case 'uptime-short':
      // newest lastApply first = started most recently
      return s.sort((a, b) => new Date(b.lastApply || 0) - new Date(a.lastApply || 0))
    default:
      return s
  }
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

export function AppGrid({ repos, selectedStack, syncingRepos, syncStatus, onSelectStack, onForceSync }) {
  const [sortOrder, setSortOrder] = useState(
    () => localStorage.getItem('stackd:stackSort') || 'name-asc'
  )

  const handleSortChange = (e) => {
    const v = e.target.value
    setSortOrder(v)
    localStorage.setItem('stackd:stackSort', v)
  }

  if (!repos.length) {
    return (
      <div class="empty-state">
        <p>No repositories found</p>
        <p class="empty-state__hint">Mount git repos into REPOS_DIR to get started.</p>
      </div>
    )
  }

  return (
    <div class="app-grid">
      <div class="grid-sort-bar">
        <label for="stack-sort" class="sort-label">Sort</label>
        <select
          id="stack-sort"
          class="sort-select"
          value={sortOrder}
          onChange={handleSortChange}
          aria-label="Sort stacks"
        >
          {SORT_OPTIONS.map(o => (
            <option key={o.key} value={o.key}>{o.label}</option>
          ))}
        </select>
      </div>
      {repos.map(repo => (
        <RepoGroup
          key={repo.name}
          repo={repo}
          sortOrder={sortOrder}
          selectedStack={selectedStack}
          isSyncing={syncingRepos.has(repo.name)}
          repoSyncStatus={syncStatus?.[repo.name]}
          onSelectStack={onSelectStack}
          onForceSync={onForceSync}
        />
      ))}
    </div>
  )
}

function RepoGroup({ repo, sortOrder, selectedStack, isSyncing, repoSyncStatus, onSelectStack, onForceSync }) {
  const statusClass = repoSyncStatus?.state === 'success' ? 'repo-header--flash-ok'
    : repoSyncStatus?.state === 'rateLimit' ? 'repo-header--flash-warn'
    : repoSyncStatus?.state === 'error' ? 'repo-header--flash-err'
    : ''

  const sortedStacks = sortStacks(repo.stacks || [], sortOrder)

  return (
    <div class="repo-group">
      <div class={`repo-header ${statusClass}`}>
        <div class="repo-header__left">
          <span
            class={`repo-status-dot repo-status-dot--${repo.status}`}
            aria-hidden="true"
          />
          <span class="repo-name">{repo.name}</span>
          {repo.lastSha && (
            <span class="repo-sha" title={repo.lastSha}>{repo.lastSha.slice(0, 7)}</span>
          )}
        </div>
        <div class="repo-header__right">
          {repoSyncStatus?.message ? (
            <span class={`sync-feedback sync-feedback--${repoSyncStatus.state}`} role="status">
              {repoSyncStatus.message}
            </span>
          ) : repo.lastSync ? (
            <span class="repo-last-sync">{formatRelative(repo.lastSync)}</span>
          ) : null}
          <button
            class={`sync-btn ${isSyncing ? 'sync-btn--spinning' : ''}`}
            onClick={() => onForceSync(repo.name)}
            aria-label={isSyncing ? `Syncing ${repo.name}…` : `Force sync ${repo.name}`}
            title={isSyncing ? 'Syncing…' : 'Force sync'}
            disabled={isSyncing}
          >
            ↻
          </button>
        </div>
      </div>

      {repo.lastError && (
        <div class="repo-error" role="alert">{repo.lastError}</div>
      )}

      <div class="stack-list">
        {sortedStacks.length > 0 ? (
          sortedStacks.map(stack => (
            <StackCard
              key={stack.name}
              stack={stack}
              isSelected={
                selectedStack?.name === stack.name &&
                selectedStack?.repoName === repo.name
              }
              onSelect={() => onSelectStack({ ...stack, repoName: repo.name })}
            />
          ))
        ) : (
          <div class="empty-stacks">No stacks configured</div>
        )}
      </div>
    </div>
  )
}

function StackCard({ stack, isSelected, onSelect }) {
  const status = deriveStackStatus(stack)
  const running = (stack.containers || []).filter(c => c.status === 'running').length
  const total = (stack.containers || []).length

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      onSelect()
    }
  }

  return (
    <div
      class={`stack-card stack-card--${status} ${isSelected ? 'stack-card--selected' : ''}`}
      onClick={onSelect}
      onKeyDown={handleKeyDown}
      role="button"
      tabIndex={0}
      aria-pressed={isSelected}
      aria-label={`${stack.name} — ${status}, ${running} of ${total} containers running`}
    >
      <div class="stack-card__header">
        <span class="stack-card__name">{stack.name}</span>
        <span
          class={`stack-badge stack-badge--${status}`}
          aria-hidden="true"
          title={status === 'degraded' ? 'Some containers are not running' : status === 'applying' ? 'Docker Compose is applying changes' : undefined}
        >
          {status}
        </span>
      </div>
      <div class="stack-card__meta">
        <span class="container-count">{running}/{total} running</span>
        {stack.lastApply && (
          <span class="stack-last-apply">{formatRelative(stack.lastApply)}</span>
        )}
      </div>
      {stack.lastError && (
        <div class="stack-error">{stack.lastError}</div>
      )}
    </div>
  )
}
