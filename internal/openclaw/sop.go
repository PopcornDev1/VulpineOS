package openclaw

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteSOP writes a Standard Operating Procedure to a temp file for agent injection.
func WriteSOP(sop string) (string, error) {
	dir := os.TempDir()
	path := filepath.Join(dir, fmt.Sprintf("vulpineos-sop-%d.json", os.Getpid()))

	if err := os.WriteFile(path, []byte(sop), 0600); err != nil {
		return "", fmt.Errorf("write SOP file: %w", err)
	}
	return path, nil
}

// CleanupSOP removes a temporary SOP file.
func CleanupSOP(path string) {
	os.Remove(path)
}
