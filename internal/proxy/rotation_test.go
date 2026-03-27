package proxy

import (
	"testing"
)

func TestDefaultConfigHasRotationDisabled(t *testing.T) {
	cfg := DefaultRotationConfig()
	if cfg.Enabled {
		t.Error("expected default config to have rotation disabled")
	}
	if !cfg.SyncFingerprint {
		t.Error("expected default config to have SyncFingerprint true")
	}
}

func TestRotationCyclesThroughPool(t *testing.T) {
	r := NewRotator()
	r.SetConfig("a1", &RotationConfig{
		Enabled:   true,
		ProxyPool: []string{"http://p1:8080", "http://p2:8080", "http://p3:8080"},
	})

	results := make([]string, 4)
	for i := 0; i < 4; i++ {
		proxy, err := r.Rotate("a1")
		if err != nil {
			t.Fatalf("Rotate %d: %v", i, err)
		}
		results[i] = proxy
	}

	// Starting at index 0, rotations go: 1, 2, 0, 1
	expected := []string{"http://p2:8080", "http://p3:8080", "http://p1:8080", "http://p2:8080"}
	for i, want := range expected {
		if results[i] != want {
			t.Errorf("rotation %d: got %s, want %s", i, results[i], want)
		}
	}
}

func TestShouldRotateReturnsFalseWhenDisabled(t *testing.T) {
	r := NewRotator()
	r.SetConfig("a1", &RotationConfig{
		Enabled:           false,
		RotateOnRateLimit: true,
		ProxyPool:         []string{"http://p1:8080", "http://p2:8080"},
	})

	if r.ShouldRotate("a1", "rateLimit") {
		t.Error("expected ShouldRotate false when rotation disabled")
	}
}

func TestShouldRotateReturnsTrueOnRateLimit(t *testing.T) {
	r := NewRotator()
	r.SetConfig("a1", &RotationConfig{
		Enabled:           true,
		RotateOnRateLimit: true,
		ProxyPool:         []string{"http://p1:8080", "http://p2:8080"},
	})

	if !r.ShouldRotate("a1", "rateLimit") {
		t.Error("expected ShouldRotate true for rateLimit")
	}
}

func TestShouldRotateReturnsFalseWithSmallPool(t *testing.T) {
	r := NewRotator()
	r.SetConfig("a1", &RotationConfig{
		Enabled:           true,
		RotateOnRateLimit: true,
		ProxyPool:         []string{"http://p1:8080"}, // only 1 proxy
	})

	if r.ShouldRotate("a1", "rateLimit") {
		t.Error("expected ShouldRotate false with single-proxy pool")
	}
}

func TestOnRateLimitTriggersRotation(t *testing.T) {
	r := NewRotator()
	r.SetConfig("a1", &RotationConfig{
		Enabled:           true,
		RotateOnRateLimit: true,
		ProxyPool:         []string{"http://p1:8080", "http://p2:8080"},
	})

	rotated, newProxy, err := r.OnRateLimit("a1")
	if err != nil {
		t.Fatalf("OnRateLimit error: %v", err)
	}
	if !rotated {
		t.Error("expected rotation to happen")
	}
	if newProxy != "http://p2:8080" {
		t.Errorf("expected p2, got %s", newProxy)
	}
}

func TestOnRateLimitNoRotationWhenDisabled(t *testing.T) {
	r := NewRotator()
	r.SetConfig("a1", &RotationConfig{
		Enabled:           false,
		RotateOnRateLimit: true,
		ProxyPool:         []string{"http://p1:8080", "http://p2:8080"},
	})

	rotated, _, _ := r.OnRateLimit("a1")
	if rotated {
		t.Error("expected no rotation when disabled")
	}
}

func TestCurrentProxyReturnsCorrectProxy(t *testing.T) {
	r := NewRotator()
	r.SetConfig("a1", &RotationConfig{
		Enabled:      true,
		ProxyPool:    []string{"http://p1:8080", "http://p2:8080", "http://p3:8080"},
		CurrentIndex: 0,
	})

	if got := r.CurrentProxy("a1"); got != "http://p1:8080" {
		t.Errorf("expected p1, got %s", got)
	}

	r.Rotate("a1")

	if got := r.CurrentProxy("a1"); got != "http://p2:8080" {
		t.Errorf("expected p2 after rotation, got %s", got)
	}
}

func TestCurrentProxyEmptyForUnknownAgent(t *testing.T) {
	r := NewRotator()
	if got := r.CurrentProxy("unknown"); got != "" {
		t.Errorf("expected empty string for unknown agent, got %s", got)
	}
}

func TestWrapsAroundPool(t *testing.T) {
	r := NewRotator()
	r.SetConfig("a1", &RotationConfig{
		Enabled:      true,
		ProxyPool:    []string{"http://p1:8080", "http://p2:8080"},
		CurrentIndex: 1, // start at last element
	})

	proxy, err := r.Rotate("a1")
	if err != nil {
		t.Fatalf("Rotate error: %v", err)
	}
	if proxy != "http://p1:8080" {
		t.Errorf("expected wrap to p1, got %s", proxy)
	}
}

func TestOnBlockTriggersRotation(t *testing.T) {
	r := NewRotator()
	r.SetConfig("a1", &RotationConfig{
		Enabled:       true,
		RotateOnBlock: true,
		ProxyPool:     []string{"http://p1:8080", "http://p2:8080"},
	})

	rotated, newProxy, err := r.OnBlock("a1")
	if err != nil {
		t.Fatalf("OnBlock error: %v", err)
	}
	if !rotated {
		t.Error("expected rotation on block")
	}
	if newProxy != "http://p2:8080" {
		t.Errorf("expected p2, got %s", newProxy)
	}
}
