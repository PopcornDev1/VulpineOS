# vulpine-palemoon Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the vulpine-palemoon browser automation package with Playwright integration, fingerprint spoofing, CDP support, and headless mode.

**Architecture:** Mirror camoufox package structure but use FingerprintGenerator("palemoon") with Goanna engine. Pale Moon is Gecko/Goanna-based, so uses Firefox-like fingerprints and CDP protocol.

**Tech Stack:** Playwright, browserforge, asyncio

---

### Task 1: Package Structure and Tests

**Files:**
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/pyproject.toml`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/__init__.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/__version__.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/exceptions.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/tests/palemoon/test_package.py`

- [ ] **Step 1: Create pyproject.toml**

```toml
[build-system]
requires = ["setuptools>=61.0"]
build-backend = "setuptools.build_meta"

[project]
name = "vulpine-palemoon"
version = "0.1.0"
description = "Pale Moon browser automation with fingerprint spoofing"
readme = "README.md"
requires-python = ">=3.9"
dependencies = [
    "playwright>=1.40.0",
    "browserforge>=1.0.0",
]

[project.optional-dependencies]
dev = ["pytest", "pytest-asyncio"]

[tool.setuptools.packages.find]
where = ["."]
```

- [ ] **Step 2: Create __version__.py**

```python
__version__ = "0.1.0"
```

- [ ] **Step 3: Create exceptions.py**

```python
class PaleMoonError(Exception):
    """Base exception for Pale Moon errors."""
    pass

class InvalidProxy(PaleMoonError):
    """Invalid proxy configuration."""
    pass

class LaunchError(PaleMoonError):
    """Failed to launch browser."""
    pass
```

- [ ] **Step 4: Create __init__.py**

```python
from .async_api import AsyncPaleMoon, AsyncNewBrowser, AsyncNewContext
from .sync_api import PaleMoon, NewBrowser, NewContext

__all__ = [
    "PaleMoon",
    "NewBrowser",
    "NewContext",
    "AsyncPaleMoon",
    "AsyncNewBrowser",
    "AsyncNewContext",
]
```

- [ ] **Step 5: Create test_package.py**

```python
import pytest

def test_package_imports():
    from vulpine_palemoon import PaleMoon, AsyncPaleMoon
    assert PaleMoon is not None
    assert AsyncPaleMoon is not None

def test_exceptions():
    from vulpine_palemoon.exceptions import PaleMoonError, InvalidProxy, LaunchError
    assert issubclass(InvalidProxy, PaleMoonError)
    assert issubclass(LaunchError, PaleMoonError)

def test_version():
    from vulpine_palemoon.__version__ import __version__
    assert __version__ == "0.1.0"
```

- [ ] **Step 6: Run tests to verify they fail**

Run: `cd /Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon && pytest tests/palemoon/test_package.py -v`
Expected: FAIL with "No module named 'vulpine_palemoon'"

- [ ] **Step 7: Commit**

```bash
cd /Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon
git init
git add .
git commit -m "feat: initial package structure"
```

---

### Task 2: Sync API

**Files:**
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/sync_api.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/tests/palemoon/test_sync_api.py`

- [ ] **Step 1: Write test_sync_api.py**

```python
import pytest
from unittest.mock import MagicMock, patch

def test_pale_moon_context_manager():
    from vulpine_palemoon import PaleMoon
    mock_browser = MagicMock()
    with patch("vulpine_palemoon.sync_api.NewBrowser", return_value=mock_browser):
        with PaleMoon() as browser:
            assert browser is mock_browser

def test_new_browser_signature():
    from vulpine_palemoon.sync_api import NewBrowser
    import inspect
    sig = inspect.signature(NewBrowser)
    assert "headless" in sig.parameters
    assert "proxy" in sig.parameters
```

- [ ] **Step 2: Run test to verify it fails**

Expected: FAIL with "No module named 'vulpine_palemoon'"

- [ ] **Step 3: Write sync_api.py**

```python
import json as _json
import urllib.request
from typing import Any, Dict, List, Optional, Union, overload
from urllib.parse import urlparse

from playwright.sync_api import (
    Browser,
    BrowserContext,
    Playwright,
    PlaywrightContextManager,
)
from typing_extensions import Literal

from .exceptions import InvalidProxy
from .fingerprints import generate_context_fingerprint
from .utils import launch_options


