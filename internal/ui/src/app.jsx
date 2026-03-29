import { useState, useEffect, useMemo, useCallback } from 'preact/hooks'
import { AppDetail } from './components/AppDetail'
import { RepoCardsView } from './components/RepoCardsView'
import { Settings } from './components/Settings'
import './app.css'

export function App() {
  const [page, setPage] = useState('dashboard')
  const [repos, setRepos] = useState([])
  const [infisical, setInfisical] = useState(null)
  const [selectedRepo, setSelectedRepo] = useState(null)
  const [selectedStack, setSelectedStack] = useState(null)
  const [error, setError] = useState(null)
  const [syncingRepos, setSyncingRepos] = useState(new Set())
  const [syncStatus, setSyncStatus] = useState({})

  const [lastFetched, setLastFetched] = useState(null)
  const [now, setNow] = useState(Date.now())

  const fetchStatus = useCallback(async () => {
    fetch('/api/status')
      .then(r => r.json())
      .then(data => {
        setRepos(data.repos || [])
        setInfisical(data.infisical)
        setError(null)
        setLastFetched(Date.now())
        const errorCount = (data.repos || [])
          .flatMap(r => r.stacks || [])
          .filter(s => {
            if (s.status === 'error') return true
            const containers = s.containers || []
            return containers.length > 0 && !containers.every(c => c.status === 'running')
          }).length
        document.title = errorCount > 0 ? `(${errorCount}) stackd` : 'stackd'
      })
      .catch(err => setError(err.message))
  }, [])

  useEffect(() => {
    fetchStatus()
    let interval = setInterval(fetchStatus, 5000)

    const handleVisibility = () => {
      if (document.hidden) {
        clearInterval(interval)
      } else {
        fetchStatus()
        interval = setInterval(fetchStatus, 5000)
      }
    }

    document.addEventListener('visibilitychange', handleVisibility)
    return () => {
      clearInterval(interval)
      document.removeEventListener('visibilitychange', handleVisibility)
    }
  }, [])

  // Tick every 10s to keep freshness label live between polls
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 10000)
    return () => clearInterval(timer)
  }, [])

  // Keep selectedStack live — sync it from repos on every poll so container
  // status reflects reality immediately after a start/stop/restart action.
  useEffect(() => {
    if (!selectedStack) return
    for (const repo of repos) {
      if (repo.name !== selectedStack.repoName) continue
      const fresh = (repo.stacks || []).find(s => s.name === selectedStack.name)
      if (fresh) setSelectedStack({ ...fresh, repoName: repo.name })
      break
    }
  }, [repos])

  // Auto-select first repo when repos load
  useEffect(() => {
    if (repos.length > 0 && !selectedRepo) {
      setSelectedRepo(repos[0].name)
    }
  }, [repos])

  const clearSyncing = (repoName) => {
    setSyncingRepos(prev => {
      const s = new Set(prev)
      s.delete(repoName)
      return s
    })
  }

  const handleForceSync = (repoName) => {
    setSyncingRepos(prev => new Set([...prev, repoName]))
    fetch(`/api/sync/${repoName}`, { method: 'POST' })
      .then(res => {
        clearSyncing(repoName)
        if (res.status === 429) {
          setSyncStatus(prev => ({ ...prev, [repoName]: { state: 'rateLimit', message: 'Rate limited — wait a moment' } }))
          setTimeout(() => setSyncStatus(prev => { const n = { ...prev }; delete n[repoName]; return n }), 5000)
        } else if (res.ok) {
          setSyncStatus(prev => ({ ...prev, [repoName]: { state: 'success', message: 'Synced ✓' } }))
          setTimeout(() => setSyncStatus(prev => { const n = { ...prev }; delete n[repoName]; return n }), 2000)
        } else {
          setSyncStatus(prev => ({ ...prev, [repoName]: { state: 'error', message: `Sync failed (${res.status})` } }))
          setTimeout(() => setSyncStatus(prev => { const n = { ...prev }; delete n[repoName]; return n }), 4000)
        }
      })
      .catch(err => {
        clearSyncing(repoName)
        setSyncStatus(prev => ({ ...prev, [repoName]: { state: 'error', message: err.message } }))
        setTimeout(() => setSyncStatus(prev => { const n = { ...prev }; delete n[repoName]; return n }), 4000)
      })
  }

  // Derive stacks with problems for the health banner
  const problemStacks = useMemo(() => {
    if (!repos || repos.length === 0) return []
    return repos.flatMap(r =>
      (r.stacks || []).filter(s => {
        if (s.status === 'error') return true
        const containers = s.containers || []
        return containers.length > 0 && !containers.every(c => c.status === 'running')
      }).map(s => ({ ...s, repoName: r.name }))
    )
  }, [repos])

  const freshnessLabel = lastFetched
    ? (() => {
        const s = Math.floor((now - lastFetched) / 1000)
        if (s < 15) return 'just now'
        if (s < 60) return `${s}s ago`
        return `${Math.floor(s / 60)}m ago`
      })()
    : null

  const currentRepo = repos.find(r => r.name === selectedRepo) || null

  return (
    <div class="app-shell">
      <aside class="app-sidebar">
        <div class="sidebar-brand">
          <h1 class="sr-only">stackd</h1>
          <img src="/logo.svg" alt="stackd" class="app-logo" width="118" height="44" />
          <button
            class={`sidebar-mobile-settings${page === 'settings' ? ' active' : ''}`}
            onClick={() => setPage(p => p === 'settings' ? 'dashboard' : 'settings')}
            aria-label="Settings"
          >
            ⚙
          </button>
        </div>
        <div class="sidebar-body">
          <nav class="repo-nav" aria-label="Repositories">
            <span class="repo-nav__label">Repos</span>
            {repos.map(repo => {
              const hasError = (repo.stacks || []).some(s =>
                s.status === 'error' || (s.containers || []).some(c => c.status !== 'running' && c.status !== 'unknown')
              )
              return (
                <button
                  key={repo.name}
                  class={`repo-nav__item${selectedRepo === repo.name ? ' active' : ''}`}
                  onClick={() => { setSelectedRepo(repo.name); setSelectedStack(null); setPage('dashboard') }}
                >
                  <span class={`repo-nav__dot repo-nav__dot--${hasError ? 'error' : 'ok'}`} aria-hidden="true" />
                  <span class="repo-nav__name">{repo.name}</span>
                </button>
              )
            })}
          </nav>
        </div>
        <div class="sidebar-footer">
          <button
            class={`sidebar-settings-btn${page === 'settings' ? ' active' : ''}`}
            onClick={() => setPage(p => p === 'settings' ? 'dashboard' : 'settings')}
          >
            <span aria-hidden="true">⚙</span> Settings
          </button>
          <div class="sidebar-meta">
            {freshnessLabel && (
              <span class="freshness-label" aria-live="polite" aria-label={`Data updated ${freshnessLabel}`}>
                {freshnessLabel}
              </span>
            )}
            {infisical?.enabled && (
              <span class="infisical-badge">Infisical · {infisical.env}</span>
            )}
          </div>
        </div>
      </aside>

      <div class="app-body">
        {error && (
          <div class="error-banner" role="alert">
            <span><span aria-hidden="true">⚠</span> Could not reach API: {error}</span>
            <button onClick={fetchStatus}>Retry</button>
          </div>
        )}

        {!error && problemStacks.length > 0 && (
          <div class="health-banner health-banner--error" role="alert">
            <span class="health-banner__icon" aria-hidden="true">⚠</span>
            <span class="health-banner__text">
              {problemStacks.length} stack{problemStacks.length !== 1 ? 's' : ''} need attention
            </span>
            <div class="health-banner__links">
              {problemStacks.map(s => (
                <button
                  key={`${s.repoName}/${s.name}`}
                  class="health-banner__link"
                  onClick={() => { setSelectedRepo(s.repoName); setSelectedStack(s); setPage('dashboard') }}
                >
                  {s.repoName}/{s.name}
                </button>
              ))}
            </div>
          </div>
        )}

        <main class="app-content">
          {page === 'settings' ? (
            <Settings />
          ) : selectedStack ? (
            <div class="detail-panel">
              <AppDetail
                stack={selectedStack}
                onClose={() => setSelectedStack(null)}
                onRefresh={fetchStatus}
                onForceSync={handleForceSync}
                isSyncing={syncingRepos.has(selectedStack?.repoName)}
              />
            </div>
          ) : currentRepo ? (
            <RepoCardsView
              repo={currentRepo}
              onSelectStack={setSelectedStack}
              isSyncing={syncingRepos.has(currentRepo.name)}
              onSync={handleForceSync}
              syncStatus={syncStatus[currentRepo.name]}
            />
          ) : (
            <div class="empty-detail">
              <p class="empty-detail__hint">No repos configured</p>
            </div>
          )}
        </main>
      </div>
    </div>
  )
}
