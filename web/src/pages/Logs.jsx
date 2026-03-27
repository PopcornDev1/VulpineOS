import React, { useState } from 'react'

export default function Logs({ ws }) {
  const [filter, setFilter] = useState('')
  const events = ws.events || []

  const filtered = filter
    ? events.filter(e => e.method.toLowerCase().includes(filter.toLowerCase()))
    : events

  return (
    <div>
      <div className="page-header">
        <h1>Event Log</h1>
        <input
          className="input"
          style={{ width: 250 }}
          placeholder="Filter events..."
          value={filter}
          onChange={e => setFilter(e.target.value)}
        />
      </div>

      <div className="card">
        <h3>Events ({filtered.length})</h3>
        <div className="event-log" style={{ maxHeight: 600 }}>
          {filtered.length === 0 && <p style={{ color: '#666' }}>No events. Events appear as the kernel operates.</p>}
          {[...filtered].reverse().map((ev, i) => (
            <div key={i} className="event" style={{ display: 'flex', gap: 12 }}>
              <span className="event-time" style={{ minWidth: 80 }}>
                {new Date(ev.ts).toLocaleTimeString()}
              </span>
              <span className="event-method" style={{ minWidth: 200 }}>{ev.method}</span>
              <span style={{ color: '#555', fontSize: 12, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {ev.params ? JSON.stringify(ev.params).substring(0, 100) : ''}
              </span>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