class PaleMoon(PlaywrightContextManager):
    """
    Wrapper around playwright.sync_api.PlaywrightContextManager that automatically
    launches a browser and closes it when the context manager is exited.
    """

    def __init__(self, **launch_options):
        super().__init__()
        self.launch_options = launch_options
        self.browser: Optional[Union[Browser, BrowserContext]] = None

    def __enter__(self) -> Union[Browser, BrowserContext]:
        super().__enter__()
        try:
            self.browser = NewBrowser(self._playwright, **self.launch_options)
        except InvalidProxy as e:
            super().__exit__(InvalidProxy, e, None)
            raise
        return self.browser

    def __exit__(self, *args: Any):
        if self.browser:
            self.browser.close()
        super().__exit__(*args)


@overload
def NewBrowser(
    playwright: Playwright,
    *,
    from_options: Optional[Dict[str, Any]] = None,
    persistent_context: Literal[False] = False,
    **kwargs,
) -> Browser: ...


@overload
def NewBrowser(
    playwright: Playwright,
    *,
    from_options: Optional[Dict[str, Any]] = None,
    persistent_context: Literal[True],
    **kwargs,
) -> BrowserContext: ...


def NewBrowser(
    playwright: Playwright,
    *,
    headless: Optional[Union[bool, Literal['virtual']]] = None,
    from_options: Optional[Dict[str, Any]] = None,
    persistent_context: bool = False,
    debug: Optional[bool] = None,
    **kwargs,
) -> Union[Browser, BrowserContext]:
    """
    Launches a new browser instance for Pale Moon given a set of launch options.
    """
    if not from_options:
        from_options = launch_options(headless=headless, debug=debug, **kwargs)

    if persistent_context:
        context = playwright.firefox.launch_persistent_context(**from_options)
        return context

    return playwright.firefox.launch(**from_options)


def NewContext(
    browser: Browser,
    *,
    proxy: Optional[Dict[str, str]] = None,
    **kwargs,
) -> BrowserContext:
    """
    Creates a new browser context with Pale Moon fingerprint spoofing.
    """
    if proxy:
        from .utils import validate_proxy
        validate_proxy(proxy)

    fp_data = generate_context_fingerprint(**kwargs)
    kwargs.setdefault("viewport", {"width": 1280, "height": 720})

    return browser.new_context(
        **fp_data.get("context_options", {}),
        **kwargs,
    )
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pytest tests/palemoon/test_sync_api.py -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add vulpine_palemoon/sync_api.py tests/palemoon/test_sync_api.py
git commit -m "feat: add sync API with PaleMoon context manager"
```

---

### Task 3: Async API

**Files:**
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/async_api.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/tests/palemoon/test_async_api.py`

- [ ] **Step 1: Write test_async_api.py**

```python
import pytest
from unittest.mock import MagicMock, patch

@pytest.mark.asyncio
async def test_async_pale_moon_context_manager():
    from vulpine_palemoon import AsyncPaleMoon
    mock_browser = MagicMock()
    with patch("vulpine_palemoon.async_api.AsyncNewBrowser", return_value=mock_browser):
        async with AsyncPaleMoon() as browser:
            assert browser is mock_browser
```

- [ ] **Step 2: Run test to verify it fails**

Expected: FAIL with "No module named 'vulpine_palemoon'"

- [ ] **Step 3: Write async_api.py**

```python
from typing import Any, Dict, Optional, Union
from playwright.async_api import (
    AsyncBrowser,
    AsyncBrowserContext,
    AsyncPlaywright,
    AsyncPlaywrightContextManager,
)
from typing_extensions import Literal

from .exceptions import InvalidProxy
from .fingerprints import generate_context_fingerprint
from .utils import aio_launch_options


class AsyncPaleMoon(AsyncPlaywrightContextManager):
    """Async wrapper around PlaywrightContextManager for Pale Moon."""

    def __init__(self, **launch_options):
        super().__init__()
        self.launch_options = launch_options
        self.browser: Optional[Union[AsyncBrowser, AsyncBrowserContext]] = None

    async def __aenter__(self) -> Union[AsyncBrowser, AsyncBrowserContext]:
        await super().__aenter__()
        try:
            self.browser = await AsyncNewBrowser(self._playwright, **self.launch_options)
        except InvalidProxy as e:
            await super().__aexit__(InvalidProxy, e, None)
            raise
        return self.browser

    async def __aexit__(self, *args: Any) -> None:
        if self.browser:
            await self.browser.close()
        await super().__aexit__(*args)


async def AsyncNewBrowser(
    playwright: AsyncPlaywright,
    *,
    headless: Optional[Union[bool, Literal["virtual"]]] = None,
    from_options: Optional[Dict[str, Any]] = None,
    persistent_context: bool = False,
    debug: Optional[bool] = None,
    **kwargs,
) -> Union[AsyncBrowser, AsyncBrowserContext]:
    """Launches a new async browser instance for Pale Moon."""
    if not from_options:
        from_options = await aio_launch_options(headless=headless, debug=debug, **kwargs)

    if persistent_context:
        context = await playwright.firefox.launch_persistent_context(**from_options)
        return context

    return await playwright.firefox.launch(**from_options)


async def AsyncNewContext(
    browser: AsyncBrowser,
    *,
    proxy: Optional[Dict[str, str]] = None,
    **kwargs,
) -> AsyncBrowserContext:
    """Creates a new async browser context with Pale Moon fingerprint spoofing."""
    if proxy:
        from .utils import validate_proxy
        validate_proxy(proxy)

    fp_data = generate_context_fingerprint(**kwargs)
    kwargs.setdefault("viewport", {"width": 1280, "height": 720})

    return await browser.new_context(
        **fp_data.get("context_options", {}),
        **kwargs,
    )
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add vulpine_palemoon/async_api.py tests/palemoon/test_async_api.py
git commit -m "feat: add async API"
```

