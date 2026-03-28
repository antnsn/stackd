import './AppGrid.css'

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

function deriveStackStatus(stack) {
  if (stack.status === 'error') return 'error'
  if (stack.status === 'applying') return 'applying'
  const containers = stack.containers || []
  if (!containers.length) return 'unknown'
  if (containers.every(c => c.status === 'running')) return 'running'
  if (containers.some(c => c.status === 'running')) return 'degraded'
  return 'stopped'
}

export function AppGrid({ repos, selectedStack, syncingRepos, onSelectStack, onForceSync }) {
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
      {repos.map(repo => (
        <RepoGroup
          key={repo.name}
          repo={repo}
          selectedStack={selectedStack}
          isSyncing={syncingRepos.has(repo.name)}
          onSelectStack={onSelectStack}
          onForceSync={onForceSync}
        />
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
          {repo.lastSha && (
            <span class="repo-sha">{repo.lastSha.slice(0, 7)}</span>
          )}
        </div>
        <div class="repo-header__right">
          {repo.lastSync && (
            <span class="repo-last-sync">{formatRelative(repo.lastSync)}</span>
          )}
          <button
            class={`sync-btn ${isSyncing ? 'sync-btn--spinning' : ''}`}
            onClick={() => onForceSync(repo.name)}
            title={isSyncing ? 'Syncing…' : 'Force git sync'}
            disabled={isSyncing}
          >
            ↻
          </button>
        </div>
      </div>

      {repo.lastError && (
        <div class="repo-error">{repo.lastError}</div>
      )}

      <div class="stack-list">
        {(repo.stacks || []).length > 0 ? (
          repo.stacks.map(stack => (
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

  return (
    <div
      class={`stack-card stack-card--${status} ${isSelected ? 'stack-card--selected' : ''}`}
      onClick={onSelect}
    >
      <div class="stack-card__header">
        <span class="stack-card__name">{stack.name}</span>
        <span class={`stack-badge stack-badge--${status}`}>{status}</span>
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
