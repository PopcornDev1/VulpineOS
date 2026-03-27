import React, { useState, useEffect } from 'react'

export default function Webhooks({ ws }) {
  const [hooks, setHooks] = useState([])
  const [url, setUrl] = useState('')
  const [secret, setSecret] = useState('')
  const [events, setEvents] = useState('')

  const refresh = () => {
    ws.call('webhooks.list').then(r => setHooks(r?.webhooks || [])).catch(() => {})
  }

  useEffect(() => { if (ws.connected) refresh() }, [ws.connected])

  const addHook = async () => {
    if (!url.trim()) return
    const eventList = events.trim() ? events.split(',').map(e => e.trim()) : []
    try {
      await ws.call('webhooks.add', { url: url.trim(), events: eventList, secret })
      setUrl(''); setSecret(''); setEvents('')
      refresh()
    } catch (e) { alert(e.message) }
  }

  const removeHook = async (id) => {
    try { await ws.call('webhooks.remove', { id }); refresh() } catch (e) { alert(e.message) }
  }

  return (
    <div>
      <div className="page-header"><h1>Webhooks</h1></div>

      <div className="card">
        <h3>Add Webhook</h3>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          <input className="input" style={{ flex: 2, minWidth: 250 }} placeholder="https://your-server.com/webhook" value={url} onChange={e => setUrl(e.target.value)} />
          <input className="input" style={{ flex: 1, minWidth: 150 }} placeholder="Secret (optional)" value={secret} onChange={e => setSecret(e.target.value)} />
          <input className="input" style={{ flex: 1, minWidth: 200 }} placeholder="Events (comma-separated, empty=all)" value={events} onChange={e => setEvents(e.target.value)} />
          <button className="btn btn-primary" onClick={addHook}>Add</button>
        </div>
        <p style={{ fontSize: 11, color: '#555', marginTop: 8 }}>Events: agent.completed, agent.failed, agent.paused, rate_limit.detected, injection.detected, budget.alert, budget.exceeded</p>
      </div>

      <div className="card">
        <h3>Registered ({hooks.length})</h3>
        <table className="table">
          <thead><tr><th>URL</th><th>Events</th><th>Secret</th><th>Actions</th></tr></thead>
          <tbody>
            {hooks.length === 0 && <tr><td colSpan="4" style={{ textAlign: 'center', color: '#666', padding: 32 }}>No webhooks registered.</td></tr>}
            {hooks.map(h => (
              <tr key={h.id}>
                <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{h.url}</td>
                <td>{h.events?.length > 0 ? h.events.join(', ') : 'all'}</td>
                <td>{h.secret ? '••••••' : '—'}</td>
                <td><button className="btn btn-ghost btn-sm" onClick={() => removeHook(h.id)}>Remove</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