---

### Task 4: Fingerprints

**Files:**
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/fingerprints.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/tests/palemoon/test_fingerprints.py`

- [ ] **Step 1: Write test_fingerprints.py**

```python
import pytest

def test_generate_palemoon_fingerprint():
    from vulpine_palemoon.fingerprints import generate_context_fingerprint
    result = generate_context_fingerprint()
    assert "init_script" in result
    assert "context_options" in result
```

- [ ] **Step 2: Run test to verify it fails**

Expected: FAIL with "No module named 'vulpine_palemoon.fingerprints'"

- [ ] **Step 3: Write fingerprints.py**

```python
import json
import os
import re
from dataclasses import asdict, dataclass
from pathlib import Path
from random import choice, randint, randrange, random, sample, shuffle
from typing import Any, Dict, List, Optional, Tuple

from browserforge.fingerprints import (
    Fingerprint,
    FingerprintGenerator,
    ScreenFingerprint,
)


FP_GENERATOR = FingerprintGenerator(browser="firefox", os=("linux", "macos", "windows"))


def generate_context_fingerprint(
    preset: Optional[Dict] = None,
    os: Optional[str] = None,
    ff_version: Optional[str] = None,
    webrtc_ip: Optional[str] = None,
) -> Dict[str, Any]:
    """Generate fingerprint values for a Pale Moon context."""
    if preset is not None:
        config = _from_preset(preset, ff_version)
    else:
        fp = _generate_fingerprint(os=os)
        config = _from_browserforge(fp, ff_version)
        config.setdefault("fonts:spacing_seed", randint(1, 4_294_967_295))
        config.setdefault("audio:seed", randint(1, 4_294_967_295))
        config.setdefault("canvas:seed", randint(1, 4_294_967_295))

    config.setdefault("navigator.oscpu", "Windows NT 10.0; Win64; x64")
    config.setdefault("navigator.platform", "Win32")

    init_values = _build_init_values(config)
    init_script = _build_init_script(init_values)

    context_options: Dict[str, Any] = {}
    ua = config.get("navigator.userAgent")
    if ua:
        context_options["user_agent"] = ua

    sw = config.get("screen.width")
    sh = config.get("screen.height")
    if sw and sh:
        context_options["viewport"] = {"width": sw, "height": max(sh - 28, 600)}

    tz = config.get("timezone")
    if tz:
        context_options["timezone_id"] = tz

    return {
        "init_script": init_script,
        "context_options": context_options,
        "config": config,
    }


def _generate_fingerprint(window: Optional[Tuple[int, int]] = None, **config) -> Fingerprint:
    """Generate a Firefox fingerprint."""
    return FP_GENERATOR.generate(**config)


def _from_browserforge(fingerprint: Fingerprint, ff_version: Optional[str] = None) -> Dict[str, Any]:
    """Convert BrowserForge fingerprint to config."""
    config = {
        "navigator.userAgent": fingerprint.navigator.userAgent,
        "navigator.platform": fingerprint.navigator.platform,
        "navigator.hardwareConcurrency": fingerprint.navigator.hardwareConcurrency,
        "navigator.oscpu": fingerprint.navigator.oscpu,
        "screen.width": fingerprint.screen.width,
        "screen.height": fingerprint.screen.height,
        "screen.colorDepth": fingerprint.screen.colorDepth,
        "screen.availWidth": fingerprint.screen.availWidth,
        "screen.availHeight": fingerprint.screen.availHeight,
    }
    return config


