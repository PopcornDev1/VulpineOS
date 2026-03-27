import React, { useState, useEffect } from 'react'

export default function Dashboard({ ws }) {
  const [info, setInfo] = useState(null)

  useEffect(() => {
    if (ws.connected) {
      ws.send('Browser.getInfo').then(r => setInfo(r?.result)).catch(() => {})
    }
  }, [ws.connected])

  const telemetry = ws.status || {}
  const recentEvents = ws.events.slice(-10).reverse()

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
          <div className="stat-value">{telemetry.memoryMB ? `${telemetry.memoryMB}MB` : '—'}</div>
          <div className="stat-label">Memory Usage</div>
        </div>
        <div className="card">
          <h3>Contexts</h3>
          <div className="stat-value">{telemetry.contexts || 0}</div>
          <div className="stat-label">Active Browser Contexts</div>
        </div>
        <div className="card">
          <h3>Pages</h3>
          <div className="stat-value">{telemetry.pages || 0}</div>
          <div className="stat-label">Open Pages</div>
        </div>
        <div className="card">
          <h3>Risk</h3>
          <div className="stat-value" style={{ color: (telemetry.detectionRisk || 0) > 50 ? '#ef4444' : '#22c55e' }}>
            {telemetry.detectionRisk || 0}%
          </div>
          <div className="stat-label">Detection Risk</div>
        </div>
      </div>

      <div className="grid grid-2">
        <div className="card">
          <h3>Browser Info</h3>
          {info ? (
            <div style={{ fontSize: 13, color: '#aaa' }}>
              <p>Version: {info.version || 'Unknown'}</p>
              <p>User Agent: {(info.userAgent || '').substring(0, 60)}...</p>
            </div>
          ) : (
            <p style={{ color: '#666' }}>Loading...</p>
          )}
        </div>

        <div className="card">
          <h3>Recent Events</h3>
          <div className="event-log">
            {recentEvents.length === 0 && <p style={{ color: '#666' }}>No events yet</p>}
            {recentEvents.map((ev, i) => (
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
