<p align="center">
  <img src="assets/VulpineOSBanner.png" alt="VulpineOS" width="600">
</p>

<p align="center">
  <b>Operate Stealth-Aware Browser Agents from the Terminal</b>
</p>

<p align="center">
VulpineOS is the operating system for AI browser agents: a Firefox-based platform for managing native in-process agents with unique identities, browser-engine security, and TUI-first runtime controls.
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

```bash
curl -fsSL https://vulpineos.com/install | bash
```

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

VulpineOS builds on Camoufox's battle-tested stealth foundation (Firefox 146.0.1) and adds four security phases purpose-built for autonomous agents, a Go TUI for managing agents, and a native in-process agent runtime for deploying AI agents at scale.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                        VulpineOS                              │
│                                                              │
│  Vulpine Browser Engine (Firefox 146.0.1 + Vulpine patches)  │
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
│  ├── Bubbletea TUI (chat-first agent workbench)               │
│  ├── Identity Vault (SQLite — citizens, templates, sessions)  │
│  ├── Context Pool (pre-warm, recycle, memory limits)           │
│  ├── Orchestrator (spawn citizens + nomads, auto-release)      │
│  ├── Native Agent Runtime (streaming model/tool loop)         │
│  ├── Proxy Manager (geo-synced fingerprints, auto-rotation)    │
│  ├── MCP Server (42 tools via stdio)                           │
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
| **Agent Bus** | Inter-agent communication (ask, delegate, reply, notify) with user-controlled approval policies and full audit trail |
| **Cost Tracking** | Per-agent token usage and API cost tracking with budget limits. Built-in pricing for Claude, GPT-4o, Gemini. Alerts at configurable thresholds. |
| **Session Recording** | Record browser actions as timestamped timelines with a bounded per-agent in-memory window and sensitive action-data redaction. Export to JSON. Terminal-based replay at real speed. |
| **Proxy Rotation** | Auto-rotate proxies on rate limit, IP block, or time interval. Fingerprint re-synced on every rotation. 32-country locale map. |
| **Webhook Notifications** | HTTP webhooks for agent.completed/failed/paused/interrupted, rate_limit.detected, injection.detected, budget.alert/exceeded. Async delivery with secret verification and redacted delivery logs. |
| **Scripting DSL** | JSON scripting language for repetitive tasks without LLM calls. 8 actions: navigate, click, type, wait, extract, screenshot, set, if. Variable expansion with bounded script payloads, capped waits, and redacted operator-facing results. |
| **Kernel Watchdog** | Monitors Vulpine every 2s. On crash: fires callback, auto-restarts (up to 3 attempts), re-establishes Juggler connection. |
| **Token Optimization** | Viewport-aware DOM pruning, persistent page cache, delta encoding between snapshots, batch operations. |
| **Page Cache** | Saves and restores page state (URL, HTML, cookies, scroll, forms) across agent restarts. |
| **Rate Limit Monitor** | Pattern-based scanning of agent output for 429s, captchas, and blocks. Per-agent failure tracking. |
| **Structured Logging** | JSON structured logger with levels, component tags, and field chaining. |

---

## Go TUI: Agent Workbench

A terminal-based command center for managing AI agents, browser contexts, and identity profiles. The current operator flow is chat-first: stay in the conversation input, use `/` commands for actions, and click agents in the sidebar to switch context.

```text
+----------------+----------------------------------------------+
| System         | Conversation                                 |
| Kernel: running|                                              |
| Mode: GUI      | you  Find cheap flights to Tokyo in March    |
| Route: VULPINE |                                              |
| Window: VISIBLE| scout  Thinking...                           |
|                |                                              |
| Agents         |                                              |
| > Scout-1 act  |                                              |
|   Scout-2 new  | /agents                                      |
|   Research done| > Type a message...                          |
+----------------+----------------------------------------------+
```

Use `/` in an empty chat input to open the command palette. It includes common actions such as creating agents, opening settings, viewing logs, toggling trace output, showing or hiding the browser, and opening the `/agents` picker. Clicking an agent row in the sidebar selects that agent.

