import { useState } from 'preact/hooks'
import { setToken } from '../utils/auth.js'
import './LoginGate.css'

// Minimal token-entry gate shown when the dashboard has no valid token.
// The operator finds the token in the server logs (auto-generated on first run)
// or sets one in Settings → General.
export function LoginGate() {
  const [value, setValue] = useState('')

  const submit = (e) => {
    e.preventDefault()
    const t = value.trim()
    if (!t) return
    setToken(t)
  }

  return (
    <div class="login-gate">
      <form class="login-gate__card" onSubmit={submit}>
        <img src="/logo.svg" alt="stackd" class="login-gate__logo" width="132" height="49" />
        <p class="login-gate__title">Dashboard token required</p>
        <p class="login-gate__hint">
          This dashboard is protected. Paste the access token — it is logged on
          first run and can be changed in Settings.
        </p>
        <input
          class="login-gate__input"
          type="password"
          value={value}
          onInput={e => setValue(e.target.value)}
          placeholder="Access token"
          aria-label="Dashboard access token"
          autoFocus
        />
        <button class="login-gate__submit" type="submit" disabled={!value.trim()}>
          Unlock
        </button>
      </form>
    </div>
  )
}
