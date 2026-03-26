import { useState, useEffect } from 'preact/hooks'
import { AppGrid } from './components/AppGrid'
import { AppDetail } from './components/AppDetail'
import './app.css'

export function App() {
  const [selectedApp, setSelectedApp] = useState(null)
  const [apps, setApps] = useState([])

  useEffect(() => {
    // Fetch apps from backend
    fetch('/api/apps')
      .then(r => r.json())
      .then(data => setApps(data))
      .catch(() => setApps([]))
  }, [])

  return (
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
  )
}
