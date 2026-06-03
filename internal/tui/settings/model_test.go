package settings

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"vulpineos/internal/tui/shared"
)

func TestConstrainedHeightShowsFocusedSection(t *testing.T) {
	m := New()
	m.SetActive(true)
	m.SetSize(36, 8)

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("unexpected message after first tab: %#v", msg)
		}
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("unexpected message after second tab: %#v", msg)
		}
	}

	view := m.View()
	if !strings.Contains(view, "Skills") {
		t.Fatalf("constrained settings view did not show focused section:\n%s", view)
	}
	if strings.Contains(view, "General") && strings.Index(view, "General") < strings.Index(view, "Skills") {
		t.Fatalf("constrained settings view kept earlier sections above focused section:\n%s", view)
	}
}

func TestMidHeightShowsFocusedSection(t *testing.T) {
	m := New()
	m.SetActive(true)
	m.SetSize(36, 16)

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("unexpected message after first tab: %#v", msg)
		}
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("unexpected message after second tab: %#v", msg)
		}
	}

	view := m.View()
	if !strings.Contains(view, "Skills") {
		t.Fatalf("mid-height settings view did not show focused section:\n%s", view)
	}
	if strings.Contains(view, "General") && strings.Index(view, "General") < strings.Index(view, "Skills") {
		t.Fatalf("mid-height settings view kept earlier sections above focused section:\n%s", view)
	}
	if lines := strings.Split(view, "\n"); len(lines) > 16 {
		t.Fatalf("mid-height settings view height = %d, want <= 16:\n%s", len(lines), view)
	}
}

func TestMediumHeightAboveCompactShowsFocusedSection(t *testing.T) {
	m := New()
	m.SetActive(true)
	m.SetSize(36, 19)

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("unexpected message after first tab: %#v", msg)
		}
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			t.Fatalf("unexpected message after second tab: %#v", msg)
		}
	}

	view := m.View()
	if !strings.Contains(view, "Skills") {
		t.Fatalf("medium-height settings view did not show focused section:\n%s", view)
	}
	if lines := strings.Split(view, "\n"); len(lines) > 19 {
		t.Fatalf("medium-height settings view height = %d, want <= 19:\n%s", len(lines), view)
	}
}

func TestGeneralSettingsShowsReconfigureHint(t *testing.T) {
	m := New()
	m.SetActive(true)
	m.SetSize(80, 20)

	view := m.View()
	if !strings.Contains(view, "[c] reconfigure") {
		t.Fatalf("settings should show reconfigure hint:\n%s", view)
	}
	if !strings.Contains(view, "[Esc] close") {
		t.Fatalf("settings should show close hint:\n%s", view)
	}
	if strings.Contains(view, "mode:") {
		t.Fatalf("settings should not display mode indicator:\n%s", view)
	}
}

func TestProxyTestedMsgUpdatesLatencyAndCountry(t *testing.T) {
	m := New()
	m.SetActive(true)
	m.SetProxies([]ProxyItem{{
		ID:      "proxy-1",
		Label:   "edge",
		Type:    "socks5",
		Host:    "127.0.0.1",
		Port:    1080,
		Country: "",
		Latency: "untested",
	}})

	updated, cmd := m.Update(shared.ProxyTestedMsg{
		ProxyID: "proxy-1",
		Latency: "42ms",
		Country: "United Kingdom",
	})
	if cmd != nil {
		t.Fatalf("unexpected command: %#v", cmd())
	}

	got := updated.proxies[0]
	if got.Latency != "42ms" {
		t.Fatalf("latency = %q, want 42ms", got.Latency)
	}
	if got.Country != "United Kingdom" {
		t.Fatalf("country = %q, want United Kingdom", got.Country)
	}
}

