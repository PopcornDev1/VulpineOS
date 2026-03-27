import { useState, useEffect, useRef, useCallback } from 'react'

export function useWebSocket(apiKey) {
  const [connected, setConnected] = useState(false)
  const [events, setEvents] = useState([])
  const [telemetry, setTelemetry] = useState({})
  const wsRef = useRef(null)
  const idRef = useRef(1)
  const pendingRef = useRef({})

  useEffect(() => {
    if (!apiKey) return
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const ws = new WebSocket(`${protocol}//${window.location.host}/ws?token=${apiKey}`)
    wsRef.current = ws

    ws.onopen = () => setConnected(true)
    ws.onclose = () => { setConnected(false); wsRef.current = null }
    ws.onerror = () => setConnected(false)

    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data)
        // Control response
        if (msg.type === 'control') {
          const payload = JSON.parse(msg.payload || '{}')
          if (payload.id && pendingRef.current[payload.id]) {
            pendingRef.current[payload.id](payload)
            delete pendingRef.current[payload.id]
          }
          return
        }
        // Juggler event
        if (msg.type === 'juggler' || msg.method) {
          const method = msg.method || ''
          const params = msg.params || {}
          setEvents(prev => [...prev.slice(-199), { method, params, ts: Date.now() }])
          if (method === 'Browser.telemetryUpdate') setTelemetry(params)
        }
      } catch {}
    }
    return () => { ws.close(); wsRef.current = null }
  }, [apiKey])

  // Send a control command to the PanelAPI
  const call = useCallback((method, params = {}) => {
    return new Promise((resolve, reject) => {
      if (!wsRef.current || wsRef.current.readyState !== 1) {
        reject(new Error('Not connected')); return
      }
      const id = idRef.current++
      // The server expects a control envelope: {"type":"control","payload":"{\"command\":\"...\",\"params\":{}}"}
      const controlPayload = JSON.stringify({ command: method, params: JSON.stringify(params), id })
      const envelope = JSON.stringify({ type: 'control', payload: controlPayload })

      pendingRef.current[id] = (resp) => {
        if (resp.error) reject(new Error(resp.error))
        else resolve(resp)
      }
      wsRef.current.send(envelope)
      setTimeout(() => {
        if (pendingRef.current[id]) {
          delete pendingRef.current[id]
          reject(new Error('Timeout'))
        }
      }, 30000)
    })
  }, [])

  // Send a raw Juggler command
  const juggler = useCallback((method, params = {}) => {
    return new Promise((resolve, reject) => {
      if (!wsRef.current || wsRef.current.readyState !== 1) {
        reject(new Error('Not connected')); return
      }
      const id = idRef.current++
      const jugglerMsg = JSON.stringify({ type: 'juggler', payload: JSON.stringify({ id, method, params }) })
      pendingRef.current[id] = resolve
      wsRef.current.send(jugglerMsg)
      setTimeout(() => {
        if (pendingRef.current[id]) { delete pendingRef.current[id]; reject(new Error('Timeout')) }
      }, 30000)
    })
  }, [])

  return { connected, telemetry, events, call, juggler }
}
