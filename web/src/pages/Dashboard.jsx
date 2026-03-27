import React, { useState, useEffect } from 'react'

export default function Dashboard({ ws }) {
  const [status, setStatus] = useState(null)

  useEffect(() => {
    if (!ws.connected) return
    ws.call('status.get').then(r => setStatus(r)).catch(() => {})
    const interval = setInterval(() => {
      ws.call('status.get').then(r => setStatus(r)).catch(() => {})
    }, 5000)
    return () => clearInterval(interval)
  }, [ws.connected])

  const t = ws.telemetry || {}
  const s = status || {}
  const recent = ws.events.slice(-15).reverse()

  return (
    <div>
      <div className="page-header">
        <h1>Dashboard</h1>
        <span className={`badge ${ws.connected ? 'badge-green' : 'badge-red'}`}>
          {ws.connected ? 'Connected' : 'Disconnected'}
        </span>
      </div>

      <div className="grid grid-4">
        <div className="card">
          <h3>Kernel</h3>
          <div className="stat-value">{s.kernel_running ? '●' : '○'}</div>
          <div className="stat-label">PID {s.kernel_pid || '—'} · {t.memoryMB ? `${t.memoryMB}MB` : '—'}</div>
        </div>
        <div className="card">
          <h3>Agents</h3>
          <div className="stat-value">{s.active_agents || 0}</div>
          <div className="stat-label">Active</div>
        </div>
        <div className="card">
          <h3>Pool</h3>
          <div className="stat-value">{s.pool_active || 0}/{s.pool_total || 0}</div>
          <div className="stat-label">{s.pool_available || 0} available</div>
        </div>
        <div className="card">
          <h3>Cost</h3>
          <div className="stat-value">${(s.total_cost_usd || 0).toFixed(4)}</div>
          <div className="stat-label">Total spend</div>
        </div>
      </div>

      <div className="grid grid-2">
        <div className="card">
          <h3>Telemetry</h3>
          <div style={{ fontSize: 13, color: '#aaa', lineHeight: 1.8 }}>
            <div>Contexts: {t.contexts || 0} · Pages: {t.pages || 0}</div>
            <div>Detection Risk: <span style={{ color: (t.detectionRisk || 0) > 50 ? '#ef4444' : '#22c55e' }}>{t.detectionRisk || 0}%</span></div>
            <div>Citizens: {s.total_citizens || 0} · Templates: {s.total_templates || 0}</div>
          </div>
        </div>
        <div className="card">
          <h3>Recent Events</h3>
          <div className="event-log">
            {recent.length === 0 && <p style={{ color: '#666' }}>Waiting for events...</p>}
            {recent.map((ev, i) => (
              <div key={i} className="event">
                <span className="event-time">{new Date(ev.ts).toLocaleTimeString()} </span>
                <span className="event-method">{ev.method}</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