The native runtime keeps agent identity and browser guidance in the model prompt. New agents start on the assigned task immediately and restate their assigned runtime name, reducing drift toward stale persona state. The prompt forces exact action/result reporting and explicitly forbids claiming a browser action succeeded after an error, timeout, or incomplete result.
The system panel shows both the browser mode (`GUI` or `HEADLESS`) and the active browser route (`VULPINE`), so the operator can verify the runtime path without checking logs.
The TUI also shows the current browser window state (`VISIBLE`, `HIDDEN`, `HEADLESS`, or `N/A`) so browser visibility is diagnosable without checking logs.
Served mode also supports `--no-browser`, which keeps the TUI remote/control API available without launching a kernel.
Runtime startup failures land in the secret-redacted runtime audit stream, so startup problems appear in runtime views instead of only in raw log files.
After a newly created agent sends its first real reply, VulpineOS automatically snaps focus back to the chat box so the conversation is immediately writable again.
If the startup turn ends without an assistant reply, the first terminal agent status now also re-focuses chat so the input does not stay visually awake but functionally locked.
Newly created active agents now stay visually locked until they actually reply or finish startup, so the disabled banner does not disappear before chat input is really available.
When the macOS window-controller path fails, the toggle notice now preserves the underlying `osascript` error so permission problems and missing process targets are visible instead of being collapsed into a generic failure.
The trace command switches the center panel into a trace-only view of system tool events so browser/tool starts, completions, and failures are easy to inspect without mixing them into the full conversation stream.
If a tool fails and the agent still replies as if the task succeeded, VulpineOS now injects an explicit warning into that trace so false-success replies are visible immediately.
Non-zero command exits in tool results are classified as failures even when an upstream payload reports `status:"completed"`, so trace output stays aligned with the real action result.
Timeouts and incomplete tool results are classified separately from hard failures.
Tool-result summaries now preserve the exact tool-call context when available, so trace output says what action actually ran instead of falling back to generic `Tool completed: browser`.
The raw-log command opens the selected agent's operator-visible session log in the system viewer.

The agent list shows unread reply counts for non-selected agents so background work does not disappear while you are focused elsewhere.

On quit, VulpineOS pauses active agents before exiting so the next launch can resume saved sessions instead of dropping in-flight work.

Local TUI startup and runtime logs are written to `~/.vulpineos/logs/local-tui.log` so the terminal UI stays clean while the kernel, foxbridge, and native runtime initialize.

Live browser and MCP-browser integration tests are gated behind `VULPINEOS_RUN_LIVE=1` so the default `go test` and CI path stay hermetic even on machines that already have Vulpine installed. The native-agent reliability gauntlet is additionally gated by `VULPINE_AGENTCORE_GAUNTLET=1`; it uses local fixtures only and avoids verification-code, payment, and real challenge flows.

---

## Remote Access

The public runtime is TUI-only. `vulpineos --listen` starts the normal host TUI
and exposes the same control surface over WebSocket for another machine.
`vulpineos remote --url ...` launches the remote TUI client. `vulpineos serve`
runs the same backend headlessly for VPS and Docker deployments.

Remote clients pair with a short code by default. For automation and fixed
deployments, pass `--api-key` to use an explicit bearer access key instead.

---

## Foxbridge: CDP-to-Firefox Protocol Proxy

