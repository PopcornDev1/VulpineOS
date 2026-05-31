package nanoclaw

import (
	"log"
	"os"
	"path/filepath"
)

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
