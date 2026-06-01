package nanoclaw

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

const vulpineTempPrefix = "vulpineos-"

// cleanupVulpineTempFiles removes stale temp files from previous VulpineOS sessions.
// Called at daemon startup to prevent unbounded accumulation of /tmp/vulpineos-*.log
// and other transient artifacts.
func cleanupVulpineTempFiles() {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, vulpineTempPrefix) {
			continue
		}
		// Only remove known volatile artifacts — never touch persistent state
		if !strings.HasSuffix(name, ".log") &&
			!strings.HasSuffix(name, ".jsonl") &&
			!strings.HasPrefix(name, vulpineTempPrefix+"profile-") &&
			!strings.HasPrefix(name, vulpineTempPrefix+"kernel") &&
			!strings.HasPrefix(name, vulpineTempPrefix+"foxbridge") &&
			!strings.HasPrefix(name, vulpineTempPrefix+"gateway") &&
			!strings.HasPrefix(name, vulpineTempPrefix+"remote-session-") {
			continue
		}
		path := filepath.Join(os.TempDir(), name)
		if err := os.RemoveAll(path); err == nil {
			log.Printf("cleaned stale temp: %s", path)
		}
	}
}

func cleanupStreamFiles(nanoclawDir string) {
	pattern := filepath.Join(nanoclawDir, "data", "v2-sessions", "*", "*", "stream*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	for _, path := range matches {
		if err := os.Remove(path); err == nil {
			log.Printf("cleaned stale stream file: %s", path)
		}
	}
}
