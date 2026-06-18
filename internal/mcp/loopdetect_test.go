package mcp

import "testing"

func TestLoopDetector_NoLoop(t *testing.T) {
	ld := NewLoopDetector(3)
	if w := ld.Check("s1", "click", `{"x":1}`); w != "" {
		t.Errorf("expected no warning, got %q", w)
	}
	if w := ld.Check("s1", "click", `{"x":2}`); w != "" {
		t.Errorf("expected no warning for different args, got %q", w)
	}
}

func TestLoopDetector_DetectsLoop(t *testing.T) {
	ld := NewLoopDetector(3)
	ld.Check("s1", "click", `{"ref":"@1"}`)
	ld.Check("s1", "click", `{"ref":"@1"}`)
	ld.Check("s1", "click", `{"ref":"@1"}`)
	w := ld.Check("s1", "click", `{"ref":"@1"}`)
	if w == "" {
		t.Error("expected loop warning after 4 identical calls")
	}
}

func TestLoopDetector_DifferentToolsNoLoop(t *testing.T) {
	ld := NewLoopDetector(3)
	ld.Check("s1", "click", `{"ref":"@1"}`)
	ld.Check("s1", "snapshot", `{}`)
	ld.Check("s1", "click", `{"ref":"@1"}`)
	w := ld.Check("s1", "snapshot", `{}`)
	if w != "" {
		t.Errorf("expected no warning for alternating tools, got %q", w)
	}
}

func TestLoopDetector_DetectsRepeatedNonConsecutiveAction(t *testing.T) {
	ld := NewLoopDetector(3)
	ld.Check("s1", "vulpine_navigate", `{"url":"https://detector-a.example/"}`)
	ld.Check("s1", "vulpine_page_settled", `{"timeout":15}`)
	ld.Check("s1", "vulpine_snapshot", `{"profile":"compact"}`)
	ld.Check("s1", "vulpine_navigate", `{"url":"https://detector-a.example/"}`)
	ld.Check("s1", "vulpine_get_ax_tree", `{}`)
	ld.Check("s1", "vulpine_navigate", `{"url":"https://detector-b.example/"}`)
	ld.Check("s1", "vulpine_snapshot", `{"profile":"expanded"}`)

	w := ld.Check("s1", "vulpine_navigate", `{"url":"https://detector-a.example/"}`)
	if w == "" {
		t.Error("expected loop warning after repeated non-consecutive action")
	}
}

func TestLoopDetector_IsolatesSessions(t *testing.T) {
	ld := NewLoopDetector(2)
	ld.Check("s1", "click", `{"ref":"@1"}`)
	ld.Check("s1", "click", `{"ref":"@1"}`)
	// s2 should have clean history
	w := ld.Check("s2", "click", `{"ref":"@1"}`)
	if w != "" {
		t.Errorf("expected no warning for different session, got %q", w)
	}
}

func TestLoopDetector_Reset(t *testing.T) {
	ld := NewLoopDetector(2)
	ld.Check("s1", "click", `{"ref":"@1"}`)
	ld.Check("s1", "click", `{"ref":"@1"}`)
	ld.Reset("s1")
	w := ld.Check("s1", "click", `{"ref":"@1"}`)
	if w != "" {
		t.Errorf("expected no warning after reset, got %q", w)
	}
}

func TestLoopDetector_DefaultMaxRepeat(t *testing.T) {
	ld := NewLoopDetector(0)
	if ld.maxRepeat != 3 {
		t.Errorf("expected default maxRepeat=3, got %d", ld.maxRepeat)
	}
}

func TestHashAction(t *testing.T) {
	h1 := hashAction("click", `{"ref":"@1"}`)
	h2 := hashAction("click", `{"ref":"@2"}`)
	h3 := hashAction("click", `{"ref":"@1"}`)

	if h1 == h2 {
		t.Error("different args should produce different hashes")
	}
	if h1 != h3 {
		t.Error("same tool+args should produce same hash")
	}
}
