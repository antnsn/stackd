import { useState, useEffect } from 'preact/hooks'
import { apiFetch } from '../utils/auth.js'
import './Settings.css'
import './HostsSettings.css'

// ── Add / edit host form ──────────────────────────────────────────────────
// dockerHost === "" means the local Docker socket. Users add REMOTE hosts here
// so dockerHost is normally an ssh:// URL; an SSH key is required when remote.

function HostForm({ host, sshKeys, onClose, onSaved }) {
  const isEdit = !!host
  const [form, setForm] = useState({
    name: host?.name || '',
    dockerHost: host?.dockerHost || '',
    sshKeyId: host?.sshKeyId || '',
    enabled: host?.enabled !== false,
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)

  const set = (key, val) => setForm(f => ({ ...f, [key]: val }))
  const isRemote = form.dockerHost.trim() !== ''

  const save = async () => {
    if (!form.name.trim()) { setError('Name is required'); return }
    if (isRemote && !form.sshKeyId) { setError('An SSH key is required for remote (ssh://) hosts'); return }
    setSaving(true); setError(null)
    const body = {
      name: form.name.trim(),
      dockerHost: form.dockerHost.trim(),
      sshKeyId: isRemote ? form.sshKeyId : '',
      enabled: form.enabled,
    }
    const url = isEdit ? `/api/settings/hosts/${host.id}` : '/api/settings/hosts'
    try {
      const res = await apiFetch(url, {
        method: isEdit ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      const data = await res.json().catch(() => ({}))
      if (!res.ok) { setError(data.error || 'Save failed'); setSaving(false); return }
      onSaved()
    } catch (e) { setError(e.message); setSaving(false) }
  }

  return (
    <div class="inline-form">
      <div class="inline-form__title">{isEdit ? 'Edit host' : 'Add host'}</div>
      {error && <p class="settings-error">{error}</p>}
      <div class="form-grid">
        <label class="form-label">
          Name
          <input class="form-input" value={form.name} onInput={e => set('name', e.target.value)} placeholder="prod-node-1" />
        </label>
        <label class="form-label">
          Docker host
          <input
            class="form-input mono"
            value={form.dockerHost}
            onInput={e => set('dockerHost', e.target.value)}
            placeholder="ssh://user@host[:22]"
          />
          <span class="field-note">Leave blank for the local Docker socket. Use an ssh:// URL for a remote host.</span>
        </label>
        {isRemote && (
          <label class="form-label">
            SSH key
            <select class="form-select" value={form.sshKeyId} onChange={e => set('sshKeyId', e.target.value)} name="sshKeyId">
              <option value="">— select a key —</option>
              {sshKeys.map(k => <option key={k.id} value={k.id}>{k.name}</option>)}
            </select>
          </label>
        )}
        <label class="form-label form-checkbox-row">
          <input type="checkbox" checked={form.enabled} onChange={e => set('enabled', e.target.checked)} />
          Enabled
        </label>
      </div>
      <div class="form-actions">
        <button class="btn-ghost" onClick={onClose}>Cancel</button>
        <button class="btn-primary" onClick={save} disabled={saving}>
          {saving ? 'Saving…' : 'Save'}
        </button>
      </div>
    </div>
  )
}

// ── Hosts settings section ────────────────────────────────────────────────

export function HostsSettings({ sshKeys = [] }) {
  const [hosts, setHosts] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [showForm, setShowForm] = useState(false)          // false | 'add' | host object
  const [deleteConfirm, setDeleteConfirm] = useState(null) // null | host id
  const [tests, setTests] = useState({})                   // id -> { state, version?, error? }

  const load = () => {
    setLoading(true)
    apiFetch('/api/settings/hosts')
      .then(r => r.ok ? r.json() : Promise.reject(new Error(`HTTP ${r.status}`)))
      .then(data => { setHosts(Array.isArray(data) ? data : []); setError(null); setLoading(false) })
      .catch(e => { setError(e.message); setLoading(false) })
  }

  useEffect(load, [])

  const deleteHost = async (id) => {
    setDeleteConfirm(null)
    try {
      const res = await apiFetch(`/api/settings/hosts/${id}`, { method: 'DELETE' })
      if (!res.ok) {
        const data = await res.json().catch(() => ({}))
        setError(data.error || 'Delete failed')
      }
    } catch (e) { setError(e.message) }
    load()
  }

  const testHost = async (id) => {
    setTests(t => ({ ...t, [id]: { state: 'loading' } }))
    try {
      const res = await apiFetch(`/api/settings/hosts/${id}/test`, { method: 'POST' })
      const data = await res.json().catch(() => ({}))
      if (res.ok && data.ok) {
        setTests(t => ({ ...t, [id]: { state: 'ok', version: data.serverVersion } }))
      } else {
        setTests(t => ({ ...t, [id]: { state: 'err', error: data.error || `HTTP ${res.status}` } }))
      }
    } catch (e) {
      setTests(t => ({ ...t, [id]: { state: 'err', error: e.message } }))
    }
  }

  return (
    <div class="settings-tab">
      <div class="settings-tab__header">
        <h2>Hosts</h2>
        {!showForm && (
          <button class="btn-primary" onClick={() => setShowForm('add')}>+ Add host</button>
        )}
      </div>

      {showForm && (
        <HostForm
          host={showForm === 'add' ? null : showForm}
          sshKeys={sshKeys}
          onClose={() => setShowForm(false)}
          onSaved={() => { setShowForm(false); load() }}
        />
      )}

      {error && <p class="settings-error">{error}</p>}

      {loading ? (
        <p class="settings-loading">Loading…</p>
      ) : hosts.length === 0 ? (
        <div class="settings-empty">
          <p>No hosts configured.</p>
          <p>Add a remote Docker host to run stacks beyond the local socket.</p>
        </div>
      ) : (
        <div class="host-list">
          {hosts.map(host => {
            const test = tests[host.id]
            return (
              <div class="host-item" key={host.id}>
                <div class="host-item__body">
                  <div class="host-item__title">
                    <span class="host-item__name">{host.name}</span>
                    {host.isLocal && <span class="host-tag host-tag--local">local</span>}
                    <span class={`status-pill ${host.enabled ? 'status-pill--on' : 'status-pill--off'}`}>
                      {host.enabled ? 'enabled' : 'disabled'}
                    </span>
                  </div>
                  <span class="host-item__addr mono">
                    {host.isLocal || !host.dockerHost ? 'local socket' : host.dockerHost}
                  </span>
                  {test && (
                    <span class={`host-test host-test--${test.state}`} aria-live="polite">
                      {test.state === 'loading' && 'Testing connection…'}
                      {test.state === 'ok' && `Connected${test.version ? ` · Docker ${test.version}` : ''}`}
                      {test.state === 'err' && `Failed: ${test.error}`}
                    </span>
                  )}
                </div>
                <div class="host-item__actions">
                  <button
                    class="btn-ghost btn-sm"
                    onClick={() => testHost(host.id)}
                    disabled={test?.state === 'loading'}
                  >
                    {test?.state === 'loading' ? 'Testing…' : 'Test connection'}
                  </button>
                  {!host.isLocal && (
                    deleteConfirm === host.id ? (
                      <span class="confirm-row">
                        Delete {host.name}?
                        <button class="btn-ghost btn-sm btn-danger-confirm" onClick={() => deleteHost(host.id)}>Confirm</button>
                        <button class="btn-ghost btn-sm" onClick={() => setDeleteConfirm(null)}>Cancel</button>
                      </span>
                    ) : (
                      <>
                        <button class="btn-icon" onClick={() => { setShowForm(host); setDeleteConfirm(null) }} aria-label={`Edit ${host.name}`}>
                          <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M11.013 1.427a1.75 1.75 0 0 1 2.474 0l1.086 1.086a1.75 1.75 0 0 1 0 2.474l-8.61 8.61c-.21.21-.47.364-.756.445l-3.251.93a.75.75 0 0 1-.927-.928l.929-3.25c.081-.286.235-.547.445-.758l8.61-8.61z"/></svg>
                        </button>
                        <button class="btn-icon btn-icon--danger" onClick={() => setDeleteConfirm(host.id)} aria-label={`Delete ${host.name}`}>
                          <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M11 1.75V3h2.25a.75.75 0 0 1 0 1.5H2.75a.75.75 0 0 1 0-1.5H5V1.75C5 .784 5.784 0 6.75 0h2.5C10.216 0 11 .784 11 1.75zM6.5 1.75V3h3V1.75a.25.25 0 0 0-.25-.25h-2.5a.25.25 0 0 0-.25.25zM4.997 6.5a.75.75 0 1 0-1.5.006l.139 9.25a.75.75 0 0 0 1.5-.005zm5.006.006a.75.75 0 0 0-1.5-.006l-.139 9.25a.75.75 0 0 0 1.5.005z"/></svg>
                        </button>
                      </>
                    )
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
