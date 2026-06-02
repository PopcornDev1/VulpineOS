# OpenCode Local Provider for VulpineOS

## Overview

Add support for using a local OpenCode instance as an AI provider in VulpineOS, enabling agents to use OpenCode's persistent session context without API keys.

## Architecture

```
VulpineOS Agent → OpenCodeClient → opencode serve (HTTP) → OpenCode Server
                                                      ↓
                                              Persistent session context
```

## Components

### 1. Provider Configuration (`internal/config/config.go`)

Add new provider:
```go
{ID: "opencode-local", Name: "OpenCode (Local)", EnvVar: "",
 DefaultModel: "opencode/local",
 Models:       []string{"opencode/local"},
 NeedsKey:     false},
```

Add config field for binary path (default: `opencode`).

### 2. OpenCode Client (`internal/opencode/client.go`)

New package with:
- `Client` struct - manages background server and HTTP communication
- `StartServer()` - spawns `opencode serve` as subprocess, waits for ready
- `SendMessage(prompt string) (string, int, error)` - sends prompt, returns response + token count
- `Close()` - terminates server process

### 3. Agent Integration (`internal/openclaw/agent.go`)

Modify `NewAgent()` to detect `opencode-local` provider and use OpenCodeClient instead of `openclaw` binary.

### 4. Runtime Config (`internal/openclaw/runtime_config.go`)

Update `BuildEnv()` to include OPENCODE_BINARY_PATH when using opencode-local.

## API Contract

### Server Startup
```bash
opencode serve --port 0  # random available port
```

### HTTP Requests
```json
POST /api/message
{"message": "prompt text", "sessionId": "optional-session-id"}
```

### HTTP Responses (JSON-lines)
```json
{"type":"text","part":{"text":"response content"}}
{"type":"step_finish","part":{"tokens":{"total":1234,"input":1000,"output":234}}}
```

## Implementation Steps

1. Add provider to config.go
2. Create internal/opencode/client.go with server management
3. Integrate client into openclaw agent
4. Update runtime config for binary path
5. Add tests

## Error Handling

- Server not running → auto-start on first request
- Server crash → log error, return failure
- Timeout → 5 minute default
- Port conflict → retry with new port

## Testing

- Unit tests for client parsing
- Integration test with live server
- Agent lifecycle test (start, prompt, stop)