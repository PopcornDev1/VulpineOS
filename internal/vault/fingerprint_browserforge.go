package vault

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// browserForgeTimeout bounds the Python subprocess. BrowserForge's first run
// can be a little slow (model load); subsequent runs are fast.
const browserForgeTimeout = 30 * time.Second

// generateBrowserForge produces a per-context fingerprint config using the
// in-tree BrowserForge pipeline (pythonlib/genfp.py -> generate_context_fingerprint).
// It returns the dotted-key config JSON on success.
//
// This is the public default generator. It requires Python plus the camoufox
// pipeline (browserforge, orjson). When any of that is missing — or the script
// cannot be located — it returns an error so the caller falls back to the
// deterministic offline generator. It never panics and never blocks forever.
func generateBrowserForge(seed, hostOS string) (string, error) {
	script, err := locateGenFPScript()
	if err != nil {
		return "", err
	}
	python, err := locatePython()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), browserForgeTimeout)
	defer cancel()

	args := []string{filepath.Base(script), "--os", hostOS}
	if strings.TrimSpace(seed) != "" {
		args = append(args, "--seed", seed)
	}
	// Pin the Firefox version to the actual engine so the BrowserForge UA does
	// not advertise a different major version than the running Camoufox build.
	args = append(args, "--ff-version", fallbackFirefoxVersion)

	cmd := exec.CommandContext(ctx, python, args...)
	// Run from the pythonlib dir so `import camoufox` resolves to the bundled package.
	cmd.Dir = filepath.Dir(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("browserforge generator timed out after %s", browserForgeTimeout)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("browserforge generator unavailable: %s", msg)
	}

	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		return "", fmt.Errorf("browserforge generator produced no output")
	}
	// Validate it is a non-empty JSON object before trusting it.
	var probe map[string]interface{}
	if err := json.Unmarshal(out, &probe); err != nil {
		return "", fmt.Errorf("browserforge generator emitted invalid JSON: %w", err)
	}
	if len(probe) == 0 {
		return "", fmt.Errorf("browserforge generator emitted an empty config")
	}
	return string(out), nil
}

// locatePython resolves the python interpreter, honoring VULPINE_PYTHON.
func locatePython() (string, error) {
	if p := strings.TrimSpace(os.Getenv("VULPINE_PYTHON")); p != "" {
		return p, nil
	}
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("python interpreter not found (set VULPINE_PYTHON)")
}

// locateGenFPScript finds pythonlib/genfp.py, honoring VULPINE_FINGERPRINT_PY,
// then a set of paths relative to the working directory and the executable.
func locateGenFPScript() (string, error) {
	if p := strings.TrimSpace(os.Getenv("VULPINE_FINGERPRINT_PY")); p != "" {
		if fileExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("VULPINE_FINGERPRINT_PY does not exist: %s", p)
	}

	var candidates []string
	// Walk up from the working directory so the script resolves whether the
	// process runs from the repo root or a subdirectory (e.g. `go test`).
	if wd, err := os.Getwd(); err == nil {
		dir := wd
		for i := 0; i < 6; i++ {
			candidates = append(candidates, filepath.Join(dir, "pythonlib", "genfp.py"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	// Also look relative to the executable for packaged installs.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "pythonlib", "genfp.py"),
			filepath.Join(dir, "..", "pythonlib", "genfp.py"),
		)
	}
	for _, c := range candidates {
		if fileExists(c) {
			if abs, err := filepath.Abs(c); err == nil {
				return abs, nil
			}
			return c, nil
		}
	}
	return "", fmt.Errorf("genfp.py not found (set VULPINE_FINGERPRINT_PY)")
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
