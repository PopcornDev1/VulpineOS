import React, { useState, useEffect } from 'react'

export default function Contexts({ ws }) {
  const [contexts, setContexts] = useState([])

  useEffect(() => {
    if (ws.connected) {
      // Get targets to show active contexts
      ws.send('Target.getBrowserContexts').then(r => {
        const ctxs = r?.result?.browserContextIds || []
        setContexts(ctxs.map(id => ({ id, pages: 0 })))
      }).catch(() => {})
    }
  }, [ws.connected])

  const createContext = async () => {
    try {
      const r = await ws.send('Browser.createBrowserContext', { removeOnDetach: true })
      if (r?.result?.browserContextId) {
        setContexts(prev => [...prev, { id: r.result.browserContextId, pages: 0 }])
      }
    } catch (e) {
      alert('Failed: ' + e.message)
    }
  }

  const removeContext = async (id) => {
    try {
      await ws.send('Browser.removeBrowserContext', { browserContextId: id })
      setContexts(prev => prev.filter(c => c.id !== id))
    } catch (e) {
      alert('Failed: ' + e.message)
    }
  }

  return (
    <div>
      <div className="page-header">
        <h1>Browser Contexts</h1>
        <button className="btn btn-primary" onClick={createContext}>New Context</button>
      </div>

      <div className="card">
        <h3>Active Contexts</h3>
        <table className="table">
          <thead>
            <tr>
              <th>Context ID</th>
              <th>Pages</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {contexts.length === 0 && (
              <tr><td colSpan="3" style={{ textAlign: 'center', color: '#666', padding: 32 }}>
                No browser contexts. The default context is always available.
              </td></tr>
            )}
            {contexts.map(c => (
              <tr key={c.id}>
                <td style={{ fontFamily: 'monospace' }}>{c.id.substring(0, 16)}...</td>
                <td>{c.pages}</td>
                <td>
                  <button className="btn btn-ghost btn-sm" onClick={() => removeContext(c.id)}>Remove</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
