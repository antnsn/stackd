// Dashboard token auth.
//
// The backend requires a bearer token on every request. fetch() calls send it
// via an `Authorization: Bearer <token>` header; EventSource and WebSocket
// connections (which cannot set headers) send it via a `?token=<token>` query
// parameter. The token is persisted in localStorage so it survives reloads.

const TOKEN_KEY = 'stackd_token'

// Set true when a request comes back 401 — forces the login gate even if a
// (now-invalid) token is still lingering during the same tick.
let unauthorized = false

const listeners = new Set()

function emit() {
  for (const fn of [...listeners]) {
    try { fn() } catch { /* listener errors must not break auth */ }
  }
}

export function getToken() {
  try { return localStorage.getItem(TOKEN_KEY) || '' } catch { return '' }
}

export function setToken(t) {
  try { localStorage.setItem(TOKEN_KEY, t || '') } catch { /* private mode */ }
  unauthorized = false
  emit()
}

export function clearToken() {
  try { localStorage.removeItem(TOKEN_KEY) } catch { /* private mode */ }
  emit()
}

// Subscribe to auth-state changes (login, logout, 401). Returns an unsubscribe.
export function subscribe(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

// True when the UI should show the login gate: no token yet, or the last
// request was rejected as unauthorized.
export function needsLogin() {
  return unauthorized || !getToken()
}

// fetch() wrapper that attaches the bearer token and reacts to 401 by clearing
// the (invalid) token and surfacing the login gate.
export async function apiFetch(path, opts = {}) {
  const token = getToken()
  const headers = new Headers(opts.headers || {})
  if (token) headers.set('Authorization', `Bearer ${token}`)
  const res = await fetch(path, { ...opts, headers })
  if (res.status === 401) {
    clearToken()
    unauthorized = true
    emit()
  }
  return res
}

// Append the token as a query param for EventSource / WebSocket URLs, which
// cannot carry an Authorization header. Respects any existing query string.
export function withToken(url) {
  const token = getToken()
  if (!token) return url
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}token=${encodeURIComponent(token)}`
}