func TestProxyTestRequestUsesStoredConfig(t *testing.T) {
	m := New()
	m.SetActive(true)
	m.SetProxies([]ProxyItem{{
		ID:      "proxy-1",
		Label:   "auth-proxy",
		Type:    "socks5",
		Host:    "127.0.0.1",
		Port:    1080,
		Config:  `{"type":"socks5","host":"127.0.0.1","port":1080,"username":"user","password":"pass"}`,
		Latency: "untested",
	}})

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("unexpected command after tab: %#v", cmd())
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd == nil {
		t.Fatal("expected proxy test command")
	}

	msg, ok := cmd().(shared.ProxyTestRequestMsg)
	if !ok {
		t.Fatalf("command returned %T, want ProxyTestRequestMsg", cmd())
	}
	if msg.Config != `{"type":"socks5","host":"127.0.0.1","port":1080,"username":"user","password":"pass"}` {
		t.Fatalf("config = %s", msg.Config)
	}
}

func TestProxyListKeepsSelectionVisibleWhenCropped(t *testing.T) {
	m := New()
	m.SetActive(true)
	m.SetSize(40, 10)
	proxies := make([]ProxyItem, 20)
	for i := range proxies {
		proxies[i] = ProxyItem{
			ID:      fmt.Sprintf("proxy-%02d", i),
			Label:   fmt.Sprintf("proxy-%02d", i),
			Type:    "http",
			Host:    "127.0.0.1",
			Port:    8000 + i,
			Latency: "untested",
		}
	}
	m.SetProxies(proxies)

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("unexpected command after tab: %#v", cmd())
	}
	for i := 0; i < 18; i++ {
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		if cmd != nil {
			t.Fatalf("unexpected command after j: %#v", cmd())
		}
	}

	view := m.View()
	if !strings.Contains(view, "proxy-18") {
		t.Fatalf("selected proxy not visible:\n%s", view)
	}
}

func TestSkillListKeepsSelectionVisibleWhenCropped(t *testing.T) {
	m := New()
	m.SetActive(true)
	m.SetSize(40, 10)
	skills := make([]SkillItem, 20)
	for i := range skills {
		skills[i] = SkillItem{Name: fmt.Sprintf("skill-%02d", i)}
	}
	m.SetSkills(skills)

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("unexpected command after first tab: %#v", cmd())
	}
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("unexpected command after second tab: %#v", cmd())
	}
	for i := 0; i < 18; i++ {
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
		if cmd != nil {
			t.Fatalf("unexpected command after j: %#v", cmd())
		}
	}

	view := m.View()
	if !strings.Contains(view, "skill-18") {
		t.Fatalf("selected skill not visible:\n%s", view)
	}
}

func TestSelectedRowsClipWithoutBrokenANSI(t *testing.T) {
	m := New()
	m.SetActive(true)
	m.SetSize(12, 8)
	m.SetProxies([]ProxyItem{{
		ID:      "proxy-1",
		Label:   "very-long-proxy-label",
		Type:    "socks5",
		Host:    "127.0.0.1",
		Port:    1080,
		Country: "United Kingdom",
		Latency: "untested",
	}})

	var cmd tea.Cmd
	m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Fatalf("unexpected command after tab: %#v", cmd())
	}

	view := m.View()
	assertNoBrokenANSI(t, view)
}

func TestClipTextDoesNotSplitANSISequences(t *testing.T) {
	clipped := clipText("\x1b[32m[+] enabled\x1b[0m", 2)
	assertNoBrokenANSI(t, clipped)
	if !strings.Contains(clipped, "\x1b[0m") {
		t.Fatalf("clipped styled text missing reset: %q", clipped)
	}
	if width := lipgloss.Width(clipped); width > 2 {
		t.Fatalf("clipped width = %d, want <= 2: %q", width, clipped)
	}
}

var ansiSequencePattern = regexp.MustCompile(`\x1b\[[0-9;:]*m`)

func assertNoBrokenANSI(t *testing.T, view string) {
	t.Helper()
	withoutComplete := ansiSequencePattern.ReplaceAllString(view, "")
	if strings.Contains(withoutComplete, "\x1b") {
		t.Fatalf("view contains a partial ANSI sequence:\n%q", view)
	}
}
