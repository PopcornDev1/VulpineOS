# Security Policy

VulpineOS is a hardened browser-agent runtime. We take security reports
seriously. This document mirrors the policy at
<https://vulpineos.com/security>.

## Reporting a vulnerability

Two channels, pick whichever fits the report:

- **Email:** <security@vulpineos.com>. For high-impact issues, prefix the
  subject line with `VULN:` so we can triage faster. We acknowledge within
  2 business days.
- **GitHub Private Vulnerability Reporting:** open a private advisory at
  <https://github.com/VulpineOS/VulpineOS/security/advisories/new>. The
  advisory stays private until we publish a fix; you and the maintainer
  collaborate on the draft inside the repo.

For sensitive reports, request a PGP key by emailing
<security@vulpineos.com> with the subject `PGP REQUEST`. We will share the
current public key.

## In scope

- The VulpineOS runtime in this repository.
- The companion repositories: `VulpineOS/foxbridge`, `VulpineOS/vulpine-mark`,
  `VulpineOS/mobilebridge`, `VulpineOS/vulpineos-docs`.
- The web surfaces: `vulpineos.com`, `auth.vulpineos.com`, `admin.vulpineos.com`.

## Out of scope

- Third-party services we depend on (Vercel, Supabase, Google) — please
  report those upstream.
- Self-hosted instances of the runtime that you operate on your own
  infrastructure.
- Reports based purely on best-practice headers (HSTS preload, CSP nonces,
  SPF / DKIM strictness) without a demonstrable security impact.
- Volumetric denial-of-service, social engineering of staff, and physical
  access attempts.
- Vulnerabilities in versions that are no longer the latest published
  release.

## Safe harbor

If you make a good-faith effort to comply with this policy — do not access
more data than necessary to demonstrate the issue, do not exfiltrate data,
do not publicly disclose the issue before we have had a reasonable chance
to fix it, and do not disrupt other users — we will:

- Not pursue legal action against you for that research.
- Work with you on disclosure timing and credit.
- Treat your report as a private security advisory until a fix is shipped
  and users have had time to update.

## Response expectations

| Stage | Target |
| --- | --- |
| Acknowledgement | 2 business days |
| Initial assessment | 5 business days |
| Patch + advisory window | 30 days for high-severity, faster where impact justifies it |
| Public credit (if requested) | Coordinated via the GitHub Security Advisory flow, including CVE assignment |

## Abuse, not vulnerabilities

If you are reporting someone using VulpineOS to violate our
[Acceptable Use Policy](https://vulpineos.com/legal/acceptable-use), email
<abuse@vulpineos.com> instead. That is a different inbox with different
escalation paths.

## Supported versions

We support the **latest published release** on the `main` branch. Older
tagged releases do not receive security backports unless an issue is
deemed critical and the upstream Vulpine browser / Firefox fix is available.
