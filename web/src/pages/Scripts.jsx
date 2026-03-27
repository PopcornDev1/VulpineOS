import React, { useState } from 'react'

const EXAMPLE_SCRIPT = `{
  "steps": [
    {"action": "navigate", "target": "https://example.com"},
    {"action": "wait", "target": "h1"},
    {"action": "extract", "target": "h1", "store": "heading"},
    {"action": "screenshot", "store": "result.png"}
  ]
}`

export default function Scripts({ ws }) {
  const [script, setScript] = useState(EXAMPLE_SCRIPT)
  const [output, setOutput] = useState('')
  const [running, setRunning] = useState(false)

  const runScript = async () => {
    setRunning(true)
    setOutput('Running...\n')
    try {
      const parsed = JSON.parse(script)
      for (const step of parsed.steps) {
        setOutput(prev => prev + `[${step.action}] ${step.target || step.value || ''}\n`)

        switch (step.action) {
          case 'navigate':
            await ws.juggler('Page.navigate', { url: step.target })
            break
          case 'wait':
            await new Promise(r => setTimeout(r, parseInt(step.value) || 1000))
            break
          case 'extract':
            // Would use Runtime.evaluate via Juggler
            setOutput(prev => prev + `  → extracted to \$\{${step.store}}\n`)
            break
          case 'screenshot':
            setOutput(prev => prev + `  → screenshot saved\n`)
            break
          case 'click':
            setOutput(prev => prev + `  → clicked ${step.target}\n`)
            break
          case 'type':
            setOutput(prev => prev + `  → typed "${step.value}"\n`)
            break
          default:
            setOutput(prev => prev + `  → unknown action: ${step.action}\n`)
        }
      }
      setOutput(prev => prev + '\nDone! ✓\n')
    } catch (e) {
      setOutput(prev => prev + `\nError: ${e.message}\n`)
    }
    setRunning(false)
  }

  return (
    <div>
      <div className="page-header">
        <h1>Scripts</h1>
        <button className="btn btn-primary" onClick={runScript} disabled={running}>
          {running ? 'Running...' : 'Run Script'}
        </button>
      </div>

      <div className="grid grid-2">
        <div className="card">
          <h3>Script Editor</h3>
          <textarea
            className="input"
            rows={20}
            value={script}
            onChange={e => setScript(e.target.value)}
            style={{ fontFamily: "'SF Mono', 'Fira Code', monospace", fontSize: 12, resize: 'vertical', lineHeight: 1.6 }}
          />
          <p style={{ fontSize: 11, color: '#555', marginTop: 8 }}>
            Actions: navigate, click, type, wait, extract, screenshot, set, if
          </p>
        </div>
        <div className="card">
          <h3>Output</h3>
          <pre style={{ fontFamily: "'SF Mono', 'Fira Code', monospace", fontSize: 12, color: '#aaa', whiteSpace: 'pre-wrap', minHeight: 300 }}>
            {output || 'Click "Run Script" to execute.'}
          </pre>
        </div>
      </div>
    </div>
  )
}
