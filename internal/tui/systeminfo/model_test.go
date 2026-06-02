package systeminfo

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/tui/shared"
	"vulpineos/internal/vault"
)

func TestKernelStatusViewOmitsModeRouteAndWindow(t *testing.T) {
	model := New()
	model.SetHeight(20)

	updated, _ := model.Update(shared.KernelStatusMsg{
		Running:       true,
		PID:           1234,
		Uptime:        2 * time.Minute,
		Headless:      false,
		BrowserRoute:  "CAMOUFOX",
		BrowserWindow: "HIDDEN",
	})

	view := updated.View()
	if strings.Contains(view, "Mode ") {
		t.Fatalf("did not expect Mode line in view, got:\n%s", view)
	}
	if strings.Contains(view, "Route ") {
		t.Fatalf("did not expect Route line in view, got:\n%s", view)
	}
	if strings.Contains(view, "Win ") {
		t.Fatalf("did not expect Win line in view, got:\n%s", view)
	}
}

func TestViewFitsRuntimeEventsToWidth(t *testing.T) {
	model := New()
	model.SetWidth(18)
	model.SetHeight(20)

	updated, _ := model.Update(shared.RuntimeEventMsg{Event: vault.RuntimeEvent{
		Component: "gateway",
		Event:     "very-long-runtime-event-name-that-would-wrap",
		Timestamp: time.Date(2026, 5, 10, 12, 34, 0, 0, time.UTC),
	}})

	view := updated.View()
	for i, line := range strings.Split(view, "\n") {
		if width := lipgloss.Width(line); width > 18 {
			t.Fatalf("line %d width = %d, want <= 18:\n%s", i+1, width, view)
		}
	}
}

func TestDefaultHeightShowsPoolAndContextStats(t *testing.T) {
	model := New()
	model.SetHeight(13)

	updated, _ := model.Update(shared.KernelStatusMsg{
		Running:       true,
		PID:           1234,
		Uptime:        2 * time.Minute,
		Headless:      false,
		BrowserRoute:  "CAMOUFOX",
		BrowserWindow: "VISIBLE",
	})
	updated, _ = updated.Update(shared.PoolStatsMsg{Available: 3, Active: 2, Total: 5})
	updated, _ = updated.Update(shared.TelemetryMsg{ActiveContexts: 4, ActivePages: 7})

	view := updated.View()
	if !strings.Contains(view, "Pool: 3/2/5") {
		t.Fatalf("default-height system panel missing pool stats:\n%s", view)
	}
	if !strings.Contains(view, "Ctx: 4 Pg: 7") {
		t.Fatalf("default-height system panel missing context stats:\n%s", view)
	}
}

func TestDefaultHeightShowsRecentRuntimeEvent(t *testing.T) {
	model := New()
	model.SetHeight(13)

	updated, _ := model.Update(shared.KernelStatusMsg{
		Running:       true,
		PID:           1234,
		Uptime:        2 * time.Minute,
		Headless:      false,
		BrowserRoute:  "CAMOUFOX",
		BrowserWindow: "VISIBLE",
	})
	updated, _ = updated.Update(shared.PoolStatsMsg{Available: 3, Active: 2, Total: 5})
	updated, _ = updated.Update(shared.TelemetryMsg{ActiveContexts: 4, ActivePages: 7})
	updated, _ = updated.Update(shared.RuntimeEventMsg{Event: vault.RuntimeEvent{
		Component: "gateway",
		Event:     "start_failed",
		Timestamp: time.Date(2026, 5, 10, 12, 34, 0, 0, time.UTC),
	}})

	view := updated.View()
	if !strings.Contains(view, "Runtime") || !strings.Contains(view, "GATE") {
		t.Fatalf("default-height system panel missing runtime event:\n%s", view)
	}
}
