// Multi-host helpers.
//
// "local" is the built-in host that talks to the local Docker socket. It is the
// default for every repo/stack and always exists. Everything here degrades
// gracefully when the backend has no multi-host support (older builds): the
// hosts endpoint simply returns nothing and callers fall back to local-only.

import { apiFetch } from './auth.js'

export const LOCAL_HOST_ID = 'local'

// True for the built-in local host: an empty/absent id or the literal "local".
export function isLocalHostId(id) {
  return !id || id === LOCAL_HOST_ID
}

// Fetch configured Docker hosts. Returns [] on any failure (unauthorized is
// handled by apiFetch; an older backend without the endpoint 404s) so callers
// never crash and simply behave as a single-host (local-only) install.
export async function fetchHosts() {
  try {
    const res = await apiFetch('/api/settings/hosts')
    if (!res.ok) return []
    const data = await res.json()
    return Array.isArray(data) ? data : []
  } catch {
    return []
  }
}
