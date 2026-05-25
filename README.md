<p align="center">
  <img src="assets/VulpineOSBanner.png" alt="VulpineOS" width="600">
</p>

<p align="center">
  <b>Operate Stealth-Aware Nanoclaw Agents at Scale</b>
</p>

<p align="center">
VulpineOS is the operating system for AI browser agents: a Firefox/Camoufox-based platform for managing Nanoclaw agents with unique identities, browser-engine security, and stealth-aware runtime controls.
</p>

<p align="center">
  <a href="https://github.com/VulpineOS/VulpineOS/actions/workflows/ci.yml"><img src="https://github.com/VulpineOS/VulpineOS/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
</p>

<p align="center">
  <a href="https://docs.vulpineos.com">Documentation</a> ·
  <a href="https://vulpineos.com">Website</a> ·
  <a href="https://github.com/VulpineOS/foxbridge">Foxbridge CDP Proxy</a> ·
  <a href="https://github.com/VulpineOS/VulpineOS/issues">Issues</a>
</p>

---

## Why VulpineOS?

AI agents that browse the web face three unsolved problems:

1. **Prompt injection** — Hidden elements on pages trick agents into executing malicious instructions
2. **Page mutation** — The page changes between when the agent reads it and when it acts
3. **Token waste** — Raw HTML/accessibility trees consume 10x more tokens than necessary

Most existing solutions try to fix these in JavaScript or in the agent framework. VulpineOS fixes them in the browser engine itself, below page-level JavaScript hooks, making the protections harder to observe or bypass from the page.

---

## Origin

