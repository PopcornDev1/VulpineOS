# VulpineOS Agent Identity Skill

## Problem
The NanoClaw agent responds as a generic "NanoClaw agent" with no knowledge of VulpineOS — its purpose, capabilities, or the tools available to it. Every session starts as a clean slate.

## Solution
Create a `vulpineos-identity` skill in the NanoClaw container skills directory with two files:

- **`instructions.md`** — concise identity + capabilities fragment, symlinked into `.claude-fragments/` so it's loaded on every turn
- **`SKILL.md`** — full reference with browser workflows, scraping patterns, and host interaction details for on-demand reading

## Architecture

```
container/skills/vulpineos-identity/
  SKILL.md          → metadata + full reference (agent reads on demand)
  instructions.md   → concise identity fragment (symlinked → always loaded)

groups/vulpineos/.claude-fragments/
  skill-vulpineos-identity.md  → symlink → /app/skills/vulpineos-identity/instructions.md
```

## Sections

### Identity & Purpose
Defines VulpineOS as a Camoufox-based operator system for AI browser agents with four C++ security phases.

### Browser Automation (agent-browser CLI)
How to connect to the host Camoufox via `$AGENT_BROWSER_CDP`, navigate, snapshot, click, fill, extract data, and scrape.

### Host Interaction & Workspace
Three channels: Camoufox CDP, OneCLI gateway, workspace mounts. Explicitly forbidden actions listed.

### Multi-Agent Routing
Using `create_agent` and the agent bus for inter-agent communication.

## Implementation
1. Create `container/skills/vulpineos-identity/instructions.md`
2. Create `container/skills/vulpineos-identity/SKILL.md`
3. Add symlink in `groups/vulpineos/.claude-fragments/skill-vulpineos-identity.md`
4. Agent picks it up on next session
