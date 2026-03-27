package tokenopt

import "fmt"

// ViewportPruner prunes optimized DOM snapshots to only include elements
// near the agent's viewport, reducing token usage for off-screen content.
type ViewportPruner struct {
	viewportWidth  int
	viewportHeight int
	scrollX        float64
	scrollY        float64
	margin         int // extra pixels around viewport to include (default 200)
}

// NewViewportPruner creates a pruner for the given viewport dimensions.
func NewViewportPruner(width, height int) *ViewportPruner {
	return &ViewportPruner{
		viewportWidth:  width,
		viewportHeight: height,
		margin:         200,
	}
}

// SetScroll updates the scroll position for viewport calculations.
func (p *ViewportPruner) SetScroll(x, y float64) {
	p.scrollX = x
	p.scrollY = y
}

// SetMargin overrides the default margin (200px) around the viewport.
func (p *ViewportPruner) SetMargin(m int) {
	p.margin = m
}

// MaxVisibleNodes estimates the number of visible elements based on viewport height.
// Uses ~20px per element as a heuristic (typical line-height for interactive elements).
func (p *ViewportPruner) MaxVisibleNodes() int {
	n := (p.viewportHeight + p.margin) / 20
	if n < 10 {
		n = 10
	}
	return n
}

// Prune filters the optimized DOM node list to approximately the viewport-visible set.
// Nodes are [depth, roleCode, name, props?] tuples from Phase 3's getOptimizedDOM.
// Since position data isn't available in the optimized format, this uses a top-of-tree
// heuristic: keeps the first MaxVisibleNodes() elements and appends a truncation marker.
func (p *ViewportPruner) Prune(nodes []interface{}) []interface{} {
	maxNodes := p.MaxVisibleNodes()

	if len(nodes) <= maxNodes {
		return nodes
	}

	result := make([]interface{}, 0, maxNodes+1)
	result = append(result, nodes[:maxNodes]...)

	truncated := len(nodes) - maxNodes
	marker := []interface{}{
		float64(0),
		"note",
		fmt.Sprintf("...%d more nodes truncated", truncated),
	}
	result = append(result, marker)

	return result
}

// PruneWithMax filters nodes to at most maxNodes elements, overriding the viewport heuristic.
func (p *ViewportPruner) PruneWithMax(nodes []interface{}, maxNodes int) []interface{} {
	if maxNodes <= 0 {
		maxNodes = p.MaxVisibleNodes()
	}

	if len(nodes) <= maxNodes {
		return nodes
	}

	result := make([]interface{}, 0, maxNodes+1)
	result = append(result, nodes[:maxNodes]...)

	truncated := len(nodes) - maxNodes
	marker := []interface{}{
		float64(0),
		"note",
		fmt.Sprintf("...%d more nodes truncated", truncated),
	}
	result = append(result, marker)

	return result
}
