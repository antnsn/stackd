import { useState, useEffect } from 'preact/hooks'
import { AppGrid } from './components/AppGrid'
import { AppDetail } from './components/AppDetail'
import { Settings } from './components/Settings'
import './app.css'

export function App() {
  const [page, setPage] = useState('dashboard') // 'dashboard' | 'settings'
  const [repos, setRepos] = useState([])
  const [infisical, setInfisical] = useState(null)
  const [selectedStack, setSelectedStack] = useState(null)
  const [error, setError] = useState(null)
  const [syncingRepos, setSyncingRepos] = useState(new Set())
  // syncStatus: Map<repoName, { state: 'success'|'rateLimit'|'error', message?: string }>
  const [syncStatus, setSyncStatus] = useState({})

  const fetchStatus = () => {
    fetch('/api/status')
      .then(r => r.json())
      .then(data => {
        setRepos(data.repos || [])
        setInfisical(data.infisical)
        setError(null)
      })
      .catch(err => setError(err.message))
  }

  useEffect(() => {
    fetchStatus()
    const interval = setInterval(fetchStatus, 5000)
    return () => clearInterval(interval)
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

  return (
    <div class="app-shell">
      <header class="app-header">
        <div class="app-header__brand">
          <img src="/logo.svg" alt="stackd" class="app-logo" width="97" height="36" />
        </div>
        <div class="app-header__meta">
          {infisical?.enabled && (
            <span class="infisical-badge">Infisical · {infisical.env}</span>
          )}
          <nav class="app-nav" aria-label="Main navigation">
            <button
              class={`app-nav__btn ${page === 'dashboard' ? 'active' : ''}`}
              onClick={() => setPage('dashboard')}
            >
              Dashboard
            </button>
            <button
              class={`app-nav__btn ${page === 'settings' ? 'active' : ''}`}
              onClick={() => setPage('settings')}
            >
              Settings
            </button>
          </nav>
        </div>
      </header>

      {error && (
        <div class="error-banner" role="alert">
          <span><span aria-hidden="true">⚠</span> Could not reach API: {error}</span>
          <button onClick={fetchStatus}>Retry</button>
        </div>
      )}

      <div class="app-container">
        {page === 'settings' ? (
          <Settings />
        ) : (
          <>
            <div class="grid-panel">
              <AppGrid
                repos={repos}
                selectedStack={selectedStack}
                syncingRepos={syncingRepos}
                syncStatus={syncStatus}
                onSelectStack={setSelectedStack}
                onForceSync={handleForceSync}
              />
            </div>
            {selectedStack && (
              <div class="detail-panel">
                <AppDetail
                  stack={selectedStack}
                  onClose={() => setSelectedStack(null)}
                  onRefresh={fetchStatus}
                />
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}
