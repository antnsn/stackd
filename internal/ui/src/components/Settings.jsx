import { useState, useEffect } from 'preact/hooks'
import './Settings.css'

// ---- Repos tab -----------------------------------------------------------

function RepoForm({ repo, sshKeys, onClose, onSaved }) {
  const isEdit = !!repo
  const [form, setForm] = useState({
    name: repo?.name || '',
    url: repo?.url || '',
    branch: repo?.branch || 'main',
    remote: repo?.remote || 'origin',
    authType: repo?.authType || 'none',
    sshKeyId: repo?.sshKeyId || '',
    pat: '',
    stacksDir: repo?.stacksDir || '.',
    syncInterval: repo?.syncInterval || 60,
    enabled: repo?.enabled !== false,
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)

  const set = (key, val) => setForm(f => ({ ...f, [key]: val }))

  const save = async () => {
    if (!form.name || !form.url) { setError('Name and URL are required'); return }
    setSaving(true); setError(null)
    const body = { ...form, syncInterval: Number(form.syncInterval) }
    if (body.authType !== 'pat') delete body.pat
    if (body.authType !== 'ssh') delete body.sshKeyId

    const url = isEdit ? `/api/settings/repos/${repo.id}` : '/api/settings/repos'
    try {
      const res = await fetch(url, {
        method: isEdit ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      const data = await res.json()
      if (!res.ok) { setError(data.error || 'Save failed'); setSaving(false); return }
      onSaved()
    } catch (e) { setError(e.message); setSaving(false) }
  }

  return (
    <div class="inline-form">
      <div class="inline-form__title">{isEdit ? 'Edit repository' : 'Add repository'}</div>
      {error && <p class="settings-error">{error}</p>}
      <div class="form-grid">
        <label class="form-label">
          Name
          <input class="form-input" value={form.name} onInput={e => set('name', e.target.value)} placeholder="my-infra" />
        </label>
        <label class="form-label">
          URL
          <input class="form-input" value={form.url} onInput={e => set('url', e.target.value)} placeholder="git@github.com:user/repo.git" />
        </label>
        <label class="form-label">
          Branch
          <input class="form-input" value={form.branch} onInput={e => set('branch', e.target.value)} />
        </label>
        <label class="form-label">
          Remote
          <input class="form-input" value={form.remote} onInput={e => set('remote', e.target.value)} />
        </label>
        <label class="form-label">
          Authentication
          <select class="form-select" value={form.authType} onChange={e => set('authType', e.target.value)} name="authType">
            <option value="none">None (public repo)</option>
            <option value="ssh">SSH key</option>
            <option value="pat">PAT (HTTPS)</option>
          </select>
        </label>
        {form.authType === 'ssh' && (
          <label class="form-label">
            SSH key
            <select class="form-select" value={form.sshKeyId} onChange={e => set('sshKeyId', e.target.value)} name="sshKeyId">
              <option value="">— select a key —</option>
              {sshKeys.map(k => <option key={k.id} value={k.id}>{k.name}</option>)}
            </select>
          </label>
        )}
        {form.authType === 'pat' && (
          <label class="form-label">
            Personal Access Token
            <input class="form-input" type="password" value={form.pat} onInput={e => set('pat', e.target.value)}
              placeholder={isEdit ? '(leave blank to keep current)' : 'ghp_…'} />
          </label>
        )}
        <label class="form-label">
          Stacks directory
          <input class="form-input" value={form.stacksDir} onInput={e => set('stacksDir', e.target.value)} placeholder="." />
        </label>
        <label class="form-label">
          Sync interval (seconds)
          <input class="form-input" type="number" value={form.syncInterval} onInput={e => set('syncInterval', e.target.value)} min="10" />
        </label>
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

function ReposTab({ sshKeys }) {
  const [repos, setRepos] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const [showForm, setShowForm] = useState(false) // false | 'add' | repo object
  const [deleteConfirm, setDeleteConfirm] = useState(null) // null | repo id

  const loadRepos = () => {
    setLoading(true)
    fetch('/api/settings/repos')
      .then(r => r.json())
      .then(data => { setRepos(data); setLoading(false) })
      .catch(e => { setError(e.message); setLoading(false) })
  }

  useEffect(loadRepos, [])

  const deleteRepo = async (id) => {
    await fetch(`/api/settings/repos/${id}`, { method: 'DELETE' })
    setDeleteConfirm(null)
    loadRepos()
  }

  return (
    <div class="settings-tab">
      <div class="settings-tab__header">
        <h2>Repositories</h2>
        {!showForm && (
          <button class="btn-primary" onClick={() => setShowForm('add')}>+ Add repo</button>
        )}
      </div>

      {showForm && (
        <RepoForm
          repo={showForm === 'add' ? null : showForm}
          sshKeys={sshKeys}
          onClose={() => setShowForm(false)}
          onSaved={() => { setShowForm(false); loadRepos() }}
        />
      )}

      {error && <p class="settings-error">{error}</p>}
      {loading ? (
        <p class="settings-loading">Loading…</p>
      ) : repos.length === 0 ? (
        <div class="settings-empty">
          <p>No repos configured.</p>
          <p>Add a repository to start syncing Docker Compose stacks.</p>
        </div>
      ) : (
        <div class="table-wrap">
          <table class="settings-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>URL</th>
                <th>Branch</th>
                <th>Auth</th>
                <th>Stacks dir</th>
                <th>Interval</th>
                <th>Status</th>
                <th aria-label="Actions"></th>
              </tr>
            </thead>
            <tbody>
              {repos.map(repo => (
                <tr key={repo.id}>
                  <td class="td-name">{repo.name}</td>
                  <td class="td-url">{repo.url}</td>
                  <td>{repo.branch}</td>
                  <td>{repo.authType || 'none'}</td>
                  <td class="td-mono">{repo.stacksDir || '.'}</td>
                  <td>{repo.syncInterval}s</td>
                  <td>
                    <span class={`status-pill ${repo.enabled ? 'status-pill--on' : 'status-pill--off'}`}>
                      {repo.enabled ? 'enabled' : 'disabled'}
                    </span>
                  </td>
                  <td class="td-actions">
                    {deleteConfirm === repo.id ? (
                      <span class="confirm-row">
                        Delete {repo.name}?
                        <button class="btn-ghost btn-sm btn-danger-confirm" onClick={() => deleteRepo(repo.id)}>Confirm</button>
                        <button class="btn-ghost btn-sm" onClick={() => setDeleteConfirm(null)}>Cancel</button>
                      </span>
                    ) : (
                      <>
                        <button class="btn-icon" onClick={() => { setShowForm(repo); setDeleteConfirm(null) }} aria-label="Edit repo">
                          <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M11.013 1.427a1.75 1.75 0 0 1 2.474 0l1.086 1.086a1.75 1.75 0 0 1 0 2.474l-8.61 8.61c-.21.21-.47.364-.756.445l-3.251.93a.75.75 0 0 1-.927-.928l.929-3.25c.081-.286.235-.547.445-.758l8.61-8.61z"/></svg>
                        </button>
                        <button class="btn-icon btn-icon--danger" onClick={() => setDeleteConfirm(repo.id)} aria-label="Delete repo">
                          <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M11 1.75V3h2.25a.75.75 0 0 1 0 1.5H2.75a.75.75 0 0 1 0-1.5H5V1.75C5 .784 5.784 0 6.75 0h2.5C10.216 0 11 .784 11 1.75zM6.5 1.75V3h3V1.75a.25.25 0 0 0-.25-.25h-2.5a.25.25 0 0 0-.25.25zM4.997 6.5a.75.75 0 1 0-1.5.006l.139 9.25a.75.75 0 0 0 1.5-.005zm5.006.006a.75.75 0 0 0-1.5-.006l-.139 9.25a.75.75 0 0 0 1.5.005z"/></svg>
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

// ---- SSH Keys tab --------------------------------------------------------

function SSHKeysTab({ onKeysChange }) {
  const [keys, setKeys] = useState([])
  const [repos, setRepos] = useState([])
  const [loading, setLoading] = useState(true)
  const [form, setForm] = useState({ name: '', privateKey: '' })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)
  const [success, setSuccess] = useState(null)
  const [deleteConfirm, setDeleteConfirm] = useState(null) // null | key id

  const loadKeys = () => {
    setLoading(true)
    Promise.all([
      fetch('/api/settings/ssh-keys').then(r => r.json()),
      fetch('/api/settings/repos').then(r => r.json()),
    ])
      .then(([keyData, repoData]) => {
        setKeys(keyData || [])
        setRepos(repoData || [])
        setLoading(false)
        onKeysChange?.(keyData || [])
      })
      .catch(e => { setError(e.message); setLoading(false) })
  }

  useEffect(loadKeys, [])

  const addKey = async () => {
    if (!form.name || !form.privateKey) { setError('Name and private key are required'); return }
    setSaving(true); setError(null); setSuccess(null)
    try {
      const res = await fetch('/api/settings/ssh-keys', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(form),
      })
      const data = await res.json()
      if (!res.ok) { setError(data.error || 'Failed to add key'); setSaving(false); return }
      setForm({ name: '', privateKey: '' })
      const pub = data.publicKey || ''
      setSuccess('Key added — ' + (pub.length > 50 ? pub.slice(0, 50) + '…' : pub))
      setSaving(false)
      loadKeys()
    } catch (e) { setError(e.message); setSaving(false) }
  }

  const deleteKey = async (id) => {
    await fetch(`/api/settings/ssh-keys/${id}`, { method: 'DELETE' })
    setDeleteConfirm(null)
    loadKeys()
  }

  // Map key id → repo names that use it
  const keyUsage = keys.reduce((acc, k) => {
    acc[k.id] = repos.filter(r => r.sshKeyId === k.id).map(r => r.name)
    return acc
  }, {})

  return (
    <div class="settings-tab">
      <div class="settings-tab__header"><h2>SSH Keys</h2></div>

      <div class="settings-section">
        <h3>Add key</h3>
        {error && <p class="settings-error">{error}</p>}
        {success && <p class="settings-success">{success}</p>}
        <div class="form-grid">
          <label class="form-label">
            Name
            <input class="form-input" value={form.name} onInput={e => setForm(f => ({ ...f, name: e.target.value }))} placeholder="deploy-key" />
          </label>
          <label class="form-label">
            Private key (PEM)
            <textarea
              class="form-input form-textarea mono"
              value={form.privateKey}
              onInput={e => setForm(f => ({ ...f, privateKey: e.target.value }))}
              placeholder={'-----BEGIN OPENSSH PRIVATE KEY-----\n…\n-----END OPENSSH PRIVATE KEY-----'}
              rows={8}
            />
          </label>
        </div>
        <div class="form-actions">
          <button class="btn-primary" onClick={addKey} disabled={saving}>
            {saving ? 'Adding…' : 'Add key'}
          </button>
        </div>
      </div>

      <div class="settings-section">
        <h3>Stored keys</h3>
        {loading ? (
          <p class="settings-loading">Loading…</p>
        ) : keys.length === 0 ? (
          <p class="settings-empty">No SSH keys stored.</p>
        ) : (
          <div class="key-list">
            {keys.map(k => (
              <div class="key-item" key={k.id}>
                <div class="key-item__body">
                  <span class="key-item__name">{k.name}</span>
                  <span class="key-item__pub mono">{k.publicKey}</span>
                  {keyUsage[k.id]?.length > 0 ? (
                    <span class="key-item__usage">
                      Used by: {keyUsage[k.id].map(n => <span class="key-item__repo-tag" key={n}>{n}</span>)}
                    </span>
                  ) : (
                    <span class="key-item__usage key-item__usage--unused">Not used by any repo</span>
                  )}
                </div>
                {deleteConfirm === k.id ? (
                  <span class="confirm-row">
                    Delete {k.name}?
                    <button class="btn-ghost btn-sm btn-danger-confirm" onClick={() => deleteKey(k.id)}>Confirm</button>
                    <button class="btn-ghost btn-sm" onClick={() => setDeleteConfirm(null)}>Cancel</button>
                  </span>
                ) : (
                  <button class="btn-icon btn-icon--danger" onClick={() => setDeleteConfirm(k.id)} aria-label={`Delete ${k.name}`}>
                    <svg width="14" height="14" viewBox="0 0 16 16" fill="currentColor"><path d="M11 1.75V3h2.25a.75.75 0 0 1 0 1.5H2.75a.75.75 0 0 1 0-1.5H5V1.75C5 .784 5.784 0 6.75 0h2.5C10.216 0 11 .784 11 1.75zM6.5 1.75V3h3V1.75a.25.25 0 0 0-.25-.25h-2.5a.25.25 0 0 0-.25.25zM4.997 6.5a.75.75 0 1 0-1.5.006l.139 9.25a.75.75 0 0 0 1.5-.005zm5.006.006a.75.75 0 0 0-1.5-.006l-.139 9.25a.75.75 0 0 0 1.5.005z"/></svg>
                  </button>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

// ---- General Settings tab ------------------------------------------------

function InfisicalTab() {
  const [meta, setMeta] = useState(null)
  const [form, setForm] = useState(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)
  const [success, setSuccess] = useState(null)

  const load = () =>
    fetch('/api/settings/general')
      .then(r => r.json())
      .then(data => {
        setMeta(data)
        setForm({
          infisicalToken: '',
          infisicalProjectId: data.infisicalProjectId || '',
          infisicalEnv: data.infisicalEnv || 'prod',
          infisicalUrl: data.infisicalUrl || '',
        })
      })
      .catch(e => setError(e.message))

  useEffect(load, [])

  const set = (key, val) => setForm(f => ({ ...f, [key]: val }))

  const save = async () => {
    setSaving(true); setError(null); setSuccess(null)
    const body = {
      infisicalProjectId: form.infisicalProjectId,
      infisicalEnv: form.infisicalEnv,
      infisicalUrl: form.infisicalUrl,
    }
    if (form.infisicalToken) body.infisicalToken = form.infisicalToken
    try {
      const res = await fetch('/api/settings/general', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      const data = await res.json()
      if (!res.ok) { setError(data.error || 'Save failed'); setSaving(false); return }
      setSuccess('Settings saved')
      setSaving(false)
      load()
    } catch (e) { setError(e.message); setSaving(false) }
  }

  if (!form) return <p class="settings-loading">Loading…</p>

  return (
    <div class="settings-tab">
      <div class="settings-tab__header"><h2>Infisical</h2></div>
      {error && <p class="settings-error">{error}</p>}
      {success && <p class="settings-success">{success}</p>}

      <div class="settings-section">
        <div class="form-grid">
          <label class="form-label">
            Machine token
            {meta?.infisicalTokenSet && (
              <span class="field-hint">
                <span class="dot dot--green" aria-hidden="true"></span> token set
              </span>
            )}
            <input
              class="form-input"
              type="password"
              value={form.infisicalToken}
              onInput={e => set('infisicalToken', e.target.value)}
              placeholder={meta?.infisicalTokenSet ? '(leave blank to keep current)' : 'machine token…'}
            />
          </label>
          <label class="form-label">
            Project ID
            <input
              class="form-input"
              value={form.infisicalProjectId}
              onInput={e => set('infisicalProjectId', e.target.value)}
              placeholder="e.g. 3c90a1b2-..."
            />
          </label>
          <label class="form-label">
            Environment
            <input class="form-input" value={form.infisicalEnv} onInput={e => set('infisicalEnv', e.target.value)} placeholder="prod" />
          </label>
          <label class="form-label">
            Self-hosted URL <span class="form-optional">(optional)</span>
            <input class="form-input" value={form.infisicalUrl} onInput={e => set('infisicalUrl', e.target.value)} placeholder="https://infisical.example.com" />
          </label>
        </div>
      </div>

      <div class="form-actions">
        <button class="btn-primary" onClick={save} disabled={saving}>
          {saving ? 'Saving…' : 'Save settings'}
        </button>
      </div>
    </div>
  )
}

function GeneralTab() {
  const [showPulls, setShowPulls] = useState(
    () => localStorage.getItem('activity-show-pulls') === 'true'
  )
  const [dismissDelay, setDismissDelay] = useState(
    () => localStorage.getItem('activity-dismiss-delay') || '4000'
  )
  const [meta, setMeta] = useState(null)
  const [form, setForm] = useState({ dashboardToken: '', defaultSyncInterval: 60 })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState(null)
  const [success, setSuccess] = useState(null)
  const [sysInfo, setSysInfo] = useState(null)

  const load = () =>
    fetch('/api/settings/general')
      .then(r => r.json())
      .then(data => {
        setMeta(data)
        setForm(f => ({ ...f, defaultSyncInterval: data.defaultSyncInterval || 60 }))
      })
      .catch(() => {})

  useEffect(() => {
    load()
    fetch('/api/settings/system')
      .then(r => r.json())
      .then(setSysInfo)
      .catch(() => {})
  }, [])

  const togglePulls = () => {
    setShowPulls(v => {
      const next = !v
      localStorage.setItem('activity-show-pulls', String(next))
      return next
    })
  }

  const setDelayValue = val => {
    setDismissDelay(val)
    localStorage.setItem('activity-dismiss-delay', val)
  }

  const save = async () => {
    setSaving(true); setError(null); setSuccess(null)
    const body = { defaultSyncInterval: Number(form.defaultSyncInterval) }
    if (form.dashboardToken) body.dashboardToken = form.dashboardToken
    try {
      const res = await fetch('/api/settings/general', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      const data = await res.json()
      if (!res.ok) { setError(data.error || 'Save failed'); setSaving(false); return }
      setSuccess('Settings saved')
      setForm(f => ({ ...f, dashboardToken: '' }))
      setSaving(false)
      load()
    } catch (e) { setError(e.message); setSaving(false) }
  }

  return (
    <div class="settings-tab">
      <div class="settings-tab__header"><h2>General</h2></div>

      <div class="settings-section">
        <h3>Dashboard</h3>
        {error && <p class="settings-error">{error}</p>}
        {success && <p class="settings-success">{success}</p>}
        <div class="form-grid">
          <label class="form-label">
            Dashboard token
            {meta?.dashboardTokenSet && (
              <span class="field-hint">
                <span class="dot dot--green" aria-hidden="true"></span> token set
              </span>
            )}
            <input
              class="form-input"
              type="password"
              value={form.dashboardToken}
              onInput={e => setForm(f => ({ ...f, dashboardToken: e.target.value }))}
              placeholder={meta?.dashboardTokenSet ? '(leave blank to keep current)' : 'set a token to require auth…'}
            />
          </label>
          <label class="form-label">
            Default sync interval (seconds)
            <input
              class="form-input"
              type="number"
              value={form.defaultSyncInterval}
              onInput={e => setForm(f => ({ ...f, defaultSyncInterval: e.target.value }))}
              min="10"
            />
          </label>
        </div>
        <div class="form-actions">
          <button class="btn-primary" onClick={save} disabled={saving}>
            {saving ? 'Saving…' : 'Save settings'}
          </button>
        </div>
      </div>

      <div class="settings-section">
        <h3>Activity feed</h3>
        <label class="toggle-row">
          <span class="toggle-row__label">
            Show repo pulls in activity feed
            <span class="toggle-row__hint">Routine git pulls fire every sync interval — hide them to reduce noise</span>
          </span>
          <button
            class={`toggle-btn${showPulls ? ' toggle-btn--on' : ''}`}
            role="switch"
            aria-checked={showPulls}
            onClick={togglePulls}
          >
            <span class="toggle-btn__knob" />
          </button>
        </label>
        <label class="form-label" style={{ marginTop: '1rem' }}>
          Auto-dismiss delay
          <select
            class="form-select"
            value={dismissDelay}
            onChange={e => setDelayValue(e.target.value)}
          >
            <option value="2000">2 seconds</option>
            <option value="4000">4 seconds</option>
            <option value="8000">8 seconds</option>
            <option value="never">Never</option>
          </select>
        </label>
      </div>

      {sysInfo && (
        <div class="settings-section">
          <h3>About</h3>
          <div class="sys-info-grid">
            <span class="sys-info-grid__label">Version</span>
            <span class="sys-info-grid__value mono">{sysInfo.version}</span>
            <span class="sys-info-grid__label">Uptime</span>
            <span class="sys-info-grid__value mono">{sysInfo.uptime}</span>
            <span class="sys-info-grid__label">Clone dir</span>
            <span class="sys-info-grid__value mono">{sysInfo.cloneDir}</span>
            <span class="sys-info-grid__label">DB path</span>
            <span class="sys-info-grid__value mono">{sysInfo.dbPath}</span>
            <span class="sys-info-grid__label">Go version</span>
            <span class="sys-info-grid__value mono">{sysInfo.goVersion}</span>
          </div>
        </div>
      )}
    </div>
  )
}

// ---- Settings root -------------------------------------------------------

export function Settings() {
  const [tab, setTab] = useState('general')
  const [sshKeys, setSSHKeys] = useState([])

  // Load SSH keys upfront so the repo modal has them regardless of active tab.
  useEffect(() => {
    fetch('/api/settings/ssh-keys')
      .then(r => r.json())
      .then(data => setSSHKeys(data || []))
      .catch(() => {})
  }, [])

  return (
    <div class="settings-page">
      <nav class="settings-nav" aria-label="Settings sections">
        <button
          class={`settings-nav__item ${tab === 'general' ? 'active' : ''}`}
          onClick={() => setTab('general')}
        >
          General
        </button>
        <button
          class={`settings-nav__item ${tab === 'ssh' ? 'active' : ''}`}
          onClick={() => setTab('ssh')}
        >
          SSH Keys
        </button>
        <button
          class={`settings-nav__item ${tab === 'repos' ? 'active' : ''}`}
          onClick={() => setTab('repos')}
        >
          Repositories
        </button>
        <button
          class={`settings-nav__item ${tab === 'infisical' ? 'active' : ''}`}
          onClick={() => setTab('infisical')}
        >
          Infisical
        </button>
      </nav>
      <div class="settings-content">
        {tab === 'general' && <GeneralTab />}
        {tab === 'ssh' && <SSHKeysTab onKeysChange={setSSHKeys} />}
        {tab === 'repos' && <ReposTab sshKeys={sshKeys} />}
        {tab === 'infisical' && <InfisicalTab />}
      </div>
    </div>
  )
}