VulpineOS was born from work on [Camoufox](https://github.com/daijro/camoufox), the open-source anti-detect browser originally created by [daijro](https://github.com/daijro). Camoufox pioneered C++-level fingerprint injection — spoofing navigator properties, WebGL parameters, fonts, screen dimensions, and hundreds of other signals at the implementation level rather than through detectable JavaScript overrides.

[Clover Labs](https://cloverlabs.ai) took over maintenance of Camoufox, where Elliot built per-context fingerprint spoofing — the ability to run multiple browser contexts, each with a completely unique hardware identity, in a single Camoufox process. This work revealed that the same C++ interception techniques used for fingerprint rotation could solve the AI agent security problem: if you can intercept what the browser exposes to JavaScript, you can also intercept what the browser exposes to AI agents.

VulpineOS builds on Camoufox's battle-tested stealth foundation (Firefox 146.0.1) and adds four security phases purpose-built for autonomous agents, a Go TUI for managing agents, and full integration with Nanoclaw for deploying AI agents at scale.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                        VulpineOS                              │
│                                                              │
│  C++ Engine (Firefox 146.0.1 + Camoufox patches)             │
│  ├── Phase 1: Injection-Proof Accessibility Filter            │
│  ├── Phase 2: Deterministic Execution (Action-Lock)           │
│  ├── Phase 3: Token-Optimized DOM Export                      │
│  └── Phase 4: Autonomous Trust-Warming                        │
│                                                              │
│  Juggler Protocol (pipe FD 3/4)                               │
│  ├── Telemetry Service (memory, risk score, 2s interval)      │
│  └── Trust Warming Service (idle-time profile warming)        │
│                                                              │
│  Go Runtime (38 packages, 500+ tests)                         │
│  ├── Bubbletea TUI (3-column agent workbench)                 │
│  ├── Web Panel (React SPA, 10 pages, 46 control messages)     │
│  ├── Identity Vault (SQLite — citizens, templates, sessions)  │
│  ├── Context Pool (pre-warm, recycle, memory limits)           │
│  ├── Orchestrator (spawn citizens + nomads, auto-release)      │
│  ├── Nanoclaw Manager (30 AI providers, skills, SOP files)     │
│  ├── Proxy Manager (geo-synced fingerprints, auto-rotation)    │
│  ├── MCP Server (36 tools via stdio)                           │
│  ├── Foxbridge CDP Proxy (Puppeteer compatibility)             │
│  ├── Agent Bus (inter-agent messaging with approval policies)  │
│  ├── Cost Tracker (per-agent budgets, usage alerts)            │
│  ├── Notification Delivery (event notifications, async delivery)│
│  ├── Session Recording (timeline capture, replay, export)      │
│  ├── Scripting DSL (8-action JSON scripts, zero LLM tokens)   │
│  ├── Security (CSP, DOM monitoring, signatures, sandbox)       │
│  ├── Token Optimization (viewport, cache, diff, batch)         │
│  ├── Kernel Watchdog (crash recovery, auto-restart)            │
│  └── Remote Access (HTTP/WS server, API key auth)              │
│                                                              │
│  Docker: Vulpine-Box (one-click VPS deployment)               │
└──────────────────────────────────────────────────────────────┘
```

---

## Core Security Phases

### Phase 1: Injection-Proof Accessibility Filter

Strips non-visible DOM nodes from the accessibility tree before the AI agent sees them. Hidden `<div>` with "ignore previous instructions"? Gone.

- 7 visibility checks ordered by cost (aria-hidden → display → visibility → opacity → dimensions → position → clip)
- Runs at the Gecko accessibility layer — JavaScript cannot override it
- Detects and logs injection attempts to the telemetry pipeline

### Phase 2: Deterministic Execution (Action-Lock)

Freezes the page completely while the agent is thinking. No JavaScript, no timers, no layout reflows, no animations, no event handlers.

- C++ patch to `nsDocShell`: `suspendPage()` / `resumePage()`
- Freezes refresh driver, suspends timers, suppresses event handling
- Guarantees the page the agent analyzed is the page it acts on
- Auto-releases on navigation

### Phase 3: Token-Optimized DOM Export

Compressed semantic JSON snapshot. The public fixture benchmark currently measures 2,942 average tokens for VulpineOS optimized DOM versus 42,832 for compact Chrome AX, a 93.1% reduction, while passing fixture-level semantic and action-coverage checks. Agents can request larger `expanded` or `full` snapshot profiles, or retry a truncated compact snapshot with the next larger profile when a target may have been pruned.

```json
{"v":1,"title":"Example","url":"https://example.com","nodes":[
  [0,"doc","Example"],
  [1,"nav","Main Navigation"],
  [2,"a","Home",{"hr":"/"},"@0"],
  [2,"a","About",{"hr":"/about"},"@1"],
  [1,"main",""],
  [2,"h1","Welcome"],
  [2,"btn","Sign Up",null,"@2"]
]}
```

- 50+ role codes (`heading`→`h2`, `button`→`btn`, `link`→`a`)
- Element references (`@0`, `@1`) on interactive elements for click/type by ref
- Viewport-only mode — only return elements visible on screen
- Structural wrapper skipping, single-child flattening, text merging
- Reproducible benchmark: `npm run benchmark:tokens`

### Phase 4: Autonomous Trust-Warming

Background service that builds organic browsing history on high-authority sites while the agent is idle. Human-like bezier mouse trajectories, Gaussian-randomized dwell times, rate-limited visit scheduling.

---

## Advanced Security

Beyond the four core phases, VulpineOS includes hardened runtime security:

| Feature | Description |
|---------|-------------|
| **Content Security Policy** | CSP enforcement for agent-controlled pages |
| **DOM Mutation Monitoring** | Real-time alerting on unexpected DOM changes |
| **Action Signatures** | 13 injection signatures verified before execution |
| **Agent Sandboxing** | Constraint enforcement on agent capabilities |

---

## Platform Features

| Feature | Description |
|---------|-------------|
| **Web Panel** | React SPA (Vite) with 10 pages — Dashboard, Agents, Agent Detail, Bus, Contexts, Proxies, Security, Scripts, Logs, and Login. 46 WebSocket control messages, including secret-redacted persisted runtime audit history, reconnect/session auth, budget controls, bus approvals, proxy rotation, runtime-backed security status, real script execution, and a denser operator dashboard shell with runtime alerts and quick actions. |
| **Agent Bus** | Inter-agent communication (ask, delegate, reply, notify) with user-controlled approval policies and full audit trail |
| **Cost Tracking** | Per-agent token usage and API cost tracking with budget limits. Built-in pricing for Claude, GPT-4o, Gemini. Alerts at configurable thresholds. |
| **Session Recording** | Record browser actions as timestamped timelines with a bounded per-agent in-memory window and sensitive action-data redaction. Export to JSON. Terminal-based replay at real speed. |
| **Proxy Rotation** | Auto-rotate proxies on rate limit, IP block, or time interval. Fingerprint re-synced on every rotation. 32-country locale map. |
| **Notification Delivery** | HTTP notifications for agent.completed/failed/paused/interrupted, rate_limit.detected, injection.detected, budget.alert/exceeded. Async delivery with secret verification and redacted delivery logs. |
| **Scripting DSL** | JSON scripting language for repetitive tasks without LLM calls. 8 actions: navigate, click, type, wait, extract, screenshot, set, if. Variable expansion with bounded script payloads, capped waits, and redacted operator-facing results. |
| **Kernel Watchdog** | Monitors Camoufox every 2s. On crash: fires callback, auto-restarts (up to 3 attempts), re-establishes Juggler connection. |
| **Token Optimization** | Viewport-aware DOM pruning, persistent page cache, delta encoding between snapshots, batch operations. |
| **Page Cache** | Saves and restores page state (URL, HTML, cookies, scroll, forms) across agent restarts. |
| **Rate Limit Monitor** | Pattern-based scanning of agent output for 429s, captchas, and blocks. Per-agent failure tracking. |
| **Structured Logging** | JSON structured logger with levels, component tags, and field chaining. |

---

## Go TUI: Agent Workbench

A terminal-based command center for managing AI agents, browser contexts, and identity profiles.

```text
+----------------+------------------------------+-----------------+
| System         | Conversation                 | Agent Detail    |
| Kernel: running|                              | Name: Scout-1   |
| Mode: GUI      | you  Find cheap flights to   | Status: active  |
| Route: CAMOUFOX|      Tokyo in March          | Tokens: 12,847  |
| Window: VISIBLE|                              | Proxy: US-West  |
| Gateway: up    | scout  Thinking...           | Profile: mac-m1 |
|                |                              |                 |
| Agents         |                              | Contexts        |
| > Scout-1 act  |                              | ctx-a91 page    |
|   Scout-2 new  |                              |   about:blank   |
|   Research done|                              | ctx-b22 page    |
|   Monitor pause| > Type a message...          |   google.com    |
+----------------+------------------------------+-----------------+
```

**Keybinds:** `n` new agent · `j/k` navigate · `Enter` chat · `p/r` pause or resume selected agent · `P/R` pause or resume all agents · `X` kill all live agents · `x` delete · `v` show or hide Camoufox · `o` open raw session log · `t` toggle action trace · `m` toggle arrow-key mode · `S` settings · `c` reconfigure · `q` quit

Arrow keys navigate the agent list and conversation by default. If you want panel resizing on arrow keys, enable **Arrow Keys Resize Panels** in the general preferences. Press `m` to toggle resize mode for the current session without rewriting the saved default.
The settings toggle controls the saved default; `m` is the temporary per-session mode switch.

The generated Nanoclaw workspace under `~/.nanoclaw-vulpine/workspace` is refreshed with VulpineOS-owned bootstrap files so agents follow the current assigned name and task instead of inheriting an older persona from a stale workspace.
New-agent introduction turns now also assert the assigned runtime name explicitly, reducing drift toward an older remembered persona.
Those bootstrap files also force exact action/result reporting and explicitly forbid claiming a browser action succeeded after an error, timeout, or incomplete result.
The footer always shows the current arrow-key mode as `mode:navigate` or `mode:resize`.
The system panel now shows both the browser mode (`GUI` or `HEADLESS`) and the active browser route (`CAMOUFOX` when Nanoclaw is attached through foxbridge into Camoufox), so the operator can verify the runtime path without checking logs.
The TUI also shows the current browser window state (`VISIBLE`, `HIDDEN`, `HEADLESS`, or `N/A`) so `v` no longer feels opaque when the window controller cannot act.
The web panel now surfaces the same route and mode signal on Dashboard, including whether that route came from live runtime state or only from the shared Nanoclaw profile, plus whether the Nanoclaw daemon is currently running.
Agent model setup now stays separate from the Nanoclaw profile, so a machine can show a valid browser/profile route even when the current model credentials still need attention.
If an older machine already has a valid `~/.nanoclaw-vulpine/nanoclaw.json` but a stale or blank `~/.vulpineos/config.json`, VulpineOS now backfills the local provider/model/key state from the Nanoclaw profile instead of pretending the installation is unconfigured.
Saving provider settings from the web panel now also marks setup complete and regenerates the shared Nanoclaw profile immediately, so panel edits apply to the next agent run without waiting for a separate reconfigure pass.
Provider/model choices now come from the live runtime registry instead of raw free-text IDs, reducing config typos.
Served mode now starts the Nanoclaw gateway with the same repair path as local mode, so browser-backed agents do not silently lose gateway support when you move from the local TUI to the hosted panel/server path.
Served mode also supports `--no-browser`, which keeps the panel and control API available without launching a kernel; the panel reports that state as `DISABLED` route/mode instead of crashing in `Browser.enable`.
Gateway start, stop, and profile-repair failures now also land in the secret-redacted runtime audit stream, so startup problems appear in the system panel/runtime views instead of only in raw log files.
Pause/resume flows now keep scoped Nanoclaw runtime configs alive for the full resumed agent lifecycle, so a context-pinned agent does not silently fall back to the shared browser route after resume.
If the conversation panel is awake but the cursor has dropped out of the input, the next typed character re-focuses chat automatically, while `v` still works as a browser show or hide shortcut from that unfocused state.
After a newly created agent sends its first real reply, VulpineOS automatically snaps focus back to the chat box so the conversation is immediately writable again.
If the startup turn ends without an assistant reply, the first terminal agent status now also re-focuses chat so the input does not stay visually awake but functionally locked.
Newly created active agents now stay visually locked until they actually reply or finish startup, so the disabled banner does not disappear before chat input is really available.
The `v` shortcut now refreshes the actual macOS window visibility before toggling, so a stale cached state no longer turns the first show or hide into a no-op.
When the macOS window-controller path fails, the toggle notice now preserves the underlying `osascript` error so permission problems and missing process targets are visible instead of being collapsed into a generic failure.
Press `t` to switch the center panel into a trace-only view of system tool events so browser/tool starts, completions, and failures are easy to inspect without mixing them into the full conversation stream.
If a tool fails and the agent still replies as if the task succeeded, VulpineOS now injects an explicit warning into that trace so false-success replies are visible immediately.
Non-zero command exits in Nanoclaw tool results are now classified as failures even when the upstream payload reports `status:"completed"`, so trace output stays aligned with the real action result.
Timeouts and incomplete tool results are now classified separately from hard failures, and the web panel labels trace rows as `RUN`, `OK`, `PARTIAL`, `TIMEOUT`, `FAIL`, `THINK`, or `WARN` instead of flattening everything into a generic system line.
When Nanoclaw writes provider thinking blocks into the session log, VulpineOS now exposes them inside the trace view as `Thinking:` entries instead of hiding them behind the raw JSONL.
Tool-result summaries now preserve the exact tool-call context when available, so trace output says what action actually ran instead of falling back to generic `Tool completed: browser`.
Press `o` to open the selected agent's raw Nanoclaw session log in the system viewer for full JSONL trace inspection, including provider-emitted thinking blocks when the provider writes them.
The web panel's Raw tab now auto-refreshes while it is open, so long-running agent/tool sessions can be inspected without manually reloading every update.
The web panel's Raw tab redacts provider hidden-reasoning fields, signatures, bearer fragments, secret key/value fields, cookies, and credential-bearing URLs while preserving the surrounding session timeline for execution inspection.
Agent Detail now consumes only new websocket events for the selected agent, so live conversation and trace rows do not replay the same status or assistant line on every rerender.
While the chat input is focused, `Ctrl+V`, `Ctrl+O`, and `Ctrl+T` trigger browser toggle, raw log open, and trace toggle without stealing ordinary typed letters. Plain `v`, `o`, and `t` also work from a focused chat box when the input is still empty.

The agent list shows unread reply counts for non-selected agents so background work does not disappear while you are focused elsewhere.

On quit, VulpineOS pauses active agents before exiting so the next launch can resume saved sessions instead of dropping in-flight work.

Local TUI startup and runtime logs are written to `~/.vulpineos/logs/local-tui.log` so the terminal UI stays clean while the kernel, foxbridge, and Nanoclaw subsystems initialize.

Pressing `c` now queues the setup wizard for the next launch without clearing the active config first, so cancelling reconfigure no longer leaves the machine stuck in an unconfigured state.

Nanoclaw session log streaming is used as a fallback conversation source, so final assistant replies still reach the TUI and tests even when the CLI omits the final `--json` payload on stdout.
New agents now start by working on the assigned task immediately instead of spending the first turn on a canned self-introduction, and exact-output tasks are passed through as direct task instructions.

### Phase 2 Nanoclaw Integration Notes

The `phase2` branch is wired to Nanoclaw through its Unix socket at `data/cli.sock`. During local bring-up we found several Nanoclaw/runtime issues that are worth preserving because some are general integration bugs rather than machine-specific setup problems.

General Nanoclaw issues fixed locally:

- Provider config must use Nanoclaw runner provider names. A group configured with `provider: "anthropic"` makes the container exit with `Unknown provider: anthropic`; the current Nanoclaw runner registers `claude`, `mock`, and `codex`. Fix: use `claude` for the Claude SDK path or `codex` for the Codex path.
- OneCLI CA files were mounted from macOS temp paths under `/var/folders/...`. With Colima/virtiofs those bind mounts appeared inside the container as empty directories, causing `Self-signed certificate detected`. Fix: copy the OneCLI CA files into a project-shared path such as `nanoclaw/data/onecli-ca/` before adding the Docker `-v` mounts.
- CLI/socket sessions had default `session_routing` but no model-visible destination row. The agent produced replies like `<message to="unknown:cli:local">...`, which were dropped as unknown destinations. Fix: write a `reply` destination into the session inbound DB from the current session routing so agents can send `<message to="reply">...</message>` back to the socket caller.
- The Codex provider ignored the configured `model` option and only read `CODEX_MODEL`. Fix: prefer `options.model`, then `CODEX_MODEL`, then the provider default.

Local environment issues encountered on this machine:

- Docker BuildKit was enabled but `docker-buildx` was missing. Fix: install `docker-buildx` and add `/opt/homebrew/lib/docker/cli-plugins` to Docker's CLI plugin dirs.
- Docker needed Colima's socket: `DOCKER_HOST=unix:///Users/rowan/.colima/default/docker.sock`.
- Colima was running with `2GiB` RAM, which killed the Codex-enabled image build with `cannot allocate memory`. Fix: restart Colima with more memory, e.g. `colima stop && colima start --memory 6 --cpu 4`.
- The default Homebrew Node 25 binary was broken due to a missing `libsimdjson.30.dylib`. Fix: run Nanoclaw and test scripts with Node 22 via `/opt/homebrew/opt/node@22/bin/node` or put `/opt/homebrew/opt/node@22/bin` first on `PATH`.
- This machine had Codex auth in `~/.codex/auth.json` but no usable Anthropic key, so the working path was Nanoclaw `provider: "codex"` with a per-group image including `@openai/codex`.

Current verified local path:

- Nanoclaw directory: `/Users/rowan/Documents/nanoclaw`
- Nanoclaw socket: `/Users/rowan/Documents/nanoclaw/data/cli.sock`
- Docker socket: `unix:///Users/rowan/.colima/default/docker.sock`
- Verified socket response: sending `Reply with exactly: verified` returned `{"text":"verified"}`.

The live operator path is covered by env-gated soak tests in `internal/agent_soak_integration_test.go` and `internal/remote/panel_agent_soak_test.go`, including persisted-session resume plus panel-driven pause and kill flows.

Live browser, Nanoclaw, and MCP-browser integration tests in `internal/integration_test.go` and `internal/mcp/live_integration_test.go` are gated behind `VULPINEOS_RUN_LIVE=1` so the default `go test` and CI path stay hermetic even on machines that already have Camoufox installed. The scoped MCP soak harness requires both `VULPINEOS_RUN_SOAK=1` and `VULPINEOS_RUN_LIVE=1`; `./scripts/run-soak.sh` sets both before running it.

---

## Web Panel

A React SPA served from the Go binary — no separate frontend deployment needed.

`npm --prefix web run build` now refreshes both `web/dist/` and the embedded `cmd/vulpineos/panel/` assets, so a subsequent `go build -o vulpineos ./cmd/vulpineos` ships the current panel instead of stale frontend files.

**10 pages:** Dashboard, Agents, Agent Detail, Bus, Contexts, Proxies, Security, Scripts, Logs, Login

**46 control messages** covering: agent CRUD plus bulk controls and session-log access; config get/set/providers; cost usage plus persisted per-agent/default budgets; notification delivery; proxy management plus rotation get/set; agent bus pending/approve/reject/policies/add/remove; session recording export; fingerprint get/generate-and-apply; runtime-backed script execution; runtime-backed security status; status; optional extension availability and timelines; runtime audit history with retention/export controls; and context create/remove/list.

Credential, audio, mobile, and Sentinel-backed controls are stable public interfaces. The stock open-source build returns unavailable for extension-backed actions unless a provider is registered by an external extension package. When Sentinel data is available, panel/status responses redact sink URLs, probe URLs, proxy endpoints, timeline attributes, and opaque payloads before rendering; timeline requests are capped before calling the provider.

Agent Detail includes separate conversation, action trace, raw session log, recording, and fingerprint views so operator-visible tool activity is inspectable without exposing hidden reasoning or sensitive action values.

The panel now validates access keys through `/auth/check`, keeps the key only
for the current browser session, authenticates browser websocket sessions with
an access subprotocol instead of a tokenized websocket URL, and surfaces
connecting, reconnecting, and failed states inline instead of relying on browser
alerts.

The panel now also includes:
- a dedicated **Bus** page for pending approvals and communication policies
- persisted default agent budgets through config
- optional extension availability surfaces when a provider is attached
- **Agent Detail** controls for per-agent budget overrides, recording export, and fingerprint regeneration
- **Proxies** controls for persisted per-agent rotation rules
- **Scripts** execution against a real browser context through the server-side scripting engine
- **Security** protection states sourced from runtime/config instead of fixed frontend flags
- a richer **Dashboard** shell showing runtime route/mode/window, retained runtime alerts, spend, budget posture, active-work previews, and direct operator shortcuts
- an **Agents** view that surfaces aggregate spend/tokens plus per-agent budget source and limit summaries without drilling into each detail page

Panel script execution rejects payloads over 64 KiB, rejects scripts over 100 steps, and rejects `wait` durations longer than 30 seconds. Runtime filter echoes, proxy errors, notification delivery logs, MCP credential metadata/errors, raw session logs, and Sentinel timeline payloads all redact common secret fields before reaching operator-facing surfaces.

Access via `vulpineos panel`, `vulpineos serve`, or through the remote client.

---

## Foxbridge: CDP-to-Firefox Protocol Proxy

[Foxbridge](https://github.com/VulpineOS/foxbridge) is a standalone Go binary that translates Chrome DevTools Protocol (CDP) to Firefox's Juggler and WebDriver BiDi protocols. Any CDP client — Puppeteer, browser-use, and similar tools — can control Camoufox as if it were Chrome.

- **74/74 Puppeteer Juggler tests** passing
- **62/62 Puppeteer BiDi tests** passing
- Dual backend: `--backend juggler` (pipe) or `--backend bidi` (WebSocket)
- Fetch domain with request/response interception
- Embedded into VulpineOS startup — Nanoclaw agents automatically use Camoufox
- Nanoclaw is pinned to an isolated VulpineOS workspace under `~/.nanoclaw-vulpine/workspace` so personal Nanoclaw identities and memories do not leak into VulpineOS agents
- VulpineOS repairs the shared Nanoclaw profile after gateway startup and runs agents against per-run cloned configs, preventing gateway token drift and stale workspace/skill leakage
- On macOS and Linux, VulpineOS launches Nanoclaw in its own process group so pause and kill also tear down descendant agent processes cleanly

---

## Getting Started

### Prerequisites

- Go 1.26+
- Node.js 22.16+ (for Nanoclaw)
- Firefox/Camoufox binary (or build from source)

### Install

One-liner:

```bash
curl -sL https://raw.githubusercontent.com/VulpineOS/VulpineOS/phase2/install.sh | bash
```

Or step-by-step:

```bash
git clone https://github.com/VulpineOS/VulpineOS.git
cd VulpineOS
go build -o vulpineos ./cmd/vulpineos
```

You also need a Camoufox/Firefox-compatible browser binary. Either complete the
source build above, place a packaged Camoufox next to `./vulpineos`, or pass one
explicitly with `--binary /path/to/camoufox`.

### Run

Local TUI:

```bash
./vulpineos
# or
./vulpineos tui
```

Local web panel:

```bash
./vulpineos panel
```

`vulpineos panel` binds to `127.0.0.1`, prints a direct panel URL, and
opens the browser when possible. If no `--api-key` is provided, VulpineOS
generates one for the session and prints it next to the panel URL. The
auto-opened browser receives the token for session bootstrap, but the terminal
output keeps the URL clean so access keys do not end up in copied logs.

Networked serve mode:

```bash
./vulpineos serve --host 0.0.0.0 --port 8443 --no-tls
```

Explicit access key:

```bash
./vulpineos serve --host 0.0.0.0 --port 8443 --no-tls --api-key YOUR_KEY
```

If `--api-key` is omitted in serve mode, VulpineOS generates one at startup
and prints both the access key and the panel URL. Paste that key into the panel
login, or pass an explicit `--api-key` when you want a stable key. The panel
login validates explicit keys through `/auth/check` before opening the websocket
session.

Remote panel shortcut:

```bash
./vulpineos remote panel --url http://your-host:8443 --api-key YOUR_KEY
# `panel` is the default remote mode, so this also works:
./vulpineos remote --url http://your-host:8443 --api-key YOUR_KEY
```

Remote TUI:

```bash
./vulpineos remote tui --url http://your-host:8443 --api-key YOUR_KEY
```

MCP server:

```bash
./vulpineos mcp
```

## Playwright MCP smoke

The repo ships a repo-local panel smoke script for the local Playwright MCP:

- `scripts/playwright/smoke-panel.js`

Point it at a running panel with `VULPINE_PANEL_SMOKE_URL`. If the panel URL
does not include a token, also set `VULPINE_PANEL_SMOKE_ACCESS_KEY`. The script
does not write screenshots unless
`VULPINE_PANEL_SMOKE_SCREENSHOT=/path/to/output.png` is set.

When no `--binary` flag is provided, VulpineOS prefers a repo-local
`camoufox-*/obj-*/dist` build before falling back to a saved configured
binary or older installed copies.
`--binary` may point directly at the executable, at `Camoufox.app`, at a browser
`dist` directory, or at the repo root containing `camoufox-*/obj-*/dist`.

First launch opens a setup wizard to configure your AI provider (Anthropic, OpenAI, Google, xAI, and 27 more).

For Nanoclaw architecture and daemon details, see
[docs/superpowers/specs/2026-05-16-nanoclaw-replacement-design.md](docs/superpowers/specs/2026-05-16-nanoclaw-replacement-design.md)
and
[docs/superpowers/specs/2026-05-17-nanoclaw-daemon-manager-design.md](docs/superpowers/specs/2026-05-17-nanoclaw-daemon-manager-design.md).

### Docker (Vulpine-Box)

```bash
export VULPINE_API_KEY=$(openssl rand -hex 32)
./scripts/check-vulpinebox.sh
docker compose up -d
vulpineos remote panel --url http://your-vps:8443 --api-key $VULPINE_API_KEY
# or
vulpineos remote tui --url http://your-vps:8443 --api-key $VULPINE_API_KEY
```

`docker compose up -d` starts `vulpineos serve --binary ./browser/camoufox --port 8443 --no-tls`
inside the container. By default the bundled `docker-compose.yml` exposes plain HTTP on
port `8443`; add `VULPINE_TLS_CERT` and `VULPINE_TLS_KEY` plus mounted certificate files if
you want HTTPS/WSS.

The compose file requires `VULPINE_API_KEY` and will not fall back to a public default.
Use the same value with `vulpineos remote panel` or `vulpineos remote tui` when connecting
from another machine.
Run `./scripts/check-vulpinebox.sh` before `docker compose up -d` to verify Docker,
the access key, and the required Linux browser artifact are ready.
If you want the full deployment notes, including required browser artifacts under
`dist/camoufox-linux/`, persistent volumes, and optional TLS, see
[docs.vulpineos.com/docker](https://docs.vulpineos.com/docker).

## Release notes

For public release gating and audit steps, see
[docs/release-checklist.md](docs/release-checklist.md) and
[docs/release-hygiene.md](docs/release-hygiene.md).

For a standardized AWS Mac builder runbook and wrapper scripts, see
[docs/ec2-mac-builder.md](docs/ec2-mac-builder.md).

---

## MCP Tools

VulpineOS exposes 36 tools via Model Context Protocol:

| Tool | Description |
|------|-------------|
| Core browser controls | Navigate, snapshot, click, type, screenshot, scroll, context lifecycle, and accessibility-tree access |
| Ref-based interactions | Click, type, and hover by `@ref` from optimized DOM snapshots. Snapshot profiles are `compact` (180 nodes/90 chars), `expanded` (360/160), and `full` (800/240); `retry:true` steps up after truncation. |
| Reliability tools | Wait, find, verify, screenshot diff, page-settled checks, select options, fill forms, page info, key press, clear input, form errors |
| Human-realism tools | Human-like click, scroll, and type timing |
| Annotated interaction | Annotated screenshots and click-by-label with `@N` labels |
| Extension surfaces | Credential metadata/autofill, audio capture, Sentinel signals, and mobile bridge hooks. The stock public build returns unavailable unless an extension provider is attached; credential URL metadata/errors are redacted at the MCP boundary. |
| Mobile bridge | List Android devices, start a local CDP bridge, and disconnect bridge sessions when the public mobile bridge provider is installed |

---

## Ecosystem

The public ecosystem is split by repository:

| Product | Source | Public Description |
|---------|--------|--------------------|
| VulpineOS | Open source | Browser-agent runtime, MCP tools, TUI, web panel, and remote server |
| Foxbridge | Open source | CDP-to-Firefox bridge for Nanoclaw, Puppeteer, and CDP clients |
| Vulpine Mark | Open source | Set-of-Mark screenshots, element labels, and label-based interactions |
| MobileBridge for Android | Open source | Android device discovery, CDP proxying, gestures, and sessions |

Optional extension hooks are exposed through stable no-op interfaces in this repository. Hosted products are documented separately when they are ready for public use.

---

## Testing

**500+ Go tests** across 38 packages, all passing with race detector enabled.

```bash
go test -race ./cmd/... ./internal/...
```

---

## Build from Source

```bash
make fetch          # Download Firefox 146.0.1 source
make setup          # Extract + init git repo
make dir            # Apply patches + copy additions
make build          # Compile (~5 min on M1 with artifact builds)
make package-macos  # Package the macOS browser artifact
```

---

## Credits

VulpineOS stands on the shoulders of excellent open-source work:

- **[daijro](https://github.com/daijro)** — Created [Camoufox](https://github.com/daijro/camoufox), pioneering C++-level fingerprint injection in Firefox. The foundation that makes VulpineOS possible.
- **[Clover Labs](https://cloverlabs.ai)** — Primary maintainers of Camoufox.
- **[BrowserForge](https://github.com/daijro/browserforge)** — Bayesian network fingerprint generator that ensures spoofed identities match real-world traffic distribution.
- **[LibreWolf](https://gitlab.com/librewolf-community/browser/source)** — Build system inspiration and debloat patches.
- **[riflosnake/HumanCursor](https://github.com/riflosnake/HumanCursor)** — Original human-like cursor algorithm, ported to C++.

---

## License

VulpineOS is released under the [MPL 2.0](LICENSE) license, consistent with its Firefox/Camoufox heritage.

---

<p align="center">
  <a href="https://vulpineos.com">vulpineos.com</a> · <a href="https://docs.vulpineos.com">docs</a> · <a href="https://foxbridge.vulpineos.com">foxbridge</a>
</p>
