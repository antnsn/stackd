import './HostBadge.css'

// Small, muted badge naming the Docker host a stack or repo runs on.
// Purely informational — it never uses the reserved status colours.
// Renders nothing when there is no host name to show.
export function HostBadge({ hostName, title }) {
  if (!hostName) return null
  return (
    <span class="host-badge" title={title || `Host: ${hostName}`}>
      <svg
        class="host-badge__icon"
        width="10"
        height="10"
        viewBox="0 0 16 16"
        fill="currentColor"
        aria-hidden="true"
      >
        <path d="M3.5 2h9A1.5 1.5 0 0 1 14 3.5v2A1.5 1.5 0 0 1 12.5 7h-9A1.5 1.5 0 0 1 2 5.5v-2A1.5 1.5 0 0 1 3.5 2Zm0 6h9A1.5 1.5 0 0 1 14 9.5v2A1.5 1.5 0 0 1 12.5 13h-9A1.5 1.5 0 0 1 2 11.5v-2A1.5 1.5 0 0 1 3.5 8ZM4 4a.75.75 0 1 0 0 1.5A.75.75 0 0 0 4 4Zm0 6a.75.75 0 1 0 0 1.5A.75.75 0 0 0 4 10Z" />
      </svg>
      <span class="host-badge__name">{hostName}</span>
    </span>
  )
}
