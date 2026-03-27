import React from 'react'

export default function Security({ ws }) {
  const injectionEvents = ws.events.filter(e => e.method === 'Browser.injectionAttemptDetected').slice(-20).reverse()

  return (
    <div>
      <div className="page-header"><h1>Security</h1></div>

      <div className="grid grid-2">
        <div className="card">
          <h3>Active Protections</h3>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {[
              { name: 'Injection-Proof AX Filter', desc: 'Strips hidden DOM nodes before AI reads the page', active: true },
              { name: 'Action-Lock', desc: 'Freezes page while agent thinks — no JS/timers/reflows', active: true },
              { name: 'CSP Header Injection', desc: 'Blocks inline scripts that could inject prompts', active: true },
              { name: 'DOM Mutation Monitor', desc: 'Detects elements injected after page load', active: true },
              { name: 'Injection Signature Scanner', desc: '13 patterns: role hijack, instruction override, invisible text', active: true },
              { name: 'Sandboxed JS Evaluation', desc: 'Blocks fetch/XHR/WebSocket in agent evaluations', active: true },
              { name: 'Token-Optimized DOM', desc: '>50% token reduction via role codes and compression', active: true },
            ].map(p => (
              <div key={p.name} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid #1e1e2e' }}>
                <div>
                  <div style={{ fontSize: 14, color: '#e0e0e8' }}>{p.name}</div>
                  <div style={{ fontSize: 12, color: '#666' }}>{p.desc}</div>
                </div>
                <span className="badge badge-green">Active</span>
              </div>
            ))}
          </div>
        </div>

        <div className="card">
          <h3>Injection Attempts ({injectionEvents.length})</h3>
          <div className="event-log" style={{ maxHeight: 400 }}>
            {injectionEvents.length === 0 && <p style={{ color: '#666' }}>No injection attempts detected.</p>}
            {injectionEvents.map((ev, i) => (
              <div key={i} className="event">
                <span className="event-time">{new Date(ev.ts).toLocaleTimeString()} </span>
                <span style={{ color: '#ef4444' }}>INJECTION </span>
                <span style={{ color: '#888', fontSize: 12 }}>{JSON.stringify(ev.params).substring(0, 80)}</span>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
