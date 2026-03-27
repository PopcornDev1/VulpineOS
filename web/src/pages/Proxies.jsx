import React, { useState } from 'react'

export default function Proxies({ ws }) {
  const [proxies, setProxies] = useState([])
  const [importText, setImportText] = useState('')
  const [showImport, setShowImport] = useState(false)

  const importProxies = () => {
    const lines = importText.split('\n').filter(l => l.trim())
    const newProxies = lines.map((url, i) => ({
      id: `proxy-${Date.now()}-${i}`,
      url: url.trim(),
      status: 'untested',
      latency: null,
      geo: null,
    }))
    setProxies(prev => [...prev, ...newProxies])
    setImportText('')
    setShowImport(false)
  }

  const testProxy = async (id) => {
    setProxies(prev => prev.map(p => p.id === id ? { ...p, status: 'testing' } : p))
    // In production, this would call the server to test through the proxy
    setTimeout(() => {
      setProxies(prev => prev.map(p =>
        p.id === id ? { ...p, status: 'active', latency: Math.floor(Math.random() * 200) + 50 } : p
      ))
    }, 1000)
  }

  const deleteProxy = (id) => {
    setProxies(prev => prev.filter(p => p.id !== id))
  }

  return (
    <div>
      <div className="page-header">
        <h1>Proxies</h1>
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="btn btn-ghost" onClick={() => setShowImport(!showImport)}>
            {showImport ? 'Cancel' : 'Import'}
          </button>
        </div>
      </div>

      {showImport && (
        <div className="card">
          <h3>Import Proxies</h3>
          <p style={{ fontSize: 13, color: '#666', marginBottom: 12 }}>One proxy URL per line (http://user:pass@host:port)</p>
          <textarea
            className="input"
            rows={5}
            value={importText}
            onChange={e => setImportText(e.target.value)}
            placeholder="http://user:pass@proxy1:8080&#10;http://user:pass@proxy2:8080"
            style={{ resize: 'vertical', fontFamily: 'monospace', fontSize: 12 }}
          />
          <button className="btn btn-primary" style={{ marginTop: 12 }} onClick={importProxies}>
            Import {importText.split('\n').filter(l => l.trim()).length} Proxies
          </button>
        </div>
      )}

      <div className="card">
        <h3>Proxy Pool ({proxies.length})</h3>
        <table className="table">
          <thead>
            <tr>
              <th>URL</th>
              <th>Status</th>
              <th>Latency</th>
              <th>Geo</th>
              <th>Actions</th>
            </tr>
          </thead>
          <tbody>
            {proxies.length === 0 && (
              <tr><td colSpan="5" style={{ textAlign: 'center', color: '#666', padding: 32 }}>
                No proxies. Click Import to add proxy URLs.
              </td></tr>
            )}
            {proxies.map(p => (
              <tr key={p.id}>
                <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{p.url.substring(0, 40)}...</td>
                <td>
                  <span className={`badge badge-${p.status === 'active' ? 'green' : p.status === 'testing' ? 'yellow' : 'gray'}`}>
                    {p.status}
                  </span>
                </td>
                <td>{p.latency ? `${p.latency}ms` : '—'}</td>
                <td>{p.geo || '—'}</td>
                <td>
                  <button className="btn btn-ghost btn-sm" onClick={() => testProxy(p.id)} style={{ marginRight: 4 }}>Test</button>
                  <button className="btn btn-ghost btn-sm" onClick={() => deleteProxy(p.id)}>Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
