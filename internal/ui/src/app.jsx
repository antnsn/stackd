import { useState, useEffect } from 'preact/hooks'
import { AppGrid } from './components/AppGrid'
import { AppDetail } from './components/AppDetail'
import './app.css'

export function App() {
  const [selectedApp, setSelectedApp] = useState(null)
  const [apps, setApps] = useState([])

  useEffect(() => {
    fetch('/api/status')
      .then(r => r.json())
      .then(data => {
        const containers = []
        for (const repo of data.repos || []) {
          for (const stack of repo.stacks || []) {
            for (const c of stack.containers || []) {
              containers.push({
                id: c.name,
                name: c.name,
                status: c.status,
                version: c.image,
              })
            }
          }
        }
        setApps(containers)
      })
      .catch(() => setApps([]))
  }, [])

  return (
    <div class="app-shell">
      <header class="app-header">
        <img src="/logo.svg" alt="stackd" class="app-logo" />
      </header>
      <div class="app-container">
        <div class="grid-panel">
          <AppGrid apps={apps} onSelect={setSelectedApp} />
        </div>
        {selectedApp && (
          <div class="detail-panel">
            <AppDetail app={selectedApp} onClose={() => setSelectedApp(null)} />
          </div>
        )}
      </div>
    </div>
  )
}