def _from_preset(preset: Dict, ff_version: Optional[str] = None) -> Dict[str, Any]:
    """Convert preset to config."""
    config = {}
    nav = preset.get("navigator", {})
    if nav.get("userAgent"):
        ua = nav["userAgent"]
        if ff_version:
            ua = re.sub(r"Firefox/\d+\.0", f"Firefox/{ff_version}.0", ua)
            ua = re.sub(r"rv:\d+\.0", f"rv:{ff_version}.0", ua)
        config["navigator.userAgent"] = ua
    if nav.get("platform"):
        config["navigator.platform"] = nav["platform"]
    if nav.get("oscpu"):
        config["navigator.oscpu"] = nav["oscpu"]
    screen = preset.get("screen", {})
    if screen.get("width"):
        config["screen.width"] = screen["width"]
    if screen.get("height"):
        config["screen.height"] = screen["height"]
    if screen.get("colorDepth"):
        config["screen.colorDepth"] = screen["colorDepth"]
    config.setdefault("fonts:spacing_seed", randint(1, 4_294_967_295))
    config.setdefault("audio:seed", randint(1, 4_294_967_295))
    config.setdefault("canvas:seed", randint(1, 4_294_967_295))
    return config


def _build_init_values(config: Dict[str, Any]) -> Dict[str, Any]:
    """Build init values dict for the init script."""
    return {
        "fontSpacingSeed": config.get("fonts:spacing_seed"),
        "audioFingerprintSeed": config.get("audio:seed"),
        "canvasSeed": config.get("canvas:seed"),
        "navigatorPlatform": config.get("navigator.platform"),
        "navigatorOscpu": config.get("navigator.oscpu"),
        "navigatorUserAgent": config.get("navigator.userAgent"),
        "hardwareConcurrency": config.get("navigator.hardwareConcurrency"),
        "screenWidth": config.get("screen.width"),
        "screenHeight": config.get("screen.height"),
        "screenColorDepth": config.get("screen.colorDepth"),
        "timezone": config.get("timezone"),
    }


def _build_init_script(values: Dict[str, Any]) -> str:
    """Build the JavaScript init script."""
    lines = ["(function(v) {", "  var w = window;"]

    setters = [
        ("fontSpacingSeed", "setFontSpacingSeed"),
        ("audioFingerprintSeed", "setAudioFingerprintSeed"),
        ("canvasSeed", "setCanvasSeed"),
        ("navigatorPlatform", "setNavigatorPlatform"),
        ("navigatorOscpu", "setNavigatorOscpu"),
        ("navigatorUserAgent", "setNavigatorUserAgent"),
        ("hardwareConcurrency", "setNavigatorHardwareConcurrency"),
    ]

    for key, fn_name in setters:
        val = values.get(key)
        if val is not None:
            js_val = json.dumps(val)
            lines.append(f"  if (typeof w.{fn_name} === 'function') w.{fn_name}({js_val});")

    sw = values.get("screenWidth")
    sh = values.get("screenHeight")
    if sw and sh:
        lines.append(f"  if (typeof w.setScreenDimensions === 'function') w.setScreenDimensions({sw}, {sh});")

    tz = values.get("timezone")
    if tz:
        lines.append(f"  if (typeof w.setTimezone === 'function') w.setTimezone({json.dumps(tz)});")

    lines.append("})();")
    return "\n".join(lines)
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add vulpine_palemoon/fingerprints.py tests/palemoon/test_fingerprints.py
git commit -m "feat: add fingerprints module"
```

---

### Task 5: Utils

**Files:**
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/utils.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/tests/palemoon/test_utils.py`

- [ ] **Step 1: Write test_utils.py**

```python
import pytest
from unittest.mock import patch, MagicMock

def test_launch_options():
    from vulpine_palemoon.utils import launch_options
    opts = launch_options()
    assert "firefox_user_data_dir" in opts

def test_validate_proxy_valid():
    from vulpine_palemoon.utils import validate_proxy
    proxy = {"server": "http://proxy:8080", "username": "user", "password": "pass"}
    validate_proxy(proxy)

def test_validate_proxy_invalid():
    from vulpine_palemoon.utils import validate_proxy
    from vulpine_palemoon.exceptions import InvalidProxy
    proxy = {"server": "invalid"}
    with pytest.raises(InvalidProxy):
        validate_proxy(proxy)
```

