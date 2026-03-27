import React, { useState, useEffect } from 'react'
import { useParams, Link } from 'react-router-dom'

export default function AgentDetail({ ws }) {
  const { id } = useParams()
  const [messages, setMessages] = useState([])
  const [timeline, setTimeline] = useState([])
  const [fingerprint, setFingerprint] = useState(null)
  const [input, setInput] = useState('')
  const [tab, setTab] = useState('conversation')

  useEffect(() => {
    if (!ws.connected || !id) return
    ws.call('agents.getMessages', { agentId: id }).then(r => setMessages(r?.messages || [])).catch(() => {})
    ws.call('recording.getTimeline', { agentId: id }).then(r => setTimeline(r?.actions || [])).catch(() => {})
    ws.call('fingerprints.get', { agentId: id }).then(r => setFingerprint(r)).catch(() => {})
  }, [ws.connected, id])

  const sendMessage = async () => {
    if (!input.trim()) return
    try {
      await ws.call('agents.resume', { agentId: id, message: input })
      setInput('')
      setTimeout(() => {
        ws.call('agents.getMessages', { agentId: id }).then(r => setMessages(r?.messages || [])).catch(() => {})
      }, 2000)
    } catch (e) { alert(e.message) }
  }

  return (
    <div>
      <div className="page-header">
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <Link to="/agents" style={{ color: '#666', textDecoration: 'none' }}>← Agents</Link>
          <h1>Agent {id.substring(0, 12)}</h1>
        </div>
      </div>

      <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
        {['conversation', 'recording', 'fingerprint'].map(t => (
          <button key={t} className={`btn ${tab === t ? 'btn-primary' : 'btn-ghost'}`} onClick={() => setTab(t)}>
            {t.charAt(0).toUpperCase() + t.slice(1)}
          </button>
        ))}
      </div>

      {tab === 'conversation' && (
        <div className="card">
          <h3>Conversation</h3>
          <div style={{ maxHeight: 500, overflowY: 'auto', marginBottom: 12 }}>
            {messages.length === 0 && <p style={{ color: '#666' }}>No messages yet.</p>}
            {messages.map((m, i) => (
              <div key={i} style={{ padding: '8px 0', borderBottom: '1px solid #1e1e2e' }}>
                <span style={{ color: m.role === 'user' ? '#60a5fa' : m.role === 'assistant' ? '#a78bfa' : '#666', fontWeight: 600, fontSize: 12 }}>
                  {m.role?.toUpperCase()}
                </span>
                <div style={{ fontSize: 13, color: '#ccc', marginTop: 4, whiteSpace: 'pre-wrap' }}>{m.content}</div>
              </div>
            ))}
          </div>
          <div style={{ display: 'flex', gap: 8 }}>
            <input className="input" value={input} onChange={e => setInput(e.target.value)}
              placeholder="Send message to agent..." onKeyDown={e => e.key === 'Enter' && sendMessage()} />
            <button className="btn btn-primary" onClick={sendMessage}>Send</button>
          </div>
        </div>
      )}

      {tab === 'recording' && (
        <div className="card">
          <h3>Action Timeline</h3>
          <div style={{ fontFamily: 'monospace', fontSize: 12, lineHeight: 1.8 }}>
            {timeline.length === 0 && <p style={{ color: '#666' }}>No recorded actions.</p>}
            {timeline.map((a, i) => (
              <div key={i} style={{ color: '#aaa' }}>
                <span style={{ color: '#666' }}>[{new Date(a.timestamp).toLocaleTimeString()}]</span>{' '}
                <span style={{ color: '#a78bfa' }}>{a.type?.toUpperCase()}</span>{' '}
                {a.data && <span>{JSON.stringify(a.data).substring(0, 60)}</span>}
              </div>
            ))}
          </div>
        </div>
      )}

      {tab === 'fingerprint' && (
        <div className="card">
          <h3>Fingerprint</h3>
          {fingerprint ? (
            <pre style={{ fontSize: 12, color: '#aaa', overflow: 'auto', maxHeight: 400 }}>
              {JSON.stringify(fingerprint, null, 2)}
            </pre>
          ) : (
            <p style={{ color: '#666' }}>No fingerprint assigned.</p>
          )}
        </div>
      )}
    </div>
  )
}
