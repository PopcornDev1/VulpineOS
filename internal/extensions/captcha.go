package extensions

import (
	"context"
	"time"
)

const (
	CaptchaVendorRecaptcha = "recaptcha"
	CaptchaVendorHCaptcha  = "hcaptcha"
	CaptchaVendorTurnstile = "turnstile"
	CaptchaVendorImage     = "image"
	CaptchaVendorUnknown   = "unknown"

	CaptchaPolicyAllow               = "allow"
	CaptchaPolicyNeedsConfirmation   = "needs_confirmation"
	CaptchaPolicyBlocked             = "blocked"
	CaptchaSolutionSolved            = "solved"
	CaptchaSolutionNeedsConfirmation = "needs_confirmation"
	CaptchaSolutionFailed            = "failed"
)

// CaptchaProvider is the public challenge-handling seam. Public builds ship a
// no-op provider; private builds can register authorized solver adapters.
type CaptchaProvider interface {
	Detect(ctx context.Context, req CaptchaDetectRequest) (*CaptchaChallenge, error)
	Solve(ctx context.Context, req CaptchaSolveRequest) (*CaptchaSolution, error)
	Apply(ctx context.Context, req CaptchaApplyRequest) error
	Available() bool
}

type CaptchaDetectRequest struct {
	PageID         string
	FrameID        string
	URL            string
	VendorHint     string
	SendScreenshot bool
	ScreenshotPNG  []byte
}

type CaptchaChallenge struct {
	ID                   string
	Vendor               string
	Type                 string
	Domain               string
	SiteKey              string
	Action               string
	RequiresConfirmation bool
	PolicyDecision       string
	DetectedAt           time.Time
}

type CaptchaSolveRequest struct {
	ChallengeID    string
	AllowCostCents int
	AutoApply      bool
}

type CaptchaSolution struct {
	ID                string
	ChallengeID       string
	Provider          string
	Status            string
	Token             string
	Instructions      string
	ExpiresAt         time.Time
	CostEstimateCents int
	NeedsConfirmation bool
	ProviderRequestID string
}

type CaptchaApplyRequest struct {
	ChallengeID string
	SolutionID  string
	PageID      string
	FrameID     string
	Submit      bool
}

var defaultCaptchaProvider CaptchaProvider = noopCaptchaProvider{}

type noopCaptchaProvider struct{}

func (noopCaptchaProvider) Detect(ctx context.Context, req CaptchaDetectRequest) (*CaptchaChallenge, error) {
	return nil, ErrUnavailable
}

func (noopCaptchaProvider) Solve(ctx context.Context, req CaptchaSolveRequest) (*CaptchaSolution, error) {
	return nil, ErrUnavailable
}

func (noopCaptchaProvider) Apply(ctx context.Context, req CaptchaApplyRequest) error {
	return ErrUnavailable
}

func (noopCaptchaProvider) Available() bool { return false }
