import React, { useState } from 'react'
import { Routes, Route, Link, useLocation } from 'react-router-dom'
import { useWebSocket } from './hooks/useWebSocket'
import Dashboard from './pages/Dashboard'
import Agents from './pages/Agents'
import AgentDetail from './pages/AgentDetail'
import Contexts from './pages/Contexts'
import Proxies from './pages/Proxies'
import Settings from './pages/Settings'
import Logs from './pages/Logs'
import Security from './pages/Security'
import Webhooks from './pages/Webhooks'
import Scripts from './pages/Scripts'
import Login from './pages/Login'
import './App.css'

export default function App() {
  const [apiKey, setApiKey] = useState(localStorage.getItem('vulpine_key') || '')
  const ws = useWebSocket(apiKey)
  const location = useLocation()

  if (!apiKey) {
    return <Login onLogin={(key) => { localStorage.setItem('vulpine_key', key); setApiKey(key) }} />
  }

  const nav = [
    { path: '/', label: 'Dashboard', icon: '📊' },
    { path: '/agents', label: 'Agents', icon: '🤖' },
    { path: '/contexts', label: 'Contexts', icon: '🔲' },
    { path: '/proxies', label: 'Proxies', icon: '🌐' },
    { path: '/security', label: 'Security', icon: '🛡️' },
    { path: '/webhooks', label: 'Webhooks', icon: '🔔' },
    { path: '/scripts', label: 'Scripts', icon: '📜' },
    { path: '/logs', label: 'Logs', icon: '📋' },
    { path: '/settings', label: 'Settings', icon: '⚙️' },
  ]

  return (
    <div className="app">
      <nav className="sidebar">
        <div className="logo">
          <h2>VulpineOS</h2>
          <span className={`status-dot ${ws.connected ? 'connected' : 'disconnected'}`} />
        </div>
        {nav.map(n => (
          <Link key={n.path} to={n.path} className={`nav-item ${location.pathname === n.path ? 'active' : ''}`}>
            <span className="nav-icon">{n.icon}</span>
            <span>{n.label}</span>
          </Link>
        ))}
        <div className="nav-spacer" />
        <button className="nav-item logout" onClick={() => { localStorage.removeItem('vulpine_key'); setApiKey('') }}>
          <span className="nav-icon">🚪</span>
          <span>Logout</span>
        </button>
      </nav>
      <main className="content">
        <Routes>
          <Route path="/" element={<Dashboard ws={ws} />} />
          <Route path="/agents" element={<Agents ws={ws} />} />
          <Route path="/agents/:id" element={<AgentDetail ws={ws} />} />
          <Route path="/contexts" element={<Contexts ws={ws} />} />
          <Route path="/proxies" element={<Proxies ws={ws} />} />
          <Route path="/security" element={<Security ws={ws} />} />
          <Route path="/webhooks" element={<Webhooks ws={ws} />} />
          <Route path="/scripts" element={<Scripts ws={ws} />} />
          <Route path="/logs" element={<Logs ws={ws} />} />
          <Route path="/settings" element={<Settings ws={ws} />} />
        </Routes>
      </main>
    </div>
  )
}
