<p align="center">
  <img src="assets/VulpineOSBanner.png" alt="VulpineOS" width="600">
</p>

<p align="center">
  <b>Operate Stealth and Secure OpenClaw Agents at Scale</b>
</p>

<p align="center">
VulpineOS is the operating system for AI browser agents — a Firefox/Camoufox-based platform for managing hundreds of OpenClaw agents with unique identities, full security, and zero detection.
</p>

<p align="center">
  <a href="https://github.com/PopcornDev1/VulpineOS/actions/workflows/ci.yml"><img src="https://github.com/PopcornDev1/VulpineOS/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
</p>

<p align="center">
  <a href="https://vulpineos.com">Documentation</a> ·
  <a href="https://github.com/PopcornDev1/foxbridge">Foxbridge CDP Proxy</a> ·
  <a href="https://github.com/PopcornDev1/VulpineOS/issues">Issues</a>
</p>

---

## Why VulpineOS?

AI agents that browse the web face three unsolved problems:

1. **Prompt injection** — Hidden elements on pages trick agents into executing malicious instructions
2. **Page mutation** — The page changes between when the agent reads it and when it acts
3. **Token waste** — Raw HTML/accessibility trees consume 10x more tokens than necessary

Every existing solution tries to fix these in JavaScript or in the agent framework. VulpineOS fixes them in the browser engine itself — in C++, where they can't be detected or circumvented.

---

## Origin

