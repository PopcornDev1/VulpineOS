package main

import (
	"embed"
	"io/fs"
)

//go:embed all:panel
var panelFS embed.FS

// PanelFS returns the embedded web panel filesystem (rooted at panel/).
// Returns nil if the panel directory doesn't exist in the embed.
func PanelFS() fs.FS {
	sub, err := fs.Sub(panelFS, "panel")
	if err != nil {
		return nil
	}
	// Check if it has any files
	entries, err := fs.ReadDir(sub, ".")
	if err != nil || len(entries) == 0 {
		return nil
	}
	return sub
}