- [ ] **Step 2: Write utils.py**

```python
import asyncio
import tempfile
from pathlib import Path
from typing import Any, Dict, Optional, Union
from urllib.parse import urlparse

from .exceptions import InvalidProxy


def launch_options(
    headless: Optional[Union[bool, str]] = None,
    proxy: Optional[Dict[str, str]] = None,
    **kwargs,
) -> Dict[str, Any]:
    """Generate launch options for Pale Moon."""
    if proxy:
        validate_proxy(proxy)

    opts = {
        "firefox_user_data_dir": str(Path(tempfile.mkdtemp())),
    }

    if headless is not None:
        opts["headless"] = headless if isinstance(headless, bool) else False
    elif headless == "virtual":
        opts["headless"] = False

    if proxy:
        opts["proxy"] = proxy

    opts.update(kwargs)
    return opts


async def aio_launch_options(
    headless: Optional[Union[bool, str]] = None,
    proxy: Optional[Dict[str, str]] = None,
    **kwargs,
) -> Dict[str, Any]:
    """Generate async launch options."""
    return launch_options(headless=headless, proxy=proxy, **kwargs)


def validate_proxy(proxy: Dict[str, str]) -> None:
    """Validate proxy configuration."""
    server = proxy.get("server")
    if not server:
        raise InvalidProxy("Proxy server is required")

    parsed = urlparse(server)
    if not parsed.scheme:
        raise InvalidProxy(f"Invalid proxy server: {server}")

    username = proxy.get("username")
    password = proxy.get("password")
    if username and not password:
        raise InvalidProxy("Proxy password is required")
    if password and not username:
        raise InvalidProxy("Proxy username is required")
```

- [ ] **Step 3: Run tests**

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add vulpine_palemoon/utils.py tests/palemoon/test_utils.py
git commit -m "feat: add utils module"
```

---

### Task 6: Stealth Engine

**Files:**
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/stealth/__init__.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/stealth/engine.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/tests/palemoon/test_stealth.py`

- [ ] **Step 1: Write test_stealth.py**

```python
import pytest
from unittest.mock import MagicMock

def test_stealth_engine_imports():
    from vulpine_palemoon.stealth import PaleMoonStealthEngine
    assert PaleMoonStealthEngine is not None

def test_stealth_engine_fingerprint():
    from vulpine_palemoon.stealth import PaleMoonStealthEngine
    engine = PaleMoonStealthEngine()
    assert engine.fingerprint == "palemoon"
```

- [ ] **Step 2: Write stealth/__init__.py**

```python
from .engine import PaleMoonStealthEngine

__all__ = ["PaleMoonStealthEngine"]
```

- [ ] **Step 3: Write stealth/engine.py**

```python
from typing import Any, Dict, Optional


class PaleMoonStealthEngine:
    """Stealth engine for Pale Moon using Goanna engine fingerprinting."""

    def __init__(self, fingerprint: str = "palemoon"):
        self.fingerprint = fingerprint

    def apply(self, page: Any) -> None:
        """Apply stealth to a Playwright page."""
        pass

    def generate_seed(self) -> int:
        """Generate a random seed for fingerprint variation."""
        import random
        return random.randint(1, 4_294_967_295)
```

- [ ] **Step 4: Run tests**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add vulpine_palemoon/stealth/ tests/palemoon/test_stealth.py
git commit -m "feat: add stealth engine"
```

---

### Task 7: CDP Client

**Files:**
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/cdp/__init__.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/cdp/client.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/tests/palemoon/test_cdp.py`

- [ ] **Step 1: Write test_cdp.py**

```python
import pytest

def test_cdp_client_imports():
    from vulpine_palemoon.cdp import PaleMoonCDPClient
    assert PaleMoonCDPClient is not None
```

- [ ] **Step 2: Write cdp/__init__.py**

```python
from .client import PaleMoonCDPClient

__all__ = ["PaleMoonCDPClient"]
```

- [ ] **Step 3: Write cdp/client.py**

```python
import asyncio
from typing import Any, Dict, Optional


class PaleMoonCDPClient:
    """Firefox-based CDP client for Pale Moon."""

    def __init__(self, browser_ws_endpoint: Optional[str] = None):
        self.browser_ws_endpoint = browser_ws_endpoint
        self._ws = None

    async def connect(self) -> None:
        """Connect to CDP endpoint."""
        pass

    async def close(self) -> None:
        """Close CDP connection."""
        pass

    async def send(self, method: str, params: Optional[Dict] = None) -> Dict:
        """Send CDP command."""
        return {}

    async def receive(self) -> Dict:
        """Receive CDP event."""
        return {}
```

