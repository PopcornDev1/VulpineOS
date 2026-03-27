import React, { useState } from 'react'

export default function Login({ onLogin }) {
  const [key, setKey] = useState('')
  const [error, setError] = useState('')

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (!key.trim()) { setError('API key required'); return }

    // Test the connection
    try {
      const res = await fetch('/health')
      if (res.ok) {
        onLogin(key.trim())
      } else {
        setError('Server not reachable')
      }
    } catch {
      onLogin(key.trim()) // Try anyway — server might require WS auth only
    }
  }

  return (
    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'center', minHeight: '100vh' }}>
      <div className="card" style={{ width: 400 }}>
        <h2 style={{ color: '#a78bfa', marginBottom: 8 }}>VulpineOS</h2>
        <p style={{ color: '#666', fontSize: 14, marginBottom: 24 }}>Enter your API key to connect</p>
        <form onSubmit={handleSubmit}>
          <input
            className="input"
            type="password"
            placeholder="API Key"
            value={key}
            onChange={e => { setKey(e.target.value); setError('') }}
            autoFocus
          />
          {error && <p style={{ color: '#ef4444', fontSize: 13, marginTop: 8 }}>{error}</p>}
          <button className="btn btn-primary" type="submit" style={{ width: '100%', marginTop: 16 }}>
            Connect
          </button>
        </form>
      </div>
    </div>
  )
}
