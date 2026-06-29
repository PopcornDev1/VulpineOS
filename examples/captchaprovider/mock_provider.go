// Package captchaprovider contains a harmless CaptchaProvider example.
//
// It demonstrates the VulpineOS extension seam without contacting a solver,
// producing challenge tokens, or injecting anything into a live page.
package captchaprovider

import (
	"context"
	"net/url"
	"strings"
	"time"
	"unicode"

	"vulpineos/internal/extensions"
)

// MockOptions controls the example provider. AllowedDomains accepts exact host
// names or parent domains; for example, "example.com" allows
// "login.example.com".
type MockOptions struct {
	AllowedDomains []string
	ProviderName   string
	ChallengeType  string
}

// MockProvider is an example CaptchaProvider. It detects a mock challenge and
// always asks for user action; it never returns a raw captcha token.
type MockProvider struct {
	options MockOptions
}

// NewMockProvider returns an example provider suitable for local wiring tests
// and documentation. It is not a captcha-solving implementation.
func NewMockProvider(options MockOptions) *MockProvider {
	if strings.TrimSpace(options.ProviderName) == "" {
		options.ProviderName = "mock-captcha-provider"
	}
	if strings.TrimSpace(options.ChallengeType) == "" {
		options.ChallengeType = "interactive"
	}
	return &MockProvider{options: options}
}

func (p *MockProvider) Available() bool {
	return p != nil
}

func (p *MockProvider) Detect(ctx context.Context, req extensions.CaptchaDetectRequest) (*extensions.CaptchaChallenge, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	domain := hostFromURL(req.URL)
	policy := extensions.CaptchaPolicyNeedsConfirmation
	if !domainAllowed(domain, p.options.AllowedDomains) {
		policy = extensions.CaptchaPolicyBlocked
	}

	vendor := strings.TrimSpace(req.VendorHint)
	if vendor == "" {
		vendor = extensions.CaptchaVendorUnknown
	}

	return &extensions.CaptchaChallenge{
		ID:                   "mock-" + stableIDPart(domain),
		Vendor:               vendor,
		Type:                 p.options.ChallengeType,
		Domain:               domain,
		RequiresConfirmation: true,
		PolicyDecision:       policy,
		DetectedAt:           time.Now().UTC(),
	}, nil
}

func (p *MockProvider) Solve(ctx context.Context, req extensions.CaptchaSolveRequest) (*extensions.CaptchaSolution, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	return &extensions.CaptchaSolution{
		ID:                "mock-solution-" + stableIDPart(req.ChallengeID),
		ChallengeID:       req.ChallengeID,
		Provider:          p.options.ProviderName,
		Status:            extensions.CaptchaSolutionNeedsConfirmation,
		Instructions:      "NEEDS_USER_ACTION: this mock provider does not solve challenges; ask the operator to complete or approve the step.",
		CostEstimateCents: 0,
		NeedsConfirmation: true,
	}, nil
}

func (p *MockProvider) Apply(ctx context.Context, req extensions.CaptchaApplyRequest) error {
	return extensions.ErrUnavailable
}

func hostFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func domainAllowed(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSpace(host))
	for _, domain := range allowed {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func stableIDPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}