VulpineOS was born from work on [Camoufox](https://github.com/daijro/camoufox), the open-source anti-detect browser originally created by [daijro](https://github.com/daijro). Camoufox pioneered C++-level fingerprint injection — spoofing navigator properties, WebGL parameters, fonts, screen dimensions, and hundreds of other signals at the implementation level rather than through detectable JavaScript overrides.

[Clover Labs](https://cloverlabs.ai) took over maintenance of Camoufox, where Elliot built per-context fingerprint spoofing — the ability to run multiple browser contexts, each with a completely unique hardware identity, in a single Camoufox process. This work revealed that the same C++ interception techniques used for fingerprint rotation could solve the AI agent security problem: if you can intercept what the browser exposes to JavaScript, you can also intercept what the browser exposes to AI agents.

VulpineOS builds on Camoufox's battle-tested stealth foundation (Firefox 146.0.1) and adds four security phases purpose-built for autonomous agents, a Go TUI for managing agents, and full integration with [OpenClaw](https://github.com/anthropics/openclaw) for deploying AI agents at scale.

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
│  Go Runtime (22 packages, 283 tests)                          │
│  ├── Bubbletea TUI (3-column agent workbench)                 │
│  ├── Web Panel (React SPA, 11 pages, 29 API endpoints)        │
│  ├── Identity Vault (SQLite — citizens, templates, sessions)  │
│  ├── Context Pool (pre-warm, recycle, memory limits)           │
│  ├── Orchestrator (spawn citizens + nomads, auto-release)      │
│  ├── OpenClaw Manager (31 AI providers, skills, SOP files)     │
│  ├── Proxy Manager (geo-synced fingerprints, auto-rotation)    │
│  ├── MCP Server (12 browser tools via stdio)                   │
│  ├── Foxbridge CDP Proxy (Puppeteer compatibility)             │
│  ├── Agent Bus (inter-agent messaging with approval policies)  │
│  ├── Cost Tracker (per-agent budgets, usage alerts)            │
│  ├── Webhooks (event notifications, async delivery)            │
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

Compressed semantic JSON snapshot achieving >50% token reduction vs standard accessibility trees.

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
| **Web Panel** | React SPA (Vite) with 11 pages — Dashboard, Agents, Contexts, Proxies, Security, Webhooks, Scripts, Settings, Logs, and more. 29 API endpoints over WebSocket. |
| **Agent Bus** | Inter-agent communication (ask, delegate, reply, notify) with user-controlled approval policies and full audit trail |
| **Cost Tracking** | Per-agent token usage and API cost tracking with budget limits. Built-in pricing for Claude, GPT-4o, Gemini. Alerts at configurable thresholds. |
| **Session Recording** | Record browser actions as timestamped timelines. Export to JSON. Terminal-based replay at real speed. |
| **Proxy Rotation** | Auto-rotate proxies on rate limit, IP block, or time interval. Fingerprint re-synced on every rotation. 32-country locale map. |
| **Webhook Notifications** | HTTP webhooks for agent.completed/failed/paused, rate_limit.detected, injection.detected, budget.alert/exceeded. Async delivery with secret verification. |
| **Scripting DSL** | JSON scripting language for repetitive tasks without LLM calls. 8 actions: navigate, click, type, wait, extract, screenshot, set, if. Variable expansion. |
| **Kernel Watchdog** | Monitors Camoufox every 2s. On crash: fires callback, auto-restarts (up to 3 attempts), re-establishes Juggler connection. |
| **Token Optimization** | Viewport-aware DOM pruning, persistent page cache, delta encoding between snapshots, batch operations. |
| **Page Cache** | Saves and restores page state (URL, HTML, cookies, scroll, forms) across agent restarts. |
| **Rate Limit Monitor** | Pattern-based scanning of agent output for 429s, captchas, and blocks. Per-agent failure tracking. |
| **Structured Logging** | JSON structured logger with levels, component tags, and field chaining. |

---

## Go TUI: Agent Workbench

A terminal-based command center for managing AI agents, browser contexts, and identity profiles.

```
┌─ System ──────┬─ Conversation ──────────────┬─ Agent Detail ──┐
│ Kernel: ● ON  │                              │ Name: Scout-1   │
│ Memory: 847MB │ you  Find cheap flights to   │ Status: ● Active│
│ Contexts: 3/20│      Tokyo in March          │ Tokens: 12,847  │
│ Risk: Low     │                              │ Proxy: US-West  │
│               │ scout ⠋ Thinking...          │ Profile: mac-m1 │
├─ Agents ──────┤                              ├─ Contexts ──────┤
│ ● Scout-1     │                              │ ctx-a91 page    │
│ ◌ Scout-2     │                              │   about:blank   │
│ ✓ Researcher  │                              │ ctx-b22 page    │
│ ⏸ Monitor     │ > Type a message...          │   google.com    │
└───────────────┴──────────────────────────────┴─────────────────┘
```

**Keybinds:** `n` new agent · `j/k` navigate · `Enter` chat · `p` pause · `r` resume · `x` delete · `S` settings · `q` quit

---

## Web Panel

A React SPA served from the Go binary — no separate frontend deployment needed.

**11 pages:** Dashboard, Agents, Agent Detail, Contexts, Proxies, Security, Webhooks, Scripts, Settings, Logs, Login

**29 API endpoints** covering: agent CRUD, config management, cost tracking, webhooks, proxy management, agent bus (pending/approve/reject/policies), session recording, fingerprints, and system status.

Access via `--serve --port 8443 --api-key KEY` or through the remote client.

---

## Foxbridge: CDP-to-Firefox Protocol Proxy

[Foxbridge](https://github.com/PopcornDev1/foxbridge) is a standalone Go binary that translates Chrome DevTools Protocol (CDP) to Firefox's Juggler and WebDriver BiDi protocols. Any CDP tool — OpenClaw, Puppeteer, browser-use — can control Camoufox as if it were Chrome.

- **74/74 Puppeteer Juggler tests** passing
- **62/62 Puppeteer BiDi tests** passing
- Dual backend: `--backend juggler` (pipe) or `--backend bidi` (WebSocket)
- Fetch domain with request/response interception
- Embedded into VulpineOS startup — OpenClaw agents automatically use Camoufox

---

## Getting Started

### Prerequisites

- Go 1.26+
- Node.js 20+ (for OpenClaw)
- Firefox/Camoufox binary (or build from source)

### Install

```bash
git clone https://github.com/PopcornDev1/VulpineOS.git
cd VulpineOS
npm install          # installs OpenClaw
go build -o vulpineos ./cmd/vulpineos
```

### Run

```bash
./vulpineos --binary /path/to/camoufox
```

First launch opens a setup wizard to configure your AI provider (Anthropic, OpenAI, Google, xAI, and 27 more).

### Docker (Vulpine-Box)

```bash
docker compose up -d
vulpineos --remote wss://your-vps:8443/ws --api-key $VULPINE_API_KEY
```

---

## MCP Tools

VulpineOS exposes 12 browser tools via Model Context Protocol:

| Tool | Description |
|------|-------------|
| `vulpine_snapshot` | Token-optimized DOM with element refs and viewport-only mode |
| `vulpine_click_ref` | Click element by `@ref` from snapshot |
| `vulpine_type_ref` | Focus and type into element by `@ref` |
| `vulpine_hover_ref` | Hover element by `@ref` |
| `vulpine_navigate` | Navigate to URL |
| `vulpine_click` | Click at coordinates |
| `vulpine_type` | Type text into focused element |
| `vulpine_screenshot` | Capture page screenshot |
| `vulpine_scroll` | Scroll the page |
| `vulpine_new_context` | Create isolated browser context |
| `vulpine_close_context` | Close browser context |
| `vulpine_get_ax_tree` | Get full accessibility tree (injection-filtered) |

---

## Testing

**283 Go tests** across 22 packages, all passing with race detector enabled.

```bash
go test -race ./...
```

---

## Build from Source

```bash
make fetch          # Download Firefox 146.0.1 source
make setup          # Extract + init git repo
make dir            # Apply patches + copy additions
make build          # Compile (~5 min on M1 with artifact builds)
make package-macos  # Create distributable
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
  <a href="https://vulpineos.com">vulpineos.com</a>
</p>
