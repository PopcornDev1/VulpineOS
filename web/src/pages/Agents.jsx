import React, { useState } from 'react'

export default function Agents({ ws }) {
  const [agents, setAgents] = useState([])
  const [newTask, setNewTask] = useState('')
  const [creating, setCreating] = useState(false)

  const refresh = async () => {
    // Agents are tracked client-side from events
  }

  const spawnAgent = async () => {
    if (!newTask.trim()) return
    setCreating(true)
    try {
      // This would call the orchestrator via the WebSocket relay
      await ws.send('control.spawnAgent', { task: newTask })
      setNewTask('')
    } catch (e) {
      alert('Failed: ' + e.message)
    }
    setCreating(false)
  }

  const killAgent = async (id) => {
    if (!confirm('Kill agent ' + id + '?')) return
    try {
      await ws.send('control.killAgent', { agentId: id })
    } catch (e) {
      alert('Failed: ' + e.message)
    }
  }

  return (
    <div>
      <div className="page-header">
        <h1>Agents</h1>
        <div style={{ display: 'flex', gap: 8 }}>
          <input
            className="input"
            style={{ width: 300 }}
            placeholder="New agent task..."
            value={newTask}
            onChange={e => setNewTask(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && spawnAgent()}
          />
          <button className="btn btn-primary" onClick={spawnAgent} disabled={creating}>
            {creating ? 'Creating...' : 'Spawn Agent'}
          </button>
        </div>
      </div>

      <div className="card">
        <h3>Active Agents</h3>
        <table className="table">
          <thead>
            <tr>
              <th>ID</th>
              <th>Status</th>
              <th>Task</th>
              <th>Tokens</th>
              <th>Cost</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {agents.length === 0 && (
              <tr><td colSpan="6" style={{ textAlign: 'center', color: '#666', padding: 32 }}>
                No agents running. Create one above.
              </td></tr>
            )}
            {agents.map(a => (
              <tr key={a.id}>
                <td style={{ fontFamily: 'monospace' }}>{a.id.substring(0, 8)}</td>
                <td>
                  <span className={`badge badge-${a.status === 'active' ? 'green' : a.status === 'paused' ? 'yellow' : 'gray'}`}>
                    {a.status}
                  </span>
                </td>
                <td>{a.task?.substring(0, 50)}</td>
                <td>{a.tokens || 0}</td>
                <td>${(a.cost || 0).toFixed(4)}</td>
                <td>
                  <button className="btn btn-ghost btn-sm" onClick={() => killAgent(a.id)}>Kill</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