[Foxbridge](https://github.com/VulpineOS/foxbridge) is a standalone Go binary that translates Chrome DevTools Protocol (CDP) to Firefox's Juggler and WebDriver BiDi protocols. CDP-compatible tools can control Vulpine as if it were Chrome.

- **74/74 Puppeteer Juggler tests** passing
- **62/62 Puppeteer BiDi tests** passing
- Dual backend: `--backend juggler` (pipe) or `--backend bidi` (WebSocket)
- Fetch domain with request/response interception
- Embedded into VulpineOS startup so CDP-compatible tools can route through the same Vulpine process as the TUI
- The native agent runtime uses VulpineOS MCP/Juggler browser tools directly inside the assigned browser context

---

## Getting Started

### Install

The installer downloads the latest published VulpineOS CLI and matching
Vulpine browser bundle, installs `vulpineos` onto your PATH, and
configures the browser path under `~/.vulpineos/config.json`. Developers can
force a source build with `VULPINEOS_BUILD_FROM_SOURCE=1`.

```bash
curl -fsSL https://vulpineos.com/install | bash
vulpineos
```

The installer fails clearly if the latest release is missing the required CLI or
browser assets for your platform.

`https://vulpineos.com/install` should serve or redirect to the current
`install.sh` from the public VulpineOS repo.

### Installer Prerequisites

- Python 3, `unzip`, and either `curl` or `wget`
- Docker is optional and only needed when building or running the Vulpine-Box container

### Build From Source

```bash
git clone https://github.com/VulpineOS/VulpineOS.git
cd VulpineOS
go build -o vulpineos ./cmd/vulpineos
```

Source builds still need a Vulpine/Firefox-compatible browser binary. Either
complete the browser source build below, place a packaged Vulpine bundle next to
`./vulpineos`, or pass one explicitly with `--binary /path/to/vulpine`.

### Run

Local TUI:

```bash
vulpineos          # release install
./vulpineos        # source build
```

Host TUI with remote access:

```bash
./vulpineos --listen --port 8443 --api-key YOUR_KEY
```

Networked serve mode:

```bash
./vulpineos serve --host 0.0.0.0 --port 8443 --no-tls
```

Explicit access key:

```bash
./vulpineos serve --host 0.0.0.0 --port 8443 --no-tls --api-key YOUR_KEY
```

If `--api-key` is omitted in serve mode, VulpineOS prints a pairing code at
startup. Pass an explicit `--api-key` when you want a stable bearer key.

Remote TUI:

```bash
./vulpineos remote --url http://your-host:8443 --api-key YOUR_KEY
# or pair interactively:
./vulpineos remote --url http://your-host:8443
```

MCP server:

```bash
./vulpineos mcp
```

When no `--binary` flag is provided, VulpineOS prefers a repo-local
`camoufox-*/obj-*/dist` source build before falling back to a saved configured
binary or older installed copies.
`--binary` may point directly at the executable, at `Vulpine.app` or legacy
`Camoufox.app`, at a browser `dist` directory, or at the repo root containing
`camoufox-*/obj-*/dist`.

First launch opens a setup wizard to configure your AI provider (Anthropic, OpenAI, Google, xAI, and 27 more).

### Docker (Vulpine-Box)

```bash
export VULPINE_API_KEY=$(openssl rand -hex 32)
./scripts/check-vulpinebox.sh
docker compose up -d
vulpineos remote --url http://your-vps:8443 --api-key $VULPINE_API_KEY
```

`docker compose up -d` starts `vulpineos serve --binary ./browser/vulpine --port 8443 --no-tls`
inside the container. By default the bundled `docker-compose.yml` exposes plain HTTP on
port `8443`; add `VULPINE_TLS_CERT` and `VULPINE_TLS_KEY` plus mounted certificate files if
you want HTTPS/WSS.

The compose file requires `VULPINE_API_KEY` and will not fall back to a public default.
Use the same value with `vulpineos remote` when connecting from another machine.
Run `./scripts/check-vulpinebox.sh` before `docker compose up -d` to verify Docker,
the access key, and the required Linux browser artifact are ready.
If you want the full deployment notes, including required browser artifacts under
`dist/vulpine-linux/`, persistent volumes, and optional TLS, see
[docs.vulpineos.com/docker](https://docs.vulpineos.com/docker).

## Release notes

For public release gating and audit steps, see
[docs/release-checklist.md](docs/release-checklist.md) and
[docs/release-hygiene.md](docs/release-hygiene.md).

For a standardized AWS Mac builder runbook and wrapper scripts, see
[docs/ec2-mac-builder.md](docs/ec2-mac-builder.md).

---

## MCP Tools

VulpineOS exposes 42 tools via Model Context Protocol:

| Tool | Description |
|------|-------------|
| Core browser controls | Navigate, snapshot, click, type, screenshot, scroll, context lifecycle, and accessibility-tree access |
| Ref-based interactions | Click, type, and hover by `@ref` from optimized DOM snapshots. Snapshot profiles are `compact` (180 nodes/90 chars), `expanded` (360/160), and `full` (800/240); `retry:true` steps up after truncation. |
| Reliability tools | Wait, find, verify, screenshot diff, page-settled checks, select options, fill forms, page info, key press, clear input, form errors |
| Human-realism tools | Human-like click, scroll, and type timing |
| Annotated interaction | Annotated screenshots and click-by-label with `@N` labels |
| Extension surfaces | Credential metadata/autofill, audio capture, challenge governance, Sentinel signals, and mobile bridge hooks. The stock public build returns unavailable unless an extension provider is attached; credential, challenge, and URL metadata/errors are redacted at the MCP boundary. |
| Mobile bridge | List Android devices, start a local CDP bridge, and disconnect bridge sessions when the public mobile bridge provider is installed |

---

## Ecosystem

The public ecosystem is split by repository:

| Product | Source | Public Description |
|---------|--------|--------------------|
| VulpineOS | Open source | Browser-agent runtime, MCP tools, TUI, and remote server |
| Foxbridge | Open source | CDP-to-Firefox bridge for CDP clients |
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
