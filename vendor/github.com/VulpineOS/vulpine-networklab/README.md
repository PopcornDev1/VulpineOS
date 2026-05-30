# vulpine-networklab

Per-context TLS/network identity for VulpineOS browser agents.

Shapes outbound browser network fingerprints — JA3/JA4, ALPN, HTTP/2, HTTP/3, ECH/GREASE — so each agent context presents a coherent, independent network identity rather than sharing vanilla Firefox NSS defaults.

## Status

**Private — pre-alpha.** Design doc lives in `docs/plan.md`.

## Integration

NetworkLab exposes a minimal Go API/adapter that VulpineOS calls when creating or applying an agent browser context. See `docs/plan.md` for the API surface.

## License

All rights reserved. VulpineOS private module.
