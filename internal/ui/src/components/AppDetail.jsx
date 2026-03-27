import { useState, useEffect } from 'preact/hooks'
import './AppDetail.css'

export function AppDetail({ app, onClose }) {
  const [logs, setLogs] = useState([])

  useEffect(() => {
    const es = new EventSource(`/api/logs/${app.id}`)

    es.onmessage = (event) => {
      setLogs(prev => [...prev, event.data].slice(-100))
    }

    es.onerror = () => {
      es.close()
    }

    return () => {
      es.close()
    }
  }, [app.id])

  return (
    <div class="app-detail">
      <div class="detail-header">
        <h1>{app.name}</h1>
        <button class="close-btn" onClick={onClose}>✕</button>
      </div>

      <div class="detail-info">
        <div class="info-row">
          <span class="label">Status:</span>
          <span class={`status-badge status-${app.status}`}>{app.status}</span>
        </div>
        {app.version && (
          <div class="info-row">
            <span class="label">Image:</span>
            <span>{app.version}</span>
          </div>
        )}
      </div>

      <div class="logs-container">
        <h3>Live Logs</h3>
        <div class="logs-content">
          {logs.length > 0 ? (
            logs.map((line, idx) => (
              <div key={idx} class="log-line">
                {line}
              </div>
            ))
          ) : (
            <div class="log-line log-info">Waiting for logs...</div>
          )}
        </div>
      </div>
    </div>
  )
}
