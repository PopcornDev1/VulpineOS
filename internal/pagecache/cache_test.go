package pagecache

import (
	"os"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	c := New("")
	state := &PageState{
		AgentID: "agent-1",
		URL:     "https://example.com",
		Title:   "Example",
		ScrollY: 150,
		FormValues: map[string]string{
			"#search": "hello",
		},
	}
	if err := c.Save(state); err != nil {
		t.Fatal(err)
	}

	loaded := c.Load("agent-1")
	if loaded == nil {
		t.Fatal("Load returned nil")
	}
	if loaded.URL != "https://example.com" {
		t.Errorf("URL = %s", loaded.URL)
	}
	if loaded.Title != "Example" {
		t.Errorf("Title = %s", loaded.Title)
	}
	if loaded.ScrollY != 150 {
		t.Errorf("ScrollY = %f", loaded.ScrollY)
	}
	if loaded.FormValues["#search"] != "hello" {
		t.Error("form value not preserved")
	}
	if loaded.CapturedAt.IsZero() {
		t.Error("CapturedAt not set")
	}
}

func TestLoadNonexistent(t *testing.T) {
	c := New("")
	if c.Load("nonexistent") != nil {
		t.Error("should return nil for unknown agent")
	}
}

func TestDelete(t *testing.T) {
	c := New("")
	c.Save(&PageState{AgentID: "a"})
	c.Delete("a")
	if c.Has("a") {
		t.Error("should not exist after delete")
	}
}

func TestHas(t *testing.T) {
	c := New("")
	if c.Has("x") {
		t.Error("should not have unknown agent")
	}
	c.Save(&PageState{AgentID: "x", URL: "test"})
	if !c.Has("x") {
		t.Error("should have after save")
	}
}

func TestList(t *testing.T) {
	c := New("")
	c.Save(&PageState{AgentID: "a"})
	c.Save(&PageState{AgentID: "b"})
	list := c.List()
	if len(list) != 2 {
		t.Errorf("expected 2, got %d", len(list))
	}
}

func TestDiskPersistence(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	c.Save(&PageState{
		AgentID: "persist-test",
		URL:     "https://example.com",
		Title:   "Persisted",
		HTML:    "<h1>Hello</h1>",
	})

	// Create new cache from same dir — should load from disk
	c2 := New(dir)
	loaded := c2.Load("persist-test")
	if loaded == nil {
		t.Fatal("should load from disk")
	}
	if loaded.URL != "https://example.com" {
		t.Errorf("URL = %s", loaded.URL)
	}
	if loaded.HTML != "<h1>Hello</h1>" {
		t.Error("HTML not persisted")
	}
}

func TestDiskDelete(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	c.Save(&PageState{AgentID: "del-test"})
	c.Delete("del-test")

	// File should be gone
	if _, err := os.Stat(c.filePath("del-test")); err == nil {
		t.Error("file should be deleted from disk")
	}
}

func TestSaveInvalidState(t *testing.T) {
	c := New("")
	if err := c.Save(nil); err == nil {
		t.Error("should error on nil state")
	}
	if err := c.Save(&PageState{}); err == nil {
		t.Error("should error on empty agentId")
	}
}

func TestMultipleAgentsIsolated(t *testing.T) {
	c := New("")
	c.Save(&PageState{AgentID: "a", URL: "url-a"})
	c.Save(&PageState{AgentID: "b", URL: "url-b"})

	a := c.Load("a")
	b := c.Load("b")
	if a.URL != "url-a" || b.URL != "url-b" {
		t.Error("agent states should be isolated")
	}
}
