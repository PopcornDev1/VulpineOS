# Per-Context Browser Identity

VulpineOS supports context-scoped browser identity so multiple browser contexts can run in one browser process without sharing the same operator-visible profile state.

This public note is intentionally high level. The public repository documents the runtime contract and orchestration boundary; low-level browser-engine patch notes, site-specific behavior, and implementation details are not published here.

## Public Contract

- Each agent can be assigned a browser context with its own persisted identity metadata.
- Runtime orchestration applies profile, locale, timezone, viewport, proxy, and related context settings before the agent starts work.
- Context identity is stored with the agent or citizen record in the local vault and survives pause/resume flows.
- Proxy rotation can update compatible geo settings for the assigned context.
- Public extension seams expose optional provider hooks while the stock open-source build remains safe when those providers are unavailable.

## Integration Points

- `internal/vault` stores agent, citizen, proxy, and fingerprint metadata.
- `internal/orchestrator` applies identity settings when spawning or resuming agents.
- `internal/proxy` keeps proxy geography and context identity aligned.
- `internal/remote` exposes high-level fingerprint get/regenerate controls to TUI clients.
- `internal/extensions` provides stable optional interfaces for private or external providers.

Implementation-specific browser patch notes should stay out of tracked public docs unless they have been reviewed and reduced to a compatibility-focused public description.
