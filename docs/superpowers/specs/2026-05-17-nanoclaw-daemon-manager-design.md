# NanoClaw Daemon Manager Design

## Problem
VulpineOS currently starts the NanoClaw **Gateway** (`nanoclaw gateway run`) but does not start the main **Daemon** (`src/index.ts`). The Daemon creates the Unix socket (`cli.sock`) used for agent communication. Without the Daemon, agents hang indefinitely because `spawnViaSocket` cannot find the socket.

## Solution
Add a `Daemon` manager to Vulpine that starts the NanoClaw Daemon process on Vulpine startup, waits for the socket to appear, and manages its lifecycle.

## Architecture

### New File: `internal/nanoclaw/daemon.go`
```go
type Daemon struct {
    binary     string      // Path to NanoClaw executable
    cmd        *exec.Cmd   // Running process
    socketPath string      // Path to cli.sock
    mu         sync.Mutex
    exited     bool
    exitCh     chan error
}
```

### Methods
*   `Start() error`:
    1. Finds Nanoclaw binary using `findNanoclaw()`.
    2. Constructs command: `pnpm exec tsx src/index.ts --profile vulpine`.
    3. Injects `OPENCODE_MODEL` and `OPENCODE_PROVIDER` env vars if OpenRouter is configured.
    4. Starts the process in the background.
    5. Polls for `cli.sock` to appear (max 15s timeout).
    6. Returns success once socket is ready.
*   `Stop() error`: Sends SIGTERM to the daemon process and waits for exit.

### Integration
*   **`cmd/vulpineos/main.go`:**
    *   Add `daemon.Start()` call after kernel initialization (before Gateway).
    *   Add `daemon.Stop()` to the shutdown sequence.
*   **`internal/nanoclaw/manager.go`:**
    *   No changes needed. `SpawnWithSessionIsolated` will now find the socket because the daemon is guaranteed to be running.

## Error Handling
*   **Startup Failure:** If the daemon fails to start, log a warning and continue. Vulpine can still run, but agents will fail with a clear "NanoClaw daemon not running" error.
*   **Crash Recovery:** Log daemon crashes. (Auto-restart can be added later if needed).

## Success Criteria
1. Vulpine starts the NanoClaw Daemon automatically on launch.
2. `cli.sock` exists within 15 seconds of Vulpine startup.
3. Agents spawn successfully and communicate via the socket.
4. Daemon shuts down cleanly when Vulpine exits.
