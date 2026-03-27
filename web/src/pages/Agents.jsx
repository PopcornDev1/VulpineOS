import React, { useState, useEffect } from 'react'
import { Link } from 'react-router-dom'

export default function Agents({ ws }) {
  const [agents, setAgents] = useState([])
  const [task, setTask] = useState('')
  const [costs, setCosts] = useState([])

  const refresh = () => {
    ws.call('agents.list').then(r => setAgents(r?.agents || [])).catch(() => {})
    ws.call('costs.getAll').then(r => setCosts(r?.usage || [])).catch(() => {})
  }

  useEffect(() => { if (ws.connected) refresh() }, [ws.connected])

  const spawn = async () => {
    if (!task.trim()) return
    try {
      await ws.call('agents.spawn', { task })
      setTask('')
      setTimeout(refresh, 1000)
    } catch (e) { alert('Spawn failed: ' + e.message) }
  }

  const kill = async (id) => {
    if (!confirm('Kill agent ' + id.substring(0, 8) + '?')) return
    try { await ws.call('agents.kill', { agentId: id }); refresh() }
    catch (e) { alert(e.message) }
  }

  const pause = async (id) => {
    try { await ws.call('agents.pause', { agentId: id }); refresh() }
    catch (e) { alert(e.message) }
  }

  const getCost = (id) => {
    const c = costs.find(u => u.agentId === id)
    return c ? `$${c.estimatedCost.toFixed(4)}` : '—'
  }

  const getTokens = (id) => {
    const c = costs.find(u => u.agentId === id)
    return c ? c.totalTokens.toLocaleString() : '—'
  }

  return (
    <div>
      <div className="page-header">
        <h1>Agents</h1>
        <div style={{ display: 'flex', gap: 8 }}>
          <input className="input" style={{ width: 300 }} placeholder="Task description..." value={task}
            onChange={e => setTask(e.target.value)} onKeyDown={e => e.key === 'Enter' && spawn()} />
          <button className="btn btn-primary" onClick={spawn}>Spawn</button>
          <button className="btn btn-ghost" onClick={refresh}>↻</button>
        </div>
      </div>

      <div className="card">
        <table className="table">
          <thead><tr><th>Agent</th><th>Status</th><th>Fingerprint</th><th>Tokens</th><th>Cost</th><th>Actions</th></tr></thead>
          <tbody>
            {agents.length === 0 && (
              <tr><td colSpan="6" style={{ textAlign: 'center', color: '#666', padding: 32 }}>No agents. Spawn one above.</td></tr>
            )}
            {agents.map(a => (
              <tr key={a.id}>
                <td><Link to={`/agents/${a.id}`} style={{ color: '#a78bfa', textDecoration: 'none' }}>{a.name || a.id.substring(0, 12)}</Link></td>
                <td><span className={`badge badge-${a.status === 'active' ? 'green' : a.status === 'paused' ? 'yellow' : a.status === 'completed' ? 'blue' : 'gray'}`}>{a.status}</span></td>
                <td style={{ fontSize: 12, color: '#888' }}>{a.fingerprintSummary || '—'}</td>
                <td>{getTokens(a.id)}</td>
                <td>{getCost(a.id)}</td>
                <td>
                  {a.status === 'active' && <button className="btn btn-ghost btn-sm" onClick={() => pause(a.id)} style={{ marginRight: 4 }}>Pause</button>}
                  <button className="btn btn-danger btn-sm" onClick={() => kill(a.id)}>Kill</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
