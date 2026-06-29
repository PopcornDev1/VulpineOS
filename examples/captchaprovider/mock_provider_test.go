package captchaprovider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"vulpineos/internal/extensions"
)

func TestMockProviderDetectsChallengeHandoff(t *testing.T) {
	provider := NewMockProvider(MockOptions{
		AllowedDomains: []string{"example.com"},
		ProviderName:   "example-mock",
	})

	if !provider.Available() {
		t.Fatal("mock provider should report available")
	}

	challenge, err := provider.Detect(context.Background(), extensions.CaptchaDetectRequest{
		PageID:     "page-1",
		URL:        "https://login.example.com/signup",
		VendorHint: extensions.CaptchaVendorTurnstile,
	})
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}

	if challenge.ID == "" {
		t.Fatal("challenge ID is empty")
	}
	if challenge.Vendor != extensions.CaptchaVendorTurnstile {
		t.Fatalf("vendor = %q, want %q", challenge.Vendor, extensions.CaptchaVendorTurnstile)
	}
	if challenge.Domain != "login.example.com" {
		t.Fatalf("domain = %q", challenge.Domain)
	}
	if !challenge.RequiresConfirmation {
		t.Fatal("mock provider should require confirmation")
	}
	if challenge.PolicyDecision != extensions.CaptchaPolicyNeedsConfirmation {
		t.Fatalf("policy = %q", challenge.PolicyDecision)
	}
}

func TestMockProviderBlocksUnapprovedDomains(t *testing.T) {
	provider := NewMockProvider(MockOptions{AllowedDomains: []string{"example.com"}})

	challenge, err := provider.Detect(context.Background(), extensions.CaptchaDetectRequest{
		URL: "https://not-allowed.test/challenge",
	})
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if challenge.PolicyDecision != extensions.CaptchaPolicyBlocked {
		t.Fatalf("policy = %q, want blocked", challenge.PolicyDecision)
	}
}

func TestMockProviderSolveReturnsSanitizedUserAction(t *testing.T) {
	provider := NewMockProvider(MockOptions{ProviderName: "example-mock"})

	solution, err := provider.Solve(context.Background(), extensions.CaptchaSolveRequest{
		ChallengeID:    "mock-challenge",
		AllowCostCents: 10,
	})
	if err != nil {
		t.Fatalf("Solve returned error: %v", err)
	}

	if solution.Provider != "example-mock" {
		t.Fatalf("provider = %q", solution.Provider)
	}
	if solution.Status != extensions.CaptchaSolutionNeedsConfirmation {
		t.Fatalf("status = %q", solution.Status)
	}
	if solution.Token != "" {
		t.Fatal("mock provider must not return a raw token")
	}
	if solution.ProviderRequestID != "" {
		t.Fatal("mock provider must not return a provider request ID")
	}
	if !strings.Contains(solution.Instructions, "NEEDS_USER_ACTION") {
		t.Fatalf("instructions should explain user action: %q", solution.Instructions)
	}
}

func TestMockProviderApplyNeverInjects(t *testing.T) {
	provider := NewMockProvider(MockOptions{})

	err := provider.Apply(context.Background(), extensions.CaptchaApplyRequest{
		ChallengeID: "mock-challenge",
		SolutionID:  "mock-solution",
		PageID:      "page-1",
	})
	if !errors.Is(err, extensions.ErrUnavailable) {
		t.Fatalf("Apply error = %v, want ErrUnavailable", err)
	}
}
