package vault

import (
	"os"
	"testing"
)

func TestVaultCRUD(t *testing.T) {
	// Create temp db
	f, err := os.CreateTemp("", "vault-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	defer os.Remove(f.Name())

	db, err := OpenPath(f.Name())
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer db.Close()

	// Create citizen
	c, err := db.CreateCitizen("Test User", `{"ua":"test"}`, "", "en-US", "America/New_York")
	if err != nil {
		t.Fatalf("create citizen: %v", err)
	}
	if c.Label != "Test User" {
		t.Fatalf("expected label 'Test User', got '%s'", c.Label)
	}

	// List citizens
	citizens, err := db.ListCitizens()
	if err != nil {
		t.Fatalf("list citizens: %v", err)
	}
	if len(citizens) != 1 {
		t.Fatalf("expected 1 citizen, got %d", len(citizens))
	}

	// Save cookies
	if err := db.SaveCookies(c.ID, "google.com", `[{"name":"NID","value":"123"}]`); err != nil {
		t.Fatalf("save cookies: %v", err)
	}

	cookies, err := db.GetCookies(c.ID)
	if err != nil {
		t.Fatalf("get cookies: %v", err)
	}
	if len(cookies) != 1 || cookies[0].Domain != "google.com" {
		t.Fatalf("unexpected cookies: %+v", cookies)
	}

	// Create template
	tmpl, err := db.CreateTemplate("Web Researcher", "Read-only browsing", `{"task":"research"}`, "readonly", "[]", "{}")
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	// Create nomad session
	ns, err := db.CreateNomadSession(tmpl.ID, `{"ua":"nomad"}`)
	if err != nil {
		t.Fatalf("create nomad: %v", err)
	}
	if ns.Status != "active" {
		t.Fatalf("expected active, got %s", ns.Status)
	}

	// Complete nomad
	if err := db.CompleteNomadSession(ns.ID, "completed", `{"result":"done"}`); err != nil {
		t.Fatalf("complete nomad: %v", err)
	}

	// Delete citizen (cascades cookies)
	if err := db.DeleteCitizen(c.ID); err != nil {
		t.Fatalf("delete citizen: %v", err)
	}
	citizens, _ = db.ListCitizens()
	if len(citizens) != 0 {
		t.Fatalf("expected 0 citizens after delete, got %d", len(citizens))
	}
}
