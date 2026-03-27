import React, { useState, useEffect } from 'react'

export default function Proxies({ ws }) {
  const [proxies, setProxies] = useState([])
  const [importText, setImportText] = useState('')
  const [showImport, setShowImport] = useState(false)

  const refresh = () => {
    ws.call('proxies.list').then(r => setProxies(r?.proxies || [])).catch(() => {})
  }

  useEffect(() => { if (ws.connected) refresh() }, [ws.connected])

  const importProxies = async () => {
    const urls = importText.split('\n').filter(l => l.trim())
    for (const url of urls) {
      try { await ws.call('proxies.add', { url: url.trim() }) } catch {}
    }
    setImportText('')
    setShowImport(false)
    refresh()
  }

  const testProxy = async (id) => {
    try {
      const r = await ws.call('proxies.test', { proxyId: id })
      alert(`Latency: ${r?.latency || '?'}ms`)
      refresh()
    } catch (e) { alert('Test failed: ' + e.message) }
  }

  const deleteProxy = async (id) => {
    try { await ws.call('proxies.delete', { proxyId: id }); refresh() }
    catch (e) { alert(e.message) }
  }

  return (
    <div>
      <div className="page-header">
        <h1>Proxies ({proxies.length})</h1>
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="btn btn-ghost" onClick={() => setShowImport(!showImport)}>{showImport ? 'Cancel' : 'Import'}</button>
          <button className="btn btn-ghost" onClick={refresh}>↻</button>
        </div>
      </div>

      {showImport && (
        <div className="card">
          <h3>Import Proxies</h3>
          <textarea className="input" rows={5} value={importText} onChange={e => setImportText(e.target.value)}
            placeholder="http://user:pass@proxy1:8080&#10;socks5://proxy2:1080" style={{ resize: 'vertical', fontFamily: 'monospace', fontSize: 12 }} />
          <button className="btn btn-primary" style={{ marginTop: 12 }} onClick={importProxies}>
            Import {importText.split('\n').filter(l => l.trim()).length} Proxies
          </button>
        </div>
      )}

      <div className="card">
        <table className="table">
          <thead><tr><th>URL</th><th>Status</th><th>Latency</th><th>Country</th><th>Actions</th></tr></thead>
          <tbody>
            {proxies.length === 0 && <tr><td colSpan="5" style={{ textAlign: 'center', color: '#666', padding: 32 }}>No proxies.</td></tr>}
            {proxies.map(p => (
              <tr key={p.id || p.url}>
                <td style={{ fontFamily: 'monospace', fontSize: 12 }}>{(p.url || '').substring(0, 45)}</td>
                <td><span className={`badge ${p.latencyMs > 0 ? 'badge-green' : 'badge-gray'}`}>{p.latencyMs > 0 ? 'tested' : 'untested'}</span></td>
                <td>{p.latencyMs > 0 ? `${p.latencyMs}ms` : '—'}</td>
                <td>{p.country || '—'}</td>
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
