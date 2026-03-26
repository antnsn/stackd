import './AppGrid.css'

export function AppGrid({ apps, onSelect }) {
  return (
    <div class="app-grid">
      <h2>Applications</h2>
      <div class="grid-list">
        {apps.length > 0 ? (
          apps.map(app => (
            <div
              key={app.id}
              class="grid-item"
              onClick={() => onSelect(app)}
            >
              <div class="app-name">{app.name}</div>
              <div class="app-status" data-status={app.status}>
                {app.status}
              </div>
            </div>
          ))
        ) : (
          <div class="empty-state">No applications found</div>
        )}
      </div>
    </div>
  )
}