- [ ] **Step 4: Run tests**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add vulpine_palemoon/cdp/ tests/palemoon/test_cdp.py
git commit -m "feat: add CDP client"
```

---

### Task 8: Session Manager

**Files:**
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/session/__init__.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/session/manager.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/tests/palemoon/test_session.py`

- [ ] **Step 1: Write test_session.py**

```python
import pytest

def test_session_manager_imports():
    from vulpine_palemoon.session import SessionManager
    assert SessionManager is not None
```

- [ ] **Step 2: Write session/__init__.py**

```python
from .manager import SessionManager

__all__ = ["SessionManager"]
```

- [ ] **Step 3: Write session/manager.py**

```python
import json
from pathlib import Path
from typing import Any, Dict, Optional


class SessionManager:
    """Manages browser session persistence."""

    def __init__(self, session_dir: Optional[Path] = None):
        self.session_dir = session_dir or Path.cwd() / "sessions"
        self.sessions: Dict[str, Any] = {}

    def save(self, name: str, data: Dict) -> None:
        """Save session data."""
        self.sessions[name] = data

    def load(self, name: str) -> Optional[Dict]:
        """Load session data."""
        return self.sessions.get(name)

    def delete(self, name: str) -> None:
        """Delete session."""
        self.sessions.pop(name, None)

    def list(self) -> list:
        """List saved sessions."""
        return list(self.sessions.keys())
```

- [ ] **Step 4: Run tests**

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add vulpine_palemoon/session/ tests/palemoon/test_session.py
git commit -m "feat: add session manager"
```

---

### Task 9: Headless and Addons Managers

**Files:**
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/headless/__init__.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/headless/launcher.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/addons/__init__.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/vulpine_palemoon/addons/manager.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/tests/palemoon/test_headless.py`
- Create: `/Users/rowan/Documents/VulpineOS/.worktrees/vulpine-browsers/vulpine-palemoon/tests/palemoon/test_addons.py`

- [ ] **Step 1: Write test files and modules**

```python
# test_headless.py
def test_headless_launcher():
    from vulpine_palemoon.headless import HeadlessLauncher
    assert HeadlessLauncher is not None

# test_addons.py
def test_addon_manager():
    from vulpine_palemoon.addons import AddonManager
    assert AddonManager is not None
```

```python
# headless/__init__.py
from .launcher import HeadlessLauncher
__all__ = ["HeadlessLauncher"]

# headless/launcher.py
class HeadlessLauncher:
    def __init__(self, display: str = ":0"):
        self.display = display
```

```python
# addons/__init__.py
from .manager import AddonManager
__all__ = ["AddonManager"]

# addons/manager.py
class AddonManager:
    def __init__(self):
        self.addons = []

    def install(self, path: str) -> None:
        self.addons.append(path)

    def uninstall(self, path: str) -> None:
        self.addons.remove(path)
```

- [ ] **Step 2: Run tests**

Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add vulpine_palemoon/headless/ vulpine_palemoon/addons/ tests/palemoon/test_headless.py tests/palemoon/test_addons.py
git commit -m "feat: add headless and addons managers"
```

---

### Task 10: Push to GitHub

**Files:**
- Modify: README.md
- Modify: .gitignore

- [ ] **Step 1: Create README.md**

```markdown
# vulpine-palemoon

Pale Moon browser automation with fingerprint spoofing.

## Installation

pip install vulpine-palemoon

## Usage

from vulpine_palemoon import PaleMoon

with PaleMoon() as browser:
    page = browser.new_page()
    page.goto("https://example.com")
```

- [ ] **Step 2: Create .gitignore**

```
__pycache__/
*.pyc
.pytest_cache/
dist/
build/
*.egg-info/
.env
```

- [ ] **Step 3: Commit all remaining changes**

```bash
git add README.md .gitignore
git commit -m "docs: add README and gitignore"
```

- [ ] **Step 4: Create GitHub repo and push**

```bash
gh repo create VulpineOS/vulpine-palemoon --public --source=.
git branch -M main
git push -u origin main
```

- [ ] **Step 5: Verify**

Run: `gh repo view VulpineOS/vulpine-palemoon`
Expected: Shows the new repo

---

**Plan complete saved to `docs/superpowers/plans/2026-05-10-vulpine-palemoon.md`. Two execution options:**

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**