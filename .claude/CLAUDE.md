# VulpineOS - Agent Security Runtime

Fork of [CloverLabsAI/camoufox](https://github.com/CloverLabsAI/camoufox) (Firefox 146.0.1-based).
First browser engine with AI agent security built into the C++ core.

## Build System

```bash
make fetch     # Download Firefox 146.0.1 source tarball
make setup     # Extract + init git repo
make dir       # Apply all patches (scripts/patch.py) + copy additions
make build     # Compile with ./mach (use artifact builds on M1 for <5 min)
make package-macos  # Create distributable
```

- `patches/` — unified diffs applied to Firefox source
- `additions/` — new files copied into the Firefox source tree
- `settings/` — preferences (camoufox.cfg) and branding
- `scripts/` — build orchestration (patch.py, copy-additions.sh)

## Architecture: Juggler (Playwright Integration)

The automation layer lives in `additions/juggler/`:
- `protocol/Protocol.js` — defines the Playwright wire protocol
- `protocol/PageHandler.js` — browser-process IPC handlers
- `content/PageAgent.js` — content-process implementation (runs with chrome privilege)

## VulpineOS Features

### Phase 1: Injection-Proof Accessibility Filter (IMPLEMENTED)

**Goal:** Strip non-visible DOM nodes from the accessibility tree before it reaches the AI agent, preventing indirect prompt injection.

**Files modified:**
- `additions/juggler/content/PageAgent.js` — `isNodeVisuallyHidden()` function + `buildNode()` integration
- `settings/camoufox.cfg` — `vulpineos.injection_filter.enabled` preference (default: true)

**How it works:**
The `_getFullAXTree()` method (PageAgent.js) builds a JSON accessibility tree via recursive `buildNode()`. The filter intercepts the child-walk loop, running `isNodeVisuallyHidden()` on each child before recursion. Hidden nodes and their entire subtrees are pruned.

**Visibility checks (ordered by cost, short-circuits early):**
1. `aria-hidden="true"` (DOM attribute)
2. `display: none` (computed style)
3. `visibility: hidden/collapse` (computed style)
4. `opacity: 0` (computed style)
5. Zero dimensions + hidden overflow (bounding rect)
6. Off-screen by >500px (bounding rect)
7. `clip-path: inset(100%)` / `clip: rect(0,0,0,0)` (computed style)

**Toggle:** Set `vulpineos.injection_filter.enabled` to `false` in about:config or via Playwright preferences to disable.

### Phase 2: Deterministic Execution (Action-Lock) (IMPLEMENTED)

**Goal:** Freeze the page completely while the AI agent is "thinking" — no JS, no timers, no layout reflows, no animations, no events. Guarantees the page the agent analyzed is the page it acts on.

**Files modified:**
- `patches/action-lock.patch` — C++ patch adding `suspendPage()`/`resumePage()` to `nsDocShell` (uses Firefox's `nsRefreshDriver::Freeze/Thaw`, `nsPIDOMWindowInner::Suspend/Resume`, `PresShell::SuppressEventHandling`)
- `additions/juggler/protocol/Protocol.js` — `Page.setActionLock` method definition
- `additions/juggler/protocol/PageHandler.js` — handler routing
- `additions/juggler/TargetRegistry.js` — `PageTarget.setActionLock()` IPC bridge
- `additions/juggler/content/main.js` — content-process freeze/thaw logic + `allowJavascript` toggle
- `settings/camoufox.cfg` — `vulpineos.actionlock.enabled` preference (default: true)

**How it works:**
`Page.setActionLock({enabled: true})` → disables JS via `allowJavascript=false` on all frames → calls `docShell.suspendPage()` which freezes the refresh driver (layout/paint/CSS/rAF), suspends timers/intervals/network callbacks, and suppresses event handling. Thaw reverses in opposite order.

**Protocol:** `Page.setActionLock({ enabled: boolean })`
**Toggle:** `vulpineos.actionlock.enabled` pref. Navigation auto-releases the lock.
### Phase 3: Token-Optimized DOM Export (IMPLEMENTED)

**Goal:** Compressed semantic JSON snapshot of the page for LLM context windows, achieving >50% token reduction vs the standard accessibility tree.

**Files modified:**
- `additions/juggler/content/PageAgent.js` — Extracted shared utils (`isNodeVisuallyHidden`, `waitForAXQuiet`) to module level. Added `ROLE_MAP` (50+ role→code mappings), `SKIP_ROLES` set, and `_getOptimizedDOM()` method.
- `additions/juggler/protocol/Protocol.js` — `Page.getOptimizedDOM` method definition
- `additions/juggler/protocol/PageHandler.js` — handler routing
- `settings/camoufox.cfg` — `vulpineos.dom_export.enabled` preference (default: true)

**Output format:** Array-of-tuples `[depth, roleCode, name, props?]`
```json
{"v":1,"title":"...","url":"...","nodes":[[0,"doc","Page"],[1,"h1","Welcome"],[1,"btn","Submit"]]}
```

**Compression strategies:** Short role codes (`heading`→`h2`), skip structural wrappers (section/grouping/paragraph), single-child flattening, adjacent text merging, omit empty/default fields.

**Protocol:** `Page.getOptimizedDOM({ maxDepth?, maxNodes?, maxTextLength? })`
**Toggle:** `vulpineos.dom_export.enabled` pref
### Phase 4: Autonomous Trust-Warming (IMPLEMENTED)

**Goal:** Background service that warms browser profiles on high-authority sites with human-like interactions while the agent is idle, building organic browsing history to defeat bot detection.

**Files:**
- `additions/juggler/TrustWarmService.js` — **NEW** Core service module (~350 lines): state machine, JS port of C++ bezier trajectory algorithm from `MouseTrajectories.hpp`, Gaussian-randomized dwell/scroll/hover/click sequences, visit tracking with rate limiting.
- `additions/juggler/protocol/Protocol.js` — 5 new `Browser.*` methods + `trustWarmingStateChanged` event
- `additions/juggler/protocol/BrowserHandler.js` — Handler wiring + lazy service initialization + dispose cleanup
- `settings/camoufox.cfg` — `vulpineos.trustwarm.enabled` preference (default: false, opt-in)

**Protocol methods:**
- `Browser.startTrustWarming({browserContextId?, sites?, interactionIntensity?, cooldownMinutes?})`
- `Browser.stopTrustWarming()`
- `Browser.getTrustWarmingStatus()` → `{state, sitesWarmed, currentSite?, lastVisit?}`
- `Browser.notifyTrustWarmingIdle()` / `Browser.notifyTrustWarmingBusy()`

**State machine:** `STOPPED → IDLE → WARMING → PAUSING → IDLE`
**Default sites:** Google, YouTube, Wikipedia, Reddit, Amazon, HN, GitHub (weighted, rate-limited)

### Phase 5: Go Kernel Console (M1 IMPLEMENTED — foundation)

**Goal:** Go (Bubbletea) TUI to manage the Firefox kernel, OpenClaw agents, and identity profiles. Speaks Juggler protocol directly over pipe FD 3/4.

**M1 files (Juggler client + kernel process):**
- `go.mod` — module `vulpineos`, Go 1.26
- `cmd/vulpineos/main.go` — CLI entry point, launches kernel, enables Browser protocol, subscribes to events
- `internal/juggler/transport.go` — `PipeTransport` (FD 3/4, null-byte framing matching `nsRemoteDebuggingPipe.cpp`)
- `internal/juggler/client.go` — `Client` with `Call()` (sync RPC) + `Subscribe()` (event handlers), background read loop
- `internal/juggler/messages.go` — `Message` type (request/response/event)
- `internal/juggler/protocol.go` — Domain types (`BrowserInfo`, `TelemetryUpdate`, `InjectionAttempt`, etc.)
- `internal/kernel/process.go` — `Kernel` struct: `Start()`/`Stop()`/`Wait()`, spawns Firefox with pipe FDs, auto-detects binary

**M2 files (Bubbletea TUI dashboard):**
- `internal/tui/shared/styles.go` — Lipgloss theme (violet/cyan/green/amber/red palette)
- `internal/tui/shared/messages.go` — Custom `tea.Msg` types (`KernelStatusMsg`, `TelemetryMsg`, `AlertMsg`, etc.)
- `internal/tui/app.go` — Root model: panel switching (Tab), event routing, Juggler event subscriptions, 2s tick
- `internal/tui/dashboard/model.go` — Kernel status, memory/CPU bars, detection risk gauge, trust warming state
- `internal/tui/contexts/model.go` — Target list table with j/k navigation, session/context/URL columns
- `internal/tui/agents/model.go` — OpenClaw agent table with status/objective/token columns
- `internal/tui/alerts/model.go` — Injection attempt feed (latest 8), blocked/passed indicators
- `internal/tui/statusbar/model.go` — Connection mode, keybind hints
- Binary: 4.8MB with full TUI, `--no-browser` flag for demo mode

**M3 files (Telemetry pipeline):**
- `additions/juggler/TelemetryService.js` — **NEW** Collects memory via `nsIMemoryReporterManager`, counts contexts/pages, manages detection risk score (decays over time, spikes on injection attempts). Emits `telemetryUpdate` every 2s. Also handles `injectionAttemptDetected` events from the Phase 1 filter.
- `additions/juggler/protocol/Protocol.js` — Added `Browser.telemetryUpdate` + `Browser.injectionAttemptDetected` events, `Browser.getTelemetry` + `Browser.getContextTelemetry` methods, `Page.injectionAttemptDetected` event
- `additions/juggler/protocol/BrowserHandler.js` — Auto-starts TelemetryService on `Browser.enable`, added `getTelemetry`/`getContextTelemetry` handlers
- `additions/juggler/protocol/PageHandler.js` — Forwards `injectionAttemptDetected` from content process
- `additions/juggler/content/PageAgent.js` — Modified `_getFullAXTree` to track stripped hidden nodes with text content and emit injection alerts

**M4 files (Remote WebSocket):**
- `internal/remote/server.go` — HTTP/WS server with `/ws` endpoint and `/health` check. Accepts connections, authenticates, relays Juggler messages bidirectionally, broadcasts events to all connected clients.
- `internal/remote/client.go` — WS client implementing `juggler.Transport` interface. `Dial()` connects to remote server with API key auth. Seamlessly replaces pipe transport for the TUI.
- `internal/remote/auth.go` — API key authenticator (constant-time compare). Supports `Authorization: Bearer` header and `?token=` query param.
- `internal/remote/relay.go` — Envelope types for multiplexing juggler/control/tui_state messages over WS.
- `cmd/vulpineos/main.go` — Three modes: `vulpineos` (local), `vulpineos --serve --port 8443 --api-key KEY` (server), `vulpineos --remote wss://host:port/ws --api-key KEY` (client). Server auto-broadcasts all Browser events.
- Binary: 11MB with full TUI + remote connect.

**Upcoming milestones:** M5-M7 (vault + OpenClaw agent management)

### Phase 6: Identity & Agent Vault (M5-M7 IMPLEMENTED)

**M5 — Vault storage layer:**
- `internal/vault/db.go` — SQLite via pure-Go `modernc.org/sqlite`. Auto-creates `~/.vulpineos/vault.db` with WAL mode + foreign keys. Schema: citizens, citizen_cookies, citizen_storage, templates, nomad_sessions.
- `internal/vault/models.go` — Data structs: `Citizen`, `CitizenCookies`, `CitizenStorage`, `Template`, `NomadSession`
- `internal/vault/citizen.go` — Citizen CRUD, cookie/localStorage persistence, usage tracking, detection event counting
- `internal/vault/db_test.go` — Integration test covering full CRUD + cascade delete

**M6 — Templates + Nomad mode:**
- `internal/vault/template.go` — Template CRUD (name, SOP, interaction mode, allowed domains, constraints)
- `internal/vault/nomad.go` — Ephemeral session lifecycle: create → active → completed/failed, auto-cleanup of old sessions

**M7 — OpenClaw agent management:**
- `internal/openclaw/manager.go` — `Manager`: spawn/kill agents, status channel, auto-cleanup on exit
- `internal/openclaw/agent.go` — `Agent`: subprocess lifecycle, JSON-lines stdout reader, status tracking (objective, tokens, status)
- `internal/openclaw/sop.go` — SOP file injection (temp file write/cleanup)

**Cookie persistence flow:** Activate citizen → `Browser.setCookies` → agent runs → deactivate → `Browser.getCookies` → store to vault

### Phase 7: Native OpenClaw Integration (IMPLEMENTED)

**Goal:** Self-contained agent runtime — context pooling for 100+ agents on a single VPS, full orchestrator tying kernel + pool + vault + agents together.

**Files:**
- `internal/pool/pool.go` — Context pool: pre-warm N contexts, acquire/release with channel-based queuing, auto-recycle after M uses, limits concurrent active contexts. Uses `Browser.createBrowserContext`/`removeBrowserContext` via Juggler.
- `internal/orchestrator/orchestrator.go` — `Orchestrator`: ties kernel, pool, vault, and OpenClaw manager. `SpawnCitizen()` loads identity + cookies from vault, acquires context, applies fingerprint, spawns agent. `SpawnNomad()` creates ephemeral session. Auto-releases context slots when agents complete.

**Context pool design:**
- Pre-warm 10 contexts on startup (configurable)
- Max 20 concurrent active contexts
- Recycle after 50 uses to prevent memory leaks
- Buffered channel = ready queue, blocks when at capacity
- Single Firefox process shared across all contexts (~10-15MB per context)

**MCP Bridge (OpenClaw integration):**
- `internal/mcp/protocol.go` — MCP JSON-RPC 2.0 types
- `internal/mcp/tools.go` — 9 browser tools: navigate, snapshot, click, type, screenshot, scroll, new_context, close_context, get_ax_tree
- `internal/mcp/server.go` — Stdio MCP server translating tool calls → Juggler protocol
- `internal/openclaw/config.go` — Generates `openclaw.json` disabling Chromium, routing through VulpineOS MCP
- `scripts/bundle-openclaw.sh` — Downloads Node.js + OpenClaw into self-contained `openclaw/` directory
- CLI mode: `./vulpineos --mcp-server` (4th mode alongside local/serve/remote)
- **Key advantage:** `vulpine_snapshot` uses Phase 3's token-optimized DOM (>50% fewer tokens)

**First-Run Setup Wizard:**
- `internal/config/config.go` — Config system with provider registry (Anthropic, OpenAI, Google, xAI, Ollama). Load/save `~/.vulpineos/config.json`. Auto-generates `openclaw.json` with browser disabled + VulpineOS MCP configured.
- `internal/tui/setup/model.go` — Full-screen Bubbletea setup wizard: provider picker → API key input → model selector → saves config. Runs on first launch before dashboard.
- **31 providers**: Anthropic, OpenAI, Google, xAI, Z.AI, OpenRouter, Groq, Mistral, Together, Cerebras, Moonshot, Kimi, MiniMax, Venice, NVIDIA, Hugging Face, Volcengine, BytePlus, Xiaomi, Qianfan, Model Studio, OpenCode, Kilocode, Vercel, Cloudflare, Synthetic, GitHub Copilot, Ollama, vLLM, SGLang — every OpenClaw-supported provider
- `package.json` in repo root — `npm install` auto-installs OpenClaw as a dependency
- `cmd/vulpineos/main.go` — Checks config on launch, runs setup wizard if `NeedsSetup()`, then launches dashboard
- `c` keybind re-opens setup to change provider/key/model

**Skills Management:**
- `internal/config/config.go` — `GlobalSkills` (for all agents) and `AgentSkills` (per-agent). `AddGlobalSkill()`, `RemoveGlobalSkill()`, `AddAgentSkill()`. Skills config flows into `openclaw.json` via `GenerateOpenClawConfig()`.
- Skill directories: `~/.vulpineos/skills/` (global), `~/.vulpineos/agent-skills/{id}/skills/` (per-agent)
- OpenClaw's `skills.entries` and `skills.load.extraDirs` are auto-populated from VulpineOS config

**Real Agent Spawning:**
- `internal/openclaw/manager.go` — `SpawnOpenClaw(task, agentSkills)` finds OpenClaw binary, spawns `openclaw run --config ~/.vulpineos/openclaw.json --message "task"`. Auto-detects bundled, global, or npx OpenClaw.
- `internal/tui/app.go` — `a` keybind opens task input bar (text input at bottom of screen). User types task description, Enter spawns real OpenClaw agent, Escape cancels. Agent status flows to Agents panel via stdout JSON-lines.

### Phase 8: Vulpine-Box Docker (IMPLEMENTED)

**Goal:** One-click Docker container for the full VulpineOS + OpenClaw environment, accessible remotely via Go TUI.

**Files:**
- `Dockerfile.vulpinebox` — Multi-stage build: Go binary (CGO_ENABLED=0, stripped) + Ubuntu 22.04 runtime with GTK/X11 libs + pre-built Camoufox + Xvfb
- `docker-compose.yml` — Service definition with persistent volumes for vault + profiles, 4GB memory limit, API key via env var
- `scripts/entrypoint.sh` — Starts Xvfb virtual display + launches vulpineos in serve mode with API key and optional TLS
- `.dockerignore` — Excludes source, patches, tests, git history from build context

**Usage:**
```bash
docker compose up -d
vulpineos --remote wss://vps:8443/ws --api-key $VULPINE_API_KEY
```

### TUI Revamp: 3-Column Agent Workbench (IMPLEMENTED)

**Goal:** Replace the 4-panel monitoring dashboard with an agent-centric 3-column workbench. Agents are persistent, pausable/resumable, with conversation history and fingerprint profiles.

**Layout:** Left (system+pool + agent list) | Center (full-height conversation) | Right (agent detail + contexts)

**Data Layer:**
- `internal/vault/db.go` — New `agents` + `agent_messages` tables
- `internal/vault/agent.go` — Full CRUD: CreateAgent, ListAgents, UpdateAgentStatus/Tokens, DeleteAgent, AppendMessage, GetMessages, GetRecentMessages
- `internal/vault/fingerprint.go` — Deterministic fingerprint generation from seed, ParseFingerprint, FingerprintSummary

**Process Management:**
- `internal/openclaw/agent.go` — Stdin pipe for sending messages to running agents, conversation channel for capturing chat output, `/savestate` on pause
- `internal/openclaw/manager.go` — SpawnWithSession (named OpenClaw sessions), ResumeWithSession, PauseAgent, SendMessage, ConversationChan

**TUI Components:**
- `internal/tui/app.go` — Complete rewrite: 3-column layout, agent selection loads vault history, input modes (new-agent-name → new-agent-task → chat), event routing for conversations
- `internal/tui/systeminfo/model.go` — Compact kernel metrics (left sidebar top)
- `internal/tui/agentlist/model.go` — Selectable agent list with status icons ●◌✓⏸✗ (left sidebar bottom)
- `internal/tui/conversation/model.go` — Scrollable chat display with role-colored messages + text input (center)
- `internal/tui/contextlist/model.go` — Active browser contexts (right sidebar top)
- `internal/tui/poolstats/model.go` — Pool utilization (right sidebar bottom)

**Keybinds:** `n` new agent, `j/k` navigate, `Enter` chat, `p` pause, `r` resume, `x` delete, `Tab` focus cycle, `Esc` back, `q` quit

**Agent Lifecycle:** CREATE → ACTIVE → PAUSED → ACTIVE → COMPLETED. Conversations persist in vault. Fingerprints survive across sessions.

### Proxy System + Settings Panel + Rate Limit Detection

**Proxy Management:**
- `internal/proxy/proxy.go` — ProxyConfig/GeoInfo types, ParseProxyURL (http://user:pass@host:port), ParseProxyList (bulk import), ResolveGeo (ip-api.com through proxy), TestProxy (latency measurement)
- `internal/proxy/sync.go` — SyncFingerprintToProxy: injects geolocation, timezone, WebRTC IP, locale into fingerprint JSON based on proxy exit location. Country→locale map (32 countries).
- `internal/vault/proxy.go` — Proxy CRUD in SQLite: AddProxy, ListProxies, GetProxy, UpdateProxyGeo, DeleteProxy
- Per-agent fixed proxy: each agent stores proxy_config in vault. On activation, proxy applied via `Browser.setContextProxy`, fingerprint pre-synced with proxy geo.

**Settings Panel (Shift+S):**
- `internal/tui/settings/model.go` — 3-tab settings panel replacing center column: General (provider/model/key), Proxies (import/test/delete/geo), Skills (toggle global skills)
- Proxy import: paste proxy URLs one per line → bulk import to vault
- Proxy test: measures latency + resolves exit IP through proxy

**Rate Limit Detection:**
- `internal/monitor/ratelimit.go` — Pattern-based monitor scanning agent conversation output for: "429/rate limit" → AlertRateLimit, "captcha/challenge" → AlertCaptcha, "blocked/forbidden" → AlertIPBlock (requires 2+ hits to avoid false positives). Per-agent failure tracking.

**Fingerprint System:**
- `internal/vault/fingerprint.go` — Two-tier: (1) Full Camoufox pipeline via Python subprocess (BrowserForge Bayesian network + WebGL DB with 147 GL params + OS-matched fonts + canvas/audio noise seeds), (2) Fallback with OS-consistent profiles. Locked to host OS to prevent TCP/IP/font cross-OS detection.

**Tests:** 66 tests across 8 packages, all passing with race detector.

### Foxbridge: CDP-to-Firefox Protocol Proxy (COMPLETE)

**Goal:** Replace Chrome/Chromium dependency in OpenClaw. Foxbridge translates Chrome DevTools Protocol (CDP) to Firefox protocols (Juggler + WebDriver BiDi), letting OpenClaw control Camoufox without Chrome.

**Repo:** `PopcornDev1/foxbridge` at `/Users/apple/Code/Personal/foxbridge`
**Status:** Full Puppeteer compatibility with dual backend support (Juggler + BiDi). ~100% CDP method coverage. Integrated into VulpineOS startup.

**Key files:**
- `cmd/foxbridge/main.go` — CLI entry, launches Firefox, enables Browser, starts CDP server
- `pkg/cdp/server.go` — CDP WebSocket server + HTTP discovery endpoints (/json/version, /json/list)
- `pkg/cdp/session.go` — 3-way session mapping (CDP ↔ Juggler ↔ Target)
- `pkg/backend/juggler/` — Juggler pipe client (FD 3/4, null-byte framing)
- `pkg/bridge/bridge.go` — Main router, context ID counter, session resolution
- `pkg/bridge/target.go` — Tab/page dual-session model for Puppeteer
- `pkg/bridge/page.go` — Navigate, screenshot, setContent, getLayoutMetrics, createIsolatedWorld, addScriptToEvaluateOnNewDocument, handleJavaScriptDialog
- `pkg/bridge/runtime.go` — evaluate (awaitPromise wrapping), callFunctionOn (objectId pass-through), getProperties
- `pkg/bridge/dom.go` — getDocument, querySelector/All, describeNode, resolveNode, getBoxModel, getContentQuads, getOuterHTML, scrollIntoViewIfNeeded, focus, setFileInputFiles
- `pkg/bridge/input.go` — Mouse, keyboard, touch dispatch → Juggler Page.dispatch*
- `pkg/bridge/network.go` — Cookies, headers, cache, request interception toggle
- `pkg/bridge/emulation.go` — Viewport, UA, geo, timezone, locale, touch, media
- `pkg/bridge/events.go` — Full event pipeline: target attach/detach, navigation lifecycle, execution contexts, console, network (request/response/finish/fail), dialog open/close, frame attach/detach
- `pkg/bridge/stub.go` — 20+ stub domains, Browser.getVersion/close/getWindowForTarget

**Architecture:** Dual-session model (tab + page per target) matching Puppeteer's Chrome protocol expectations. Numeric CDP context IDs mapped bidirectionally to Juggler string context IDs. awaitPromise supported via async IIFE wrapping.

**Dual Backend:**
- `--backend juggler` (default) — Pipe FD 3/4 transport, direct Juggler protocol. Fastest, most complete.
- `--backend bidi` — WebDriver BiDi over WebSocket. Future-proof (W3C standard). BiDi client translates Juggler-style calls internally so bridge layer works unchanged. Full method coverage: Browser, Page, Runtime, Input, Network, Accessibility domains. Event translation: contextCreated→attachedToTarget, realmCreated→executionContextCreated, etc.
- `--bidi-url ws://host:port/session/id` — Connect to existing BiDi endpoint without launching Firefox.

**VulpineOS Integration:**
- `internal/foxbridge/process.go` — Process manager: findFoxbridge(), Start(), Stop(), CDPURL(), waitForPort()
- `cmd/vulpineos/main.go` — Launches foxbridge alongside kernel on startup. Sets `browser.cdpUrl` in openclaw.json to route OpenClaw through Camoufox.
- When foxbridge is available, OpenClaw's browser tools transparently use Camoufox instead of Chrome.
- Graceful fallback: if foxbridge binary not found, OpenClaw uses its built-in Chrome.

### Current TUI Layout: 3-Column Agent Workbench

```
Left (system+agents) | Center (conversation) | Right (agent detail + contexts)
```

- **Left top:** System info (kernel status, memory, lag, risk, pool stats, context/page counts)
- **Left bottom:** Agent list with status icons
- **Center:** Full-height conversation panel — messages scroll up, input at bottom (Claude CLI style)
- **Right top:** Agent detail (name, status, tokens, task, profile, proxy)
- **Right bottom:** Active browser contexts

**Focus cycle:** AgentList -> Conversation -> AgentDetail -> Contexts (Tab)
**Keybinds:** `n` new agent, `j/k` navigate, `Enter` chat, `p` pause, `r` resume, `x` delete, `S` settings, `c` reconfigure, `q` quit

### OpenClaw Integration Status

**Current:** Gateway mode — OpenClaw spawns via `openclaw run --config ~/.vulpineos/openclaw.json`. Config disables Chrome browser, routes through VulpineOS MCP server (`vulpine_snapshot` for token-optimized DOM, `vulpine_navigate`, `vulpine_click`, etc.).

**Limitation:** OpenClaw still bundles/expects Chrome for its native browser tools. VulpineOS MCP bridge works around this by providing equivalent tools backed by Camoufox/Juggler. Foxbridge will eventually provide a transparent CDP layer so OpenClaw thinks it's talking to Chrome.

### Known Limitations

- OpenClaw agent spawning requires Node.js + OpenClaw installed (`npm install` or bundled)
- Fingerprint generation requires Python + BrowserForge for full pipeline (falls back to OS-consistent profiles)
- Trust warming is opt-in (default off) — aggressive warming can trigger bot detection
- Context pool recycles after 50 uses; long-running agents may hit this
- Remote WebSocket mode (`--serve`/`--remote`) does not yet support TLS certificate management
- Docker build (`Dockerfile.vulpinebox`) requires pre-built Camoufox binary
- Foxbridge BiDi backend: Firefox BiDi WebSocket auto-discovery not yet implemented (requires --bidi-url or falls back to Juggler)
