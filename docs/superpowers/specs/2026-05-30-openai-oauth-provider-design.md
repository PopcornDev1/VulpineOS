# VulpineOS OpenAI OAuth Provider

## Problem

VulpineOS currently requires an OpenRouter API key for LLM access. Users with a ChatGPT Plus/Pro subscription have no way to use their existing subscription to power VulpineOS agents. API keys require separate billing and are not covered by the $20/month ChatGPT plan.

## Solution

Add a standalone device-code OAuth flow that authenticates against OpenAI's Codex OAuth endpoints, stores tokens on the host, and mounts them into the NanoClaw container. A new TypeScript provider speaks the ChatGPT backend API directly (`chatgpt.com/backend-api/codex/responses`) using the OAuth access token.

## Architecture

```
Host (Go CLI):
  vulpine auth login --provider openai
    1. Device-code flow against auth.openai.com
    2. Tokens stored in ~/.vulpineos/credentials/openai-oauth.json
    3. vulpine auth logout --provider openai removes the file

  Container config:
    - Mounts ~/.vulpineos/credentials/ → /vulpine/credentials/ (ro)
    - Sets OPENCODE_PROVIDER=openai-oauth

Container (TypeScript provider):
  OPENCODE_PROVIDER=openai-oauth:
    1. Load tokens from /vulpine/credentials/openai-oauth.json
    2. Auto-refresh via refresh_token grant
    3. POST to chatgpt.com/backend-api/codex/responses
    4. Same tool calling (bash, web) as existing OpenRouter provider
    5. 401 → one silent refresh → retry → fail with re-auth message
```

## Components

### 1. Host-side: OAuth Device-Code Client (`internal/auth/openai.go`)

New Go package with three operations:

**Login flow (device-code, RFC 8628):**
- POST `https://auth.openai.com/oauth/device/code`
  - Parameters: `client_id=app_EMoamEEZ73f0CkXaXp7hrann`, `scope=openid profile email offline_access`
  - Response: `{ device_code, user_code, verification_uri, interval, expires_in }`
- Print `verification_uri` and `user_code` to terminal
- Poll `https://auth.openai.com/oauth/token` every `interval` seconds
  - Parameters: `client_id`, `device_code`, `grant_type=urn:ietf:params:oauth:grant-type:device_code`
  - On success: `{ access_token, refresh_token, id_token, expires_in }`
- Extract `account_id` from `access_token` (JWT decode, not verify)
- Write to `~/.vulpineos/credentials/openai-oauth.json`: `{ access_token, refresh_token, expires_at, account_id }`
- File permissions: 0600

**Logout flow:**
- Remove `~/.vulpineos/credentials/openai-oauth.json`

**Token refresh (also called by container provider):**
- POST `https://auth.openai.com/oauth/token`
  - Parameters: `client_id`, `refresh_token`, `grant_type=refresh_token`
  - Update stored `access_token`, `refresh_token`, `expires_at`
- Called by both host CLI and container provider (stateless HTTP, no local lock needed)

### 2. Host-side: CLI Commands (`cmd/vulpine/auth.go`)

```bash
vulpine auth login --provider openai
  # Runs device-code flow, stores credentials

vulpine auth logout --provider openai
  # Removes credential file

vulpine auth status --provider openai
  # Shows: logged in as <account_id>, expires in N days
```

### 3. Host-side: Container Config Contribution

In `patchNanoClawSourceRuntime` (or `ensureNanoClawSourceAssets`), add a step that:

- Checks if `~/.vulpineos/credentials/openai-oauth.json` exists
- If yes, adds mount: `~/.vulpineos/credentials/` → `/vulpine/credentials/` (read-only)
- Sets env var `OPENCODE_PROVIDER=openai-oauth`
- The NanoClaw provider container config registry gets a new entry for `openai-oauth`

For OpenCode provider resolution: when `OPENCODE_PROVIDER=openai-oauth`, map to the existing `opencode` provider slot in the registry (or add it as its own registered provider).

### 4. Container-side: OpenAI OAuth Provider (TypeScript)

New code path in `defaultOpenCodeProviderSource` alongside the existing OpenRouter block:

**When `OPENCODE_PROVIDER=openai-oauth`:**

Message construction:
- System prompt from `input.systemContext.instructions`
- User prompt from `input.prompt`
- Model from `OPENCODE_MODEL` env var (default: `gpt-5.5`)
- Tools: `bash` and `web` (same definitions as OpenRouter path)

