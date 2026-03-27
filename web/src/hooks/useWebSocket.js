import { useState, useEffect, useRef, useCallback } from 'react'

// WebSocket hook for communicating with VulpineOS serve mode
export function useWebSocket(apiKey) {
  const [connected, setConnected] = useState(false)
  const [status, setStatus] = useState(null)
  const [agents, setAgents] = useState([])
  const [events, setEvents] = useState([])
  const wsRef = useRef(null)
  const idRef = useRef(1)
  const pendingRef = useRef({})

  useEffect(() => {
    if (!apiKey) return

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/ws?token=${apiKey}`
    const ws = new WebSocket(wsUrl)
    wsRef.current = ws

    ws.onopen = () => setConnected(true)
    ws.onclose = () => { setConnected(false); wsRef.current = null }
    ws.onerror = () => setConnected(false)

    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data)

        // Response to a request
        if (msg.id && pendingRef.current[msg.id]) {
          pendingRef.current[msg.id](msg)
          delete pendingRef.current[msg.id]
          return
        }

        // Event from server
        if (msg.method) {
          setEvents(prev => [...prev.slice(-99), { method: msg.method, params: msg.params, ts: Date.now() }])

          if (msg.method === 'Browser.telemetryUpdate') {
            setStatus(msg.params)
          }
        }
      } catch {}
    }

    return () => { ws.close(); wsRef.current = null }
  }, [apiKey])

  const send = useCallback((method, params = {}) => {
    return new Promise((resolve, reject) => {
      if (!wsRef.current || wsRef.current.readyState !== 1) {
        reject(new Error('Not connected'))
        return
      }
      const id = idRef.current++
      pendingRef.current[id] = resolve
      wsRef.current.send(JSON.stringify({ id, method, params }))
      setTimeout(() => {
        if (pendingRef.current[id]) {
          delete pendingRef.current[id]
          reject(new Error('Timeout'))
        }
      }, 30000)
    })
  }, [])

  return { connected, status, agents, events, send }
}
