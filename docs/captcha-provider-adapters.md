# Captcha Provider Adapters

VulpineOS exposes a challenge/captcha extension seam so operators can connect
their own approved provider without shipping solver credentials or vendor logic
in the public build.

The stock public build has the MCP tools:

- `vulpine_captcha_detect`
- `vulpine_captcha_solve`
- `vulpine_captcha_apply`

Without a registered provider, those tools return unavailable. That is
intentional: VulpineOS should not be a turnkey abuse or fraud tool, and the
public config must not contain solver API keys.

## Acceptable Use

Only attach a provider for workflows where you are authorized to automate the
site and the challenge-handling step. Do not use this seam for fraud, spam,
credential stuffing, account abuse, paywall or access-control circumvention, or
other activity that violates law, site terms, or user consent.

Production adapters should enforce:

- allowed domains
- explicit operator confirmation for sensitive flows
- rate limits
- cost limits
- secret storage outside `~/.vulpineos/config.json`
- audit logs that redact tokens, cookies, API keys, and provider request IDs

When policy denies a solve, or when a human must complete a challenge, return a
user-action result. Agents are instructed to report `NEEDS_USER_ACTION` and keep
the browser session ready for operator takeover instead of pretending the task
succeeded.

## Provider Interface

Adapters implement `extensions.CaptchaProvider`:

```go
type CaptchaProvider interface {
    Detect(ctx context.Context, req CaptchaDetectRequest) (*CaptchaChallenge, error)
    Solve(ctx context.Context, req CaptchaSolveRequest) (*CaptchaSolution, error)
    Apply(ctx context.Context, req CaptchaApplyRequest) error
    Available() bool
}
```

The MCP boundary sanitizes provider output. Raw solve tokens and opaque provider
request IDs are not returned to the model. Provider credentials should come from
environment variables, a host secret manager, or another deployment-specific
secret store.

## Mock Example

The repository includes a harmless mock provider at:

```text
examples/captchaprovider
```

It demonstrates the interface and policy handoff behavior. It does not contact a
solver, return a raw token, or inject anything into the page.

Run its tests with:

```bash
go test ./examples/captchaprovider
```

Use it as a reference for metadata shape, domain policy handling, and sanitized
`NEEDS_USER_ACTION` responses.

## Wiring A Provider

For a provider that does not need the live browser client, register it from a
build-tagged file in a package that is compiled into your fork.

Example local file: `cmd/vulpineos/captcha_mock.go`

```go
//go:build captcha_mock

package main

import (
    "vulpineos/examples/captchaprovider"
    "vulpineos/internal/extensions"
)

func init() {
    extensions.Registry.SetCaptcha(captchaprovider.NewMockProvider(captchaprovider.MockOptions{
        AllowedDomains: []string{"example.com"},
        ProviderName:   "local-mock",
    }))
}
```

Build that local fork with:

```bash
go build -tags captcha_mock ./cmd/vulpineos
```

For a provider that needs the live browser client for `Apply`, place a
build-tagged adapter file inside `internal/extensions` in your fork. Files in
that package can assign `privateProviders.Captcha`, which is invoked by
`extensions.InitWithClient` after VulpineOS has a live Juggler client:

```go
//go:build captcha_provider

package extensions

func init() {
    privateProviders.Captcha = func(jc JugglerCallable) CaptchaProvider {
        return newYourProviderFromSecrets(jc)
    }
}
```

Keep real vendor credentials and deployment-specific policy out of the public
repository. The public config should only carry policy fields such as allowed
domains, confirmation policy, screenshot permission, timeout, rate limit, and
cost cap.

## Expected Agent Behavior

With a provider attached, agents should:

1. call `vulpine_captcha_detect` when a challenge appears
2. call `vulpine_captcha_solve` only when policy allows it
3. call `vulpine_captcha_apply` only with a provider-owned solution ID
4. verify the page state after applying a solution
5. report `NEEDS_USER_ACTION` when solving is blocked, unavailable, or requires
   the operator

They should not claim a signup, checkout, consent, or account-protection step
succeeded unless the post-action page state was observed.
