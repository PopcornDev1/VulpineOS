# Vulpine Browser Automation - Design Specification

**Created:** 2026-05-10
**Status:** Approved

## Project Overview

Create three independent browser automation libraries for VulpineOS agents, each providing full stealth and bot-detection evasion capabilities. These expand agent browsing options beyond Firefox-based camoufox.

## Target Browsers

1. **vulpine-webkit** — GNOME Web (WebKitGTK)
2. **vulpine-otter** — Otter Browser (WebKit)
3. **vulpine-palemoon** — Pale Moon (Goanna engine)

## Architecture

### Organization Structure

```
GitHub: VulpineOS/
├── shared/                    # Common utilities (fingerprinting, geo spoofing, CDP client base)
├── vulpine-webkit/           # GNOME Web implementation
├── vulpine-otter/            # Otter Browser implementation
└── vulpine-palemoon/         # Pale Moon implementation
```

### Package Structure (per browser)

```
vulpine_<browser>/
├── __init__.py              # Public API exports
├── sync_api.py              # Synchronous API
├── async_api.py             # Asynchronous API
├── stealth/
│   ├── __init__.py
│   ├── fingerprint.py       # Fingerprint randomization
│   ├── canvas.py            # Canvas noise injection
│   ├── webgl.py             # WebGL spoofing
│   ├── audio.py             # AudioContext fingerprint blocking
│   └── fonts.py             # Font enumeration spoofing
├── cdp/
│   ├── __init__.py
│   ├── client.py            # CDP protocol client
│   ├── commands.py          # CDP command wrappers
│   └── events.py            # Event handlers
├── session/
│   ├── __init__.py
│   ├── profile.py           # Profile management
│   ├── container.py         # Cookie container isolation
│   └── manager.py           # Multi-instance management
├── geo/
│   ├── __init__.py
│   └── spoofer.py           # Geolocation spoofing
├── addons/
│   ├── __init__.py
│   └── manager.py           # Extension loading
├── headless/
│   ├── __init__.py
│   ├── display.py           # Virtual display management
│   ├── screenshot.py        # Screenshot capture
│   └── interaction.py       # Element click/form fill
├── fingerprints.py           # Fingerprint presets and configs
├── exceptions.py            # Custom exceptions
└── utils.py                 # Utility functions
```

## Core Components

### 1. StealthEngine

**Responsibilities:**
- User-Agent string randomization from realistic pools
- Canvas fingerprint noise injection
- WebGL renderer/vendor spoofing
- AudioContext fingerprint blocking
- Font enumeration spoofing
- Plugin/MIME type randomization
- Screen resolution/height spoofing
- Platform/OS spoofing

**Implementation:**
- Each browser implements `StealthEngine` interface
- WebKit: Use WebKitGTK settings + JavaScript injection
- Pale Moon: Use Goanna preferences + Chrome-style overrides

### 2. CDPClient

**Responsibilities:**
- WebDriver/CDP protocol connection
- DOM inspection and manipulation
- Network request interception
- Console log capture
- JavaScript execution
- Cookie/session management
- Screenshot capture

**Implementation:**
- WebKit: WebKitGTK's WebDriver (cdp) or WebKit's Inspector protocol
- Pale Moon: Firefox's remote protocol (similar to Chrome DevTools Protocol)

### 3. SessionManager

**Responsibilities:**
- Create isolated browser profiles
- Manage multiple concurrent browser instances
- Cookie container isolation
- Browser state persistence

### 4. GeoSpoofer

**Responsibilities:**
- Override navigator.geolocation
- Spoof timezone via Date/timezone settings
- Language/locale spoofing
- IP-based geolocation matching

### 5. AddonManager

**Responsibilities:**
- Load browser extensions
- Manage extension state
- Inject content scripts

### 6. HeadlessController

**Responsibilities:**
- Virtual display (Xvfb/Display) management
- Screenshot capture
- Element location and interaction
- Form filling

## API Design

Each package exposes:

```python
# Synchronous
from vulpine_<browser> import Browser

browser = Browser(
    headless=True,
    stealth=True,
    geolocation={"latitude": 40.7128, "longitude": -74.0060},
    timezone="America/New_York",
    language="en-US",
)
browser.get("https://example.com")
browser.click("#submit-button")
screenshot = browser.screenshot()

# Asynchronous
from vulpine_<browser> import AsyncBrowser

async with AsyncBrowser() as browser:
    await browser.goto("https://example.com")
```

## Error Handling

| Exception | Description |
|-----------|-------------|
| `BrowserNotFoundError` | Browser executable not found |
| `ConnectionError` | Cannot connect to browser |
| `StealthViolationError` | Bot detection triggered |
| `UnsupportedPlatformError` | Platform not supported |
| `CDPError` | CDP command failed |

## Testing Strategy

1. **Unit tests**: Fingerprint randomization, spoofing logic
2. **Integration tests**: CDP connection, browser launch
3. **E2E tests**: Run against known detection services (e.g., bot detection APIs)

## Dependencies

Each package depends on:
- `wsproto` — WebSocket for CDP
- `requests` or `aiohttp` — HTTP for WebDriver
- `Pillow` — Screenshot processing
- Browser-specific: WebKitGTK, Goanna binary

## Cross-Platform Support

- **Linux**: Full support, primary target
- **macOS**: Full support, use system display or Xvfb
- **Windows**: Not supported (browsers primarily Linux/macOS)

## Success Criteria

Each implementation provides:
1. Stealth mode with fingerprint randomization
2. CDP/WebDriver control
3. Multi-instance isolation
4. Geolocation spoofing
5. Extension support
6. Headless operation with screenshots
7. Sync and async APIs
8. Matching detection evasion capability to camoufox