import React, { useState, useEffect } from 'react'

export default function Settings({ ws }) {
  const [cfg, setCfg] = useState({})
  const [memLimit, setMemLimit] = useState('512')
  const [budgetLimit, setBudgetLimit] = useState('1.00')
  const [saved, setSaved] = useState('')

  useEffect(() => {
    if (ws.connected) {
      ws.call('config.get').then(r => {
        setCfg(r || {})
        if (r?.model) setCfg(prev => ({ ...prev, ...r }))
      }).catch(() => {})
    }
  }, [ws.connected])

  const saveConfig = async () => {
    try {
      await ws.call('config.set', { provider: cfg.provider, model: cfg.model, apiKey: cfg.apiKey })
      setSaved('Config saved ✓')
      setTimeout(() => setSaved(''), 3000)
    } catch (e) { alert(e.message) }
  }

  return (
    <div>
      <div className="page-header">
        <h1>Settings</h1>
        {saved && <span className="badge badge-green">{saved}</span>}
      </div>

      <div className="grid grid-2">
        <div className="card">
          <h3>LLM Provider</h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div>
              <label style={{ fontSize: 12, color: '#666', display: 'block', marginBottom: 4 }}>Provider</label>
              <input className="input" value={cfg.provider || ''} onChange={e => setCfg({ ...cfg, provider: e.target.value })} placeholder="anthropic" />
            </div>
            <div>
              <label style={{ fontSize: 12, color: '#666', display: 'block', marginBottom: 4 }}>Model</label>
              <input className="input" value={cfg.model || ''} onChange={e => setCfg({ ...cfg, model: e.target.value })} placeholder="claude-sonnet-4-6" />
            </div>
            <div>
              <label style={{ fontSize: 12, color: '#666', display: 'block', marginBottom: 4 }}>API Key</label>
              <input className="input" type="password" value={cfg.apiKey || ''} onChange={e => setCfg({ ...cfg, apiKey: e.target.value })} placeholder="sk-..." />
            </div>
            <button className="btn btn-primary" onClick={saveConfig}>Save Provider</button>
          </div>
        </div>

        <div className="card">
          <h3>Resource Limits</h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div>
              <label style={{ fontSize: 12, color: '#666', display: 'block', marginBottom: 4 }}>Memory per context (MB)</label>
              <input className="input" type="number" value={memLimit} onChange={e => setMemLimit(e.target.value)} />
              <p style={{ fontSize: 11, color: '#555', marginTop: 4 }}>Contexts exceeding this limit are recycled. 0 = unlimited.</p>
            </div>
            <div>
              <label style={{ fontSize: 12, color: '#666', display: 'block', marginBottom: 4 }}>Default budget per agent (USD)</label>
              <input className="input" type="number" step="0.01" value={budgetLimit} onChange={e => setBudgetLimit(e.target.value)} />
              <p style={{ fontSize: 11, color: '#555', marginTop: 4 }}>Agents stop when exceeding budget. 0 = unlimited.</p>
            </div>
          </div>
        </div>

        <div className="card">
          <h3>Kernel</h3>
          <div style={{ fontSize: 13, color: '#aaa', lineHeight: 2 }}>
            <div>Binary: {cfg.binaryPath || 'auto-detect'}</div>
            <div>Auto-restart on crash: <span className="badge badge-green">Enabled</span> (max 3 restarts)</div>
            <div>Context pool: 10 pre-warmed, 20 max, recycle after 50 uses</div>
          </div>
        </div>

        <div className="card">
          <h3>About</h3>
          <div style={{ fontSize: 13, color: '#aaa', lineHeight: 2 }}>
            <div>VulpineOS — Agent Security Runtime</div>
            <div>Browser: Camoufox (Firefox 146.0.1)</div>
            <div>Protocol: Juggler + foxbridge CDP proxy</div>
            <div>OpenClaw integration: {cfg.setupComplete ? 'Configured' : 'Not configured'}</div>
          </div>
        </div>
      </div>
    </div>
  )
}
