package integration

import (
	"os"
	"path/filepath"
	"testing"

	"vulpineos/internal/juggler"
	"vulpineos/internal/orchestrator"
	"vulpineos/internal/pool"
	"vulpineos/internal/testutil"
	"vulpineos/internal/vault"
)

type testEnv struct {
	FakeJuggler *testutil.FakeJugglerTransport
	Client      *juggler.Client
	Vault       *vault.DB
	Pool        *pool.Pool
	Orch        *orchestrator.Orchestrator
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.createBrowserContext", map[string]string{"browserContextId": "ctx-test"})
	fake.RespondJSON("Browser.removeBrowserContext", map[string]any{})
	fake.RespondJSON("Browser.setCookies", map[string]any{})
	fake.RespondJSON("Browser.setUserAgentOverride", map[string]any{})
	fake.RespondJSON("Browser.setLocaleOverride", map[string]any{})
	fake.RespondJSON("Browser.setTimezoneOverride", map[string]any{})

	client := juggler.NewClient(fake)
	t.Cleanup(func() { client.Close() })

	vdb := openTestVault(t)
	t.Cleanup(func() { vdb.Close() })

	p := pool.New(client, pool.Config{PreWarm: 0, MaxActive: 2, MaxUsesPerSlot: 2})
	if err := p.Start(); err != nil {
		t.Fatalf("pool start: %v", err)
	}
	t.Cleanup(func() { p.Close() })

	o := orchestrator.New(nil, client, vdb, pool.Config{PreWarm: 0, MaxActive: 2, MaxUsesPerSlot: 2}, filepath.Join(t.TempDir(), "missing-nanoclaw"), orchestrator.Opts{})
	t.Cleanup(func() { o.Close() })

	return &testEnv{
		FakeJuggler: fake,
		Client:      client,
		Vault:       vdb,
		Pool:        p,
		Orch:        o,
	}
}

func openTestVault(t *testing.T) *vault.DB {
	f, err := os.CreateTemp("", "integration-test-*.db")
	if err != nil {
		t.Fatalf("create temp db: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	db, err := vault.OpenPath(f.Name())
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	return db
}
