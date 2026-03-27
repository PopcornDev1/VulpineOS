import React, { useState } from 'react'

export default function Settings({ ws }) {
  const [provider, setProvider] = useState('')
  const [model, setModel] = useState('')
  const [memLimit, setMemLimit] = useState('512')
  const [budgetLimit, setBudgetLimit] = useState('1.00')
  const [autoRestart, setAutoRestart] = useState(true)

  return (
    <div>
      <div className="page-header">
        <h1>Settings</h1>
      </div>

      <div className="grid grid-2">
        <div className="card">
          <h3>LLM Provider</h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div>
              <label style={{ fontSize: 12, color: '#666', display: 'block', marginBottom: 4 }}>Provider</label>
              <input className="input" value={provider} onChange={e => setProvider(e.target.value)} placeholder="anthropic" />
            </div>
            <div>
              <label style={{ fontSize: 12, color: '#666', display: 'block', marginBottom: 4 }}>Model</label>
              <input className="input" value={model} onChange={e => setModel(e.target.value)} placeholder="claude-sonnet-4-6" />
            </div>
            <button className="btn btn-primary">Save Provider</button>
          </div>
        </div>

        <div className="card">
          <h3>Kernel</h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div>
              <label style={{ fontSize: 12, color: '#666', display: 'block', marginBottom: 4 }}>
                <input type="checkbox" checked={autoRestart} onChange={e => setAutoRestart(e.target.checked)} style={{ marginRight: 8 }} />
                Auto-restart kernel on crash (max 3 attempts)
              </label>
            </div>
            <div>
              <label style={{ fontSize: 12, color: '#666', display: 'block', marginBottom: 4 }}>Memory limit per context (MB)</label>
              <input className="input" type="number" value={memLimit} onChange={e => setMemLimit(e.target.value)} />
            </div>
            <button className="btn btn-primary">Save Kernel Settings</button>
          </div>
        </div>

        <div className="card">
          <h3>Agent Budgets</h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div>
              <label style={{ fontSize: 12, color: '#666', display: 'block', marginBottom: 4 }}>Default cost limit per agent (USD)</label>
              <input className="input" type="number" step="0.01" value={budgetLimit} onChange={e => setBudgetLimit(e.target.value)} />
            </div>
            <p style={{ fontSize: 12, color: '#666' }}>Agents stop when they exceed this budget. Set to 0 for unlimited.</p>
            <button className="btn btn-primary">Save Budget</button>
          </div>
        </div>

        <div className="card">
          <h3>Security</h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            <label style={{ fontSize: 12, color: '#888' }}>
              <input type="checkbox" defaultChecked style={{ marginRight: 8 }} />
              Injection-proof accessibility filter
            </label>
            <label style={{ fontSize: 12, color: '#888' }}>
              <input type="checkbox" defaultChecked style={{ marginRight: 8 }} />
              Action-lock (freeze page during thinking)
            </label>
            <label style={{ fontSize: 12, color: '#888' }}>
              <input type="checkbox" defaultChecked style={{ marginRight: 8 }} />
              CSP header injection
            </label>
            <label style={{ fontSize: 12, color: '#888' }}>
              <input type="checkbox" defaultChecked style={{ marginRight: 8 }} />
              DOM mutation monitoring
            </label>
            <label style={{ fontSize: 12, color: '#888' }}>
              <input type="checkbox" defaultChecked style={{ marginRight: 8 }} />
              Sandbox agent JS evaluations
            </label>
            <label style={{ fontSize: 12, color: '#888' }}>
              <input type="checkbox" defaultChecked style={{ marginRight: 8 }} />
              Prompt injection signature scanning
            </label>
          </div>
        </div>
      </div>
    </div>
  )
}