API request:
- URL: `https://chatgpt.com/backend-api/codex/responses`
- Method: POST
- Headers: `Authorization: Bearer <access_token>`, `Content-Type: application/json`
- Model: `gpt-5.5` (or whatever `OPENCODE_MODEL` specifies)
- Format: **ChatGPT backend API** — an undocumented, manual-SSE protocol distinct from both OpenAI Chat Completions and the official Responses API. The Microsoft Amplifier provider describes it as: "a separate module because the ChatGPT backend is a distinct, undocumented API surface that rejects many standard OpenAI API parameters and requires raw HTTP + manual SSE parsing (the OpenAI Python SDK's streaming accumulator does not work against it)."

The exact JSON shape must be reverse-engineered from the Codex CLI source or prior implementations (OpenClaw, Pi, oc-codex-multi-auth). Key known differences from Chat Completions:
- Uses `input` field instead of `messages`
- Requires `stream: true` (ChatGPT backend rejects non-streaming)
- Requires `store: false`
- Supports `reasoning.encrypted_content` for multi-turn continuity
- Tool definitions use the same JSON schema format but the response shape differs

The provider must:
1. Map Chat Completions-style messages to ChatGPT backend input format
2. Handle raw SSE stream parsing (not a standard JSON response)
3. Map tool call responses back to Chat Completions-style tool results
4. Accumulate SSE events into a complete response

**Key difference from OpenRouter path — this is a raw SSE protocol, not a JSON REST API.** The provider must parse SSE manually. Tool definitions stay the same JSON format.

**Token refresh triggering conditions:**
- On 401 from API: read token file, check expiry, if expired → refresh, retry once
- On startup: check expiry, refresh proactively if within 5 minutes of expiry

### 5. Credential File Schema

`~/.vulpineos/credentials/openai-oauth.json`:
```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJSUzI1NiIs...",
  "expires_at": 1748563200,
  "account_id": "user-abc123"
}
```

## Models Available

From ChatGPT Plus subscription (as of May 2026):

| Model | Context | Priority | Fast Mode | Reasoning |
|-------|---------|----------|-----------|-----------|
| `gpt-5.5` | 272K | 0 (highest) | Yes | low/med/high/xhigh |
| `gpt-5.4` | 272K | 2 | Yes | low/med/high/xhigh |
| `gpt-5.4-mini` | 272K | 4 | No | low/med/high/xhigh |
| `gpt-5.3-codex` | 272K | 6 | No | low/med/high/xhigh |
| `gpt-5.2` | 272K | 10 | No | low/med/high/xhigh |

Models set via `OPENCODE_MODEL` env var.

## Error Handling

| Scenario | Behavior |
|----------|----------|
| No credential file | Error: "Run `vulpine auth login --provider openai` first" |
| Expired token, refresh succeeds | Silent refresh, write new file, retry request |
| Expired token, refresh fails | Error: "Session expired. Re-run `vulpine auth login --provider openai`" |
| 401 mid-request | One silent refresh + retry, then error |
| 429 rate limit | Exponential backoff, up to 3 retries |
| Model not on subscription plan | Error: "Model unavailable on your ChatGPT plan" |
| Network error | Retry up to 2 times, then propagate |

## Files to Create/Modify

| File | Type | Purpose |
|------|------|---------|
| `internal/auth/openai.go` | **New** | Device-code OAuth client (login/logout/refresh/status) |
| `cmd/vulpine/auth.go` | **New** | CLI commands for auth login/logout/status |
| `internal/nanoclaw/source_runtime.go` | Modify | Add openai-oauth provider block alongside OpenRouter |
| `internal/nanoclaw/source_runtime_test.go` | Modify | Tests for openai-oauth provider path |
| `internal/nanoclaw/manager.go` or `daemon.go` | Modify | Add credential mount + env for openai-oauth |

## Dependencies

- Host side: standard Go `net/http`, `encoding/json`, `crypto/rand` — no external deps
- Container side: the provider already uses `fetch()` and `execSync()` — no new Node deps

## Open Questions

1. **Responses API tool call format**: Need to verify exact JSON shape for tool definitions in the Responses API vs Chat Completions format. The Responses API uses `tools` array with the same JSON schema function definitions, so this should be a straightforward mapping.
2. **SSE streaming**: The ChatGPT backend API uses SSE. The current provider collects the full response body (non-streaming, `fetch` + `res.json()`). If the backend requires `stream: true`, we'll need to switch to SSE consumption in the provider, which adds complexity.
3. **Model discovery**: Currently models are hardcoded. Could optionally call `GET /backend-api/codex/models` to discover available models dynamically.

## Future Considerations

- Multi-account rotation (like OpenClaw's `oc-codex-multi-auth`)
- Fast mode support (`gpt-5.5-fast` → `service_tier: "priority"`)
- Per-container credential isolation with account labels
