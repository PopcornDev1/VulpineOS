package openclaw

import (
	"fmt"
	"os"
)

// WriteSOP writes a Standard Operating Procedure to a unique temp file for agent injection.
func WriteSOP(sop string) (string, error) {
	f, err := os.CreateTemp("", "vulpineos-sop-*.json")
	if err != nil {
		return "", fmt.Errorf("create SOP temp file: %w", err)
	}
	path := f.Name()

	if _, err := f.WriteString(sop); err != nil {
		f.Close()
		os.Remove(path)
		return "", fmt.Errorf("write SOP file: %w", err)
	}
	f.Close()
	return path, nil
}

// CleanupSOP removes a temporary SOP file.
func CleanupSOP(path string) {
	os.Remove(path)
}
