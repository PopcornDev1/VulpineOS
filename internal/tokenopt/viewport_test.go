package tokenopt

import (
	"testing"
)

func TestNewViewportPruner(t *testing.T) {
	p := NewViewportPruner(1280, 720)
	if p.viewportWidth != 1280 || p.viewportHeight != 720 {
		t.Fatalf("expected 1280x720, got %dx%d", p.viewportWidth, p.viewportHeight)
	}
	if p.margin != 200 {
		t.Fatalf("expected default margin 200, got %d", p.margin)
	}
}

func TestSetScroll(t *testing.T) {
	p := NewViewportPruner(1280, 720)
	p.SetScroll(100, 500)
	if p.scrollX != 100 || p.scrollY != 500 {
		t.Fatalf("expected scroll 100,500 got %v,%v", p.scrollX, p.scrollY)
	}
}

func TestMaxVisibleNodes(t *testing.T) {
	p := NewViewportPruner(1280, 720)
	// (720 + 200) / 20 = 46
	n := p.MaxVisibleNodes()
	if n != 46 {
		t.Fatalf("expected 46 visible nodes for 720px viewport, got %d", n)
	}
}

func TestMaxVisibleNodesMinimum(t *testing.T) {
	p := NewViewportPruner(100, 50)
	// (50 + 200) / 20 = 12, above minimum
	n := p.MaxVisibleNodes()
	if n != 12 {
		t.Fatalf("expected 12, got %d", n)
	}

	// Very small viewport
	p2 := NewViewportPruner(100, 10)
	p2.SetMargin(0)
	// 10 / 20 = 0, should clamp to 10
	n2 := p2.MaxVisibleNodes()
	if n2 != 10 {
		t.Fatalf("expected minimum 10, got %d", n2)
	}
}

func makeNodes(n int) []interface{} {
	nodes := make([]interface{}, n)
	for i := 0; i < n; i++ {
		nodes[i] = []interface{}{float64(1), "btn", "Button"}
	}
	return nodes
}

func TestPruneSmallSet(t *testing.T) {
	p := NewViewportPruner(1280, 720)
	nodes := makeNodes(10)
	result := p.Prune(nodes)
	if len(result) != 10 {
		t.Fatalf("small set should not be pruned, got %d nodes", len(result))
	}
}

func TestPruneLargeSet(t *testing.T) {
	p := NewViewportPruner(1280, 720)
	nodes := makeNodes(200)
	result := p.Prune(nodes)

	maxVisible := p.MaxVisibleNodes() // 46
	// Should be maxVisible nodes + 1 truncation marker
	expected := maxVisible + 1
	if len(result) != expected {
		t.Fatalf("expected %d nodes (including marker), got %d", expected, len(result))
	}

	// Check truncation marker
	marker, ok := result[len(result)-1].([]interface{})
	if !ok {
		t.Fatal("last element should be a truncation marker")
	}
	name, ok := marker[2].(string)
	if !ok {
		t.Fatal("marker name should be string")
	}
	if name != "...154 more nodes truncated" {
		t.Fatalf("unexpected marker: %s", name)
	}
}

func TestPruneWithMax(t *testing.T) {
	p := NewViewportPruner(1280, 720)
	nodes := makeNodes(100)
	result := p.PruneWithMax(nodes, 20)

	if len(result) != 21 { // 20 + marker
		t.Fatalf("expected 21 nodes, got %d", len(result))
	}
}

func TestPruneWithMaxZero(t *testing.T) {
	p := NewViewportPruner(1280, 720)
	nodes := makeNodes(200)
	result := p.PruneWithMax(nodes, 0) // should use MaxVisibleNodes

	maxVisible := p.MaxVisibleNodes()
	if len(result) != maxVisible+1 {
		t.Fatalf("expected %d, got %d", maxVisible+1, len(result))
	}
}

func TestPruneWithMaxSmallSet(t *testing.T) {
	p := NewViewportPruner(1280, 720)
	nodes := makeNodes(5)
	result := p.PruneWithMax(nodes, 20)
	if len(result) != 5 {
		t.Fatalf("should not prune small set, got %d", len(result))
	}
}
