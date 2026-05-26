import pytest


class TestAllBrowserPackages:
    """Integration tests for all three browser packages."""

    def test_import_vulpine_webkit(self):
        from vulpine_webkit import Browser
        assert Browser is not None

    def test_import_vulpine_otter(self):
        from vulpine_otter import Browser
        assert Browser is not None

    def test_import_vulpine_palemoon(self):
        from vulpine_palemoon import Browser
        assert Browser is not None


class TestWebKitBrowserComponents:
    """Test WebKit Browser instantiation with stealth=True."""

    def test_browser_stealth_creation(self):
        from vulpine_webkit import Browser
        browser = Browser(stealth=True)
        assert browser.stealth_enabled is True
        assert browser._stealth is not None

    def test_browser_has_stealth_engine(self):
        from vulpine_webkit import Browser
        browser = Browser(stealth=True)
        from vulpine_webkit.stealth import WebKitStealthEngine
        assert isinstance(browser._stealth, WebKitStealthEngine)

    def test_browser_has_cdp_client_type(self):
        from vulpine_webkit import Browser
        from vulpine_webkit.cdp import WebKitCDPClient
        from vulpine_webkit.cdp import WebKitCDPClient

    def test_browser_cdp_client_available(self):
        from vulpine_webkit import Browser
        browser = Browser(stealth=True)
        assert WebKitCDPClient is not None

    def test_browser_has_session_manager(self):
        from vulpine_webkit import Browser
        browser = Browser(stealth=True)
        from vulpine_webkit.session import SessionManager
        assert isinstance(browser._session, SessionManager)

    def test_browser_has_headless(self):
        from vulpine_webkit import Browser
        browser = Browser(stealth=True)
        assert browser.headless is True

    def test_browser_has_addons(self):
        from vulpine_webkit import Browser
        browser = Browser(stealth=True)
        from vulpine_webkit.addons import AddonManager
        assert isinstance(browser._addons, AddonManager)

    def test_generate_fingerprint(self):
        from vulpine_webkit import Browser
        browser = Browser(stealth=True)
        fp = browser._stealth.generate_fingerprint()
        assert "user_agent" in fp
        assert "browser" in fp
        assert "platform" in fp
        assert "engine" in fp
        assert "canvas" in fp
        assert "webgl" in fp


class TestOtterBrowserComponents:
    """Test Otter Browser instantiation with stealth=True."""

    def test_browser_stealth_creation(self):
        from vulpine_otter import Browser
        browser = Browser(stealth=True)
        assert browser.stealth_enabled is True
        assert browser._stealth is not None

    def test_browser_has_stealth_engine(self):
        from vulpine_otter import Browser
        browser = Browser(stealth=True)
        from vulpine_otter.stealth import OtterStealthEngine
        assert isinstance(browser._stealth, OtterStealthEngine)

    def test_browser_has_cdp_client_type(self):
        from vulpine_otter import Browser
        from vulpine_otter.cdp import OtterCDPClient

    def test_browser_cdp_client_available(self):
        from vulpine_otter import Browser
        browser = Browser(stealth=True)
        assert OtterCDPClient is not None

    def test_browser_has_session_manager(self):
        from vulpine_otter import Browser
        browser = Browser(stealth=True)
        from vulpine_otter.session import SessionManager
        assert isinstance(browser._session, SessionManager)

    def test_browser_has_headless(self):
        from vulpine_otter import Browser
        browser = Browser(stealth=True)
        assert browser.headless is True

    def test_browser_has_addons(self):
        from vulpine_otter import Browser
        browser = Browser(stealth=True)
        from vulpine_otter.addons import AddonManager
        assert isinstance(browser._addons, AddonManager)

    def test_generate_fingerprint(self):
        from vulpine_otter import Browser
        browser = Browser(stealth=True)
        fp = browser._stealth.generate_fingerprint()
        assert "user_agent" in fp
        assert "browser" in fp
        assert "platform" in fp
        assert "engine" in fp
        assert "canvas" in fp
        assert "webgl" in fp


class TestPaleMoonBrowserComponents:
    """Test PaleMoon Browser instantiation with stealth=True."""

    def test_browser_stealth_creation(self):
        from vulpine_palemoon import Browser
        browser = Browser(stealth=True)
        assert browser.stealth_enabled is True
        assert browser._stealth is not None

    def test_browser_has_stealth_engine(self):
        from vulpine_palemoon import Browser
        browser = Browser(stealth=True)
        from vulpine_palemoon.stealth import PaleMoonStealthEngine
        assert isinstance(browser._stealth, PaleMoonStealthEngine)

    def test_browser_has_cdp_client_type(self):
        from vulpine_palemoon import Browser
        from vulpine_palemoon.cdp import PaleMoonCDPClient

    def test_browser_cdp_client_available(self):
        from vulpine_palemoon import Browser
        browser = Browser(stealth=True)
        assert PaleMoonCDPClient is not None

    def test_browser_has_session_manager(self):
        from vulpine_palemoon import Browser
        browser = Browser(stealth=True)
        from vulpine_palemoon.session import SessionManager
        assert isinstance(browser._session, SessionManager)

    def test_browser_has_headless(self):
        from vulpine_palemoon import Browser
        browser = Browser(stealth=True)
        assert browser.headless is True

    def test_browser_has_addons(self):
        from vulpine_palemoon import Browser
        browser = Browser(stealth=True)
        from vulpine_palemoon.addons import AddonManager
        assert isinstance(browser._addons, AddonManager)

    def test_generate_fingerprint(self):
        from vulpine_palemoon import Browser
        browser = Browser(stealth=True)
        fp = browser._stealth.generate_fingerprint()
        assert "user_agent" in fp
        assert "browser" in fp
        assert "platform" in fp
        assert "engine" in fp
        assert "canvas" in fp
        assert "webgl" in fp