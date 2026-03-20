package openclaw

import (
	"os"
	"strings"
	"testing"
)

func TestWriteSOP_CreatesUniqueFiles(t *testing.T) {
	sop1 := `{"task": "browse", "url": "https://example.com"}`
	sop2 := `{"task": "search", "query": "test"}`

	path1, err := WriteSOP(sop1)
	if err != nil {
		t.Fatalf("WriteSOP(sop1): %v", err)
	}
	defer CleanupSOP(path1)

	path2, err := WriteSOP(sop2)
	if err != nil {
		t.Fatalf("WriteSOP(sop2): %v", err)
	}
	defer CleanupSOP(path2)

	if path1 == path2 {
		t.Fatalf("expected unique paths, got same: %s", path1)
	}

	if !strings.Contains(path1, "vulpineos-sop-") {
		t.Errorf("path1 should contain vulpineos-sop-, got: %s", path1)
	}

	// Verify contents
	data1, err := os.ReadFile(path1)
	if err != nil {
		t.Fatalf("read path1: %v", err)
	}
	if string(data1) != sop1 {
		t.Errorf("sop1 content mismatch: got %q", string(data1))
	}

	data2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatalf("read path2: %v", err)
	}
	if string(data2) != sop2 {
		t.Errorf("sop2 content mismatch: got %q", string(data2))
	}
}

func TestCleanupSOP_RemovesFile(t *testing.T) {
	sop := `{"task": "test"}`

	path, err := WriteSOP(sop)
	if err != nil {
		t.Fatalf("WriteSOP: %v", err)
	}

	// File should exist
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file should exist after WriteSOP: %v", err)
	}

	CleanupSOP(path)

	// File should no longer exist
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should not exist after CleanupSOP, got err: %v", err)
	}
}
