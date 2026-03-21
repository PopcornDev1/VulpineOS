<p align="center">
  <img src="https://i.imgur.com/enUBkXt.png" width="120">
</p>

<h1 align="center">VulpineOS</h1>

<h4 align="center">The first browser engine with AI agent security built into the C++ core</h4>

<p align="center">
VulpineOS is a sovereign agent runtime — a Firefox/Camoufox fork that makes AI agents undetectable, deterministic, and token-efficient at the browser engine level.
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

[Clover Labs](https://cloverlabs.ai) took over maintenance of Camoufox and extended it with per-context fingerprint spoofing — the ability to run multiple browser contexts, each with a completely unique hardware identity, in a single Firefox process. This work revealed that the same C++ interception techniques used for fingerprint rotation could solve the AI agent security problem: if you can intercept what the browser exposes to JavaScript, you can also intercept what the browser exposes to AI agents.

VulpineOS builds on Camoufox's battle-tested stealth foundation (Firefox 146.0.1) and adds four security phases purpose-built for autonomous agents, a Go TUI for managing agents, and full integration with [OpenClaw](https://github.com/anthropics/openclaw) for deploying AI agents at scale.

---

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                    VulpineOS                         │
│                                                     │
│  C++ Engine (Firefox 146.0.1 + Camoufox patches)    │
│  ├── Phase 1: Injection-Proof Accessibility Filter   │
│  ├── Phase 2: Deterministic Execution (Action-Lock)  │
│  ├── Phase 3: Token-Optimized DOM Export             │
│  └── Phase 4: Autonomous Trust-Warming               │
│                                                     │
│  Juggler Protocol (pipe FD 3/4)                      │
│  └── Telemetry Service + Trust Warming Service       │
│                                                     │
│  Go Runtime                                          │
│  ├── Bubbletea TUI (3-column agent workbench)        │
│  ├── Identity Vault (SQLite)                         │
│  ├── Context Pool (100+ agents per process)           │
│  ├── Proxy Manager (geo-synced fingerprints)          │
│  ├── OpenClaw Integration (MCP bridge)               │
│  └── Foxbridge CDP Proxy (Puppeteer compatibility)   │
│                                                     │
│  Docker: Vulpine-Box (one-click VPS deployment)      │
└─────────────────────────────────────────────────────┘
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

## Foxbridge: CDP-to-Firefox Protocol Proxy

[Foxbridge](https://github.com/PopcornDev1/foxbridge) is a standalone Go binary that translates Chrome DevTools Protocol (CDP) to Firefox's Juggler and WebDriver BiDi protocols. This lets any CDP tool — OpenClaw, Puppeteer, browser-use — control Camoufox as if it were Chrome.

- Full Puppeteer compatibility (~100% method coverage)
- Dual backend: `--backend juggler` (pipe) or `--backend bidi` (WebSocket)
- Fetch domain with request/response interception
- Integrated into VulpineOS startup — OpenClaw agents automatically use Camoufox

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
- **[Clover Labs](https://cloverlabs.ai)** — Maintains Camoufox, extended it with per-context fingerprint spoofing, WebGL database, and hardware-consistent identity generation.
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
