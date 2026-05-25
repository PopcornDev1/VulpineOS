# Playwright Integration Maintenance

Detailed Playwright/Juggler synchronization notes are maintained outside the
public repository.

The public contract is that VulpineOS exposes a compatible browser automation
route for the runtime, TUI, and MCP bridge. Low-level browser patch
sync steps, component-registration details, and version-specific migration notes
should stay in private maintainer documentation.

Public contributors proposing automation-protocol changes should open an issue
first so maintainers can confirm scope, review compatibility impact, and run the
appropriate private validation gates.
