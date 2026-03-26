import { useState, useEffect } from 'preact/hooks'
import './AppDetail.css'

export function AppDetail({ app, onClose }) {
  const [logs, setLogs] = useState([])
  const [ws, setWs] = useState(null)

  useEffect(() => {
    // Connect to WebSocket for live logs
    const wsUrl = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}/api/logs/${app.id}`
    const websocket = new WebSocket(wsUrl)

    websocket.onmessage = (event) => {
      const logEntry = JSON.parse(event.data)
      setLogs(prev => [...prev, logEntry].slice(-100)) // Keep last 100 logs
    }

    websocket.onerror = () => {
      console.error('WebSocket connection error')
    }

    setWs(websocket)

    return () => {
      websocket.close()
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
            <span class="label">Version:</span>
            <span>{app.version}</span>
          </div>
        )}
      </div>

      <div class="logs-container">
        <h3>Live Logs</h3>
        <div class="logs-content">
          {logs.length > 0 ? (
            logs.map((log, idx) => (
              <div key={idx} class={`log-line log-${log.level}`}>
                <span class="log-time">{log.timestamp}</span>
                <span class="log-level">{log.level}</span>
                <span class="log-message">{log.message}</span>
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
