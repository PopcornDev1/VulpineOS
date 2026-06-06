package vault

import (
	"testing"
)

func openTestVault(t *testing.T) *DB {
	t.Helper()
	db, err := OpenPath(t.TempDir() + "/vault.db")
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRoleSeedCRUD(t *testing.T) {
	db := openTestVault(t)

	// Create
	seed, err := db.CreateRoleSeed("market-researcher", "You are a market research specialist.", []string{"research", "market"})
	if err != nil {
		t.Fatalf("create role seed: %v", err)
	}
	if seed.Name != "market-researcher" {
		t.Errorf("expected name 'market-researcher', got %q", seed.Name)
	}

	// Get by name
	got, err := db.GetRoleSeedByName("market-researcher")
	if err != nil {
		t.Fatalf("get role seed by name: %v", err)
	}
	if got.Content != seed.Content {
		t.Errorf("content mismatch: got %q, want %q", got.Content, seed.Content)
	}

	// List
	list, err := db.ListRoleSeeds()
	if err != nil {
		t.Fatalf("list role seeds: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 role seed, got %d", len(list))
	}

	// Increment usage
	if err := db.IncrementRoleSeedUse(seed.ID); err != nil {
		t.Fatalf("increment role seed use: %v", err)
	}
	got2, err := db.GetRoleSeedByName("market-researcher")
	if err != nil {
		t.Fatalf("get role seed after increment: %v", err)
	}
	if got2.Used != 1 {
		t.Errorf("expected Used=1 after increment, got %d", got2.Used)
	}

	// Find by tag
	found, err := db.FindRoleSeeds("research")
	if err != nil {
		t.Fatalf("find role seeds: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("expected at least one result for tag query 'research'")
	}

	// Find by name fragment
	found, err = db.FindRoleSeeds("market")
	if err != nil {
		t.Fatalf("find role seeds by name: %v", err)
	}
	if len(found) == 0 {
		t.Fatal("expected at least one result for name query 'market'")
	}

	// Delete
	if err := db.DeleteRoleSeed(seed.ID); err != nil {
		t.Fatalf("delete role seed: %v", err)
	}
	list, err = db.ListRoleSeeds()
	if err != nil {
		t.Fatalf("list role seeds after delete: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(list))
	}
}
