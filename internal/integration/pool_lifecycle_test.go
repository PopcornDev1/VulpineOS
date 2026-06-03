package integration

import (
	"encoding/json"
	"testing"

	"vulpineos/internal/juggler"
	"vulpineos/internal/pool"
	"vulpineos/internal/testutil"
)

func TestPoolLifecycle_AcquireContext(t *testing.T) {
	env := newTestEnv(t)

	slot, err := env.Pool.Acquire()
	if err != nil {
		t.Fatalf("pool acquire: %v", err)
	}
	if slot == nil {
		t.Fatal("slot is nil")
	}
	if slot.ContextID == "" {
		t.Fatal("context id is empty")
	}

	env.Pool.Release(slot)

	createCalls := env.FakeJuggler.CallsByMethod("Browser.createBrowserContext")
	if len(createCalls) != 1 {
		t.Fatalf("createBrowserContext calls = %d, want 1", len(createCalls))
	}
}

func TestPoolLifecycle_ReuseWithinLimit(t *testing.T) {
	env := newTestEnv(t)

	slot1, err := env.Pool.Acquire()
	if err != nil {
		t.Fatalf("pool acquire: %v", err)
	}

	env.Pool.Release(slot1)

	slot2, err := env.Pool.Acquire()
	if err != nil {
		t.Fatalf("pool acquire: %v", err)
	}

	if slot1.ContextID != slot2.ContextID {
		t.Fatalf("context not reused: %q vs %q", slot1.ContextID, slot2.ContextID)
	}

	createCalls := env.FakeJuggler.CallsByMethod("Browser.createBrowserContext")
	if len(createCalls) != 1 {
		t.Fatalf("createBrowserContext calls = %d, want 1", len(createCalls))
	}
}

func TestPoolLifecycle_RemoveOnLimitHit(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)

	ctxCounter := 0
	fake.RespondFunc("Browser.createBrowserContext", func(*juggler.Message) (json.RawMessage, *juggler.Error) {
		ctxCounter++
		data, _ := json.Marshal(map[string]string{"browserContextId": "ctx-" + string(rune('a'+ctxCounter-1))})
		return data, nil
	})
	fake.RespondJSON("Browser.removeBrowserContext", map[string]any{})
	fake.RespondJSON("Browser.setCookies", map[string]any{})
	fake.RespondJSON("Browser.setUserAgentOverride", map[string]any{})
	fake.RespondJSON("Browser.setLocaleOverride", map[string]any{})
	fake.RespondJSON("Browser.setTimezoneOverride", map[string]any{})

	client := juggler.NewClient(fake)
	t.Cleanup(func() { client.Close() })

	p := pool.New(client, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 1})
	if err := p.Start(); err != nil {
		t.Fatalf("pool start: %v", err)
	}
	t.Cleanup(func() { p.Close() })

	slot1, err := p.Acquire()
	if err != nil {
		t.Fatalf("pool acquire: %v", err)
	}

	p.Release(slot1)

	slot2, err := p.Acquire()
	if err != nil {
		t.Fatalf("pool acquire: %v", err)
	}

	if slot1.ContextID == slot2.ContextID {
		t.Fatal("context should NOT be reused after max uses exceeded")
	}

	removeCalls := fake.CallsByMethod("Browser.removeBrowserContext")
	if len(removeCalls) != 1 {
		t.Fatalf("removeBrowserContext calls = %d, want 1", len(removeCalls))
	}

	createCalls := fake.CallsByMethod("Browser.createBrowserContext")
	if len(createCalls) != 2 {
		t.Fatalf("createBrowserContext calls = %d, want 2", len(createCalls))
	}
}

func TestPoolLifecycle_PoolClosedFails(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.createBrowserContext", map[string]string{"browserContextId": "ctx-test"})
	fake.RespondJSON("Browser.removeBrowserContext", map[string]any{})

	client := juggler.NewClient(fake)
	t.Cleanup(func() { client.Close() })

	p := pool.New(client, pool.Config{PreWarm: 0, MaxActive: 1, MaxUsesPerSlot: 2})
	if err := p.Start(); err != nil {
		t.Fatalf("pool start: %v", err)
	}
	t.Cleanup(func() { p.Close() })

	slot, err := p.Acquire()
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	_ = slot

	p.Close()

	_, err = p.Acquire()
	if err == nil {
		t.Fatal("expected error when pool is closed")
	}
}
