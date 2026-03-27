package pool

import (
	"testing"

	"vulpineos/internal/juggler"
)

func TestDefaultMemoryConfig(t *testing.T) {
	cfg := DefaultMemoryConfig()
	if cfg.MaxPerContextMB != 0 {
		t.Fatalf("expected MaxPerContextMB=0, got %d", cfg.MaxPerContextMB)
	}
	if cfg.CheckIntervalS != 30 {
		t.Fatalf("expected CheckIntervalS=30, got %d", cfg.CheckIntervalS)
	}
	if cfg.KillOnExceed {
		t.Fatal("expected KillOnExceed=false")
	}
}

func TestNewMemoryMonitor(t *testing.T) {
	tr := newAutoRespondTransport()
	client := juggler.NewClient(tr)
	defer client.Close()

	p := New(client, DefaultConfig())

	cfg := MemoryConfig{
		MaxPerContextMB: 512,
		CheckIntervalS:  10,
		KillOnExceed:    true,
	}
	mon := NewMemoryMonitor(p, client, cfg)
	if mon == nil {
		t.Fatal("NewMemoryMonitor returned nil")
	}
	if mon.config.MaxPerContextMB != 512 {
		t.Fatalf("expected MaxPerContextMB=512, got %d", mon.config.MaxPerContextMB)
	}
	if mon.config.CheckIntervalS != 10 {
		t.Fatalf("expected CheckIntervalS=10, got %d", mon.config.CheckIntervalS)
	}
}

func TestMemoryMonitor_DefaultCheckInterval(t *testing.T) {
	tr := newAutoRespondTransport()
	client := juggler.NewClient(tr)
	defer client.Close()

	p := New(client, DefaultConfig())

	cfg := MemoryConfig{CheckIntervalS: 0} // should default to 30
	mon := NewMemoryMonitor(p, client, cfg)
	if mon.config.CheckIntervalS != 30 {
		t.Fatalf("expected default CheckIntervalS=30, got %d", mon.config.CheckIntervalS)
	}
}

func TestMemoryMonitor_GetUsageEmpty(t *testing.T) {
	tr := newAutoRespondTransport()
	client := juggler.NewClient(tr)
	defer client.Close()

	p := New(client, DefaultConfig())
	mon := NewMemoryMonitor(p, client, DefaultMemoryConfig())

	usage := mon.GetUsage()
	if len(usage) != 0 {
		t.Fatalf("expected empty usage map, got %d entries", len(usage))
	}
}

func TestMemoryMonitor_StopDoesNotPanic(t *testing.T) {
	tr := newAutoRespondTransport()
	client := juggler.NewClient(tr)
	defer client.Close()

	p := New(client, DefaultConfig())
	mon := NewMemoryMonitor(p, client, DefaultMemoryConfig())
	mon.Start()
	mon.Stop()
	mon.Stop() // double stop should not panic
}

func TestMemoryMonitor_Config(t *testing.T) {
	tr := newAutoRespondTransport()
	client := juggler.NewClient(tr)
	defer client.Close()

	p := New(client, DefaultConfig())
	cfg := MemoryConfig{MaxPerContextMB: 256, CheckIntervalS: 15, KillOnExceed: true}
	mon := NewMemoryMonitor(p, client, cfg)

	got := mon.Config()
	if got.MaxPerContextMB != 256 {
		t.Fatalf("expected 256, got %d", got.MaxPerContextMB)
	}
	if !got.KillOnExceed {
		t.Fatal("expected KillOnExceed=true")
	}
}
