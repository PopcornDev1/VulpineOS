package repohealth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readRepoFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}

func TestInstallScriptContracts(t *testing.T) {
	script := readRepoFile(t, "install.sh")

	if !strings.Contains(script, `VULPINEOS_HOME="${VULPINEOS_HOME:-${HOME}/.vulpineos}"`) {
		t.Fatal("install.sh must allow tests and advanced users to override VULPINEOS_HOME")
	}
	if !strings.Contains(script, "install_camoufox_launcher()") {
		t.Fatal("install.sh must create a platform-aware camoufox launcher")
	}
	if strings.Contains(script, `ln -sf "${browser_bin}" "${bin_dir}/camoufox"`) {
		t.Fatal("install.sh must not symlink directly to macOS app bundle executable; that breaks XPCOM")
	}
}

func TestShortInstallURLDocumented(t *testing.T) {
	for _, name := range []string{"README.md", "docs/release-checklist.md"} {
		if !strings.Contains(readRepoFile(t, name), "https://vulpineos.com/install") {
			t.Fatalf("%s should document the short installer URL", name)
		}
	}
}

func TestBrowserVisibleBrandingUsesVulpine(t *testing.T) {
	userFacingBrandFiles := []string{
		"additions/browser/branding/camoufox/configure.sh",
		"additions/browser/branding/camoufox/locales/en-US/brand.ftl",
		"additions/browser/branding/camoufox/locales/en-US/brand.properties",
		"additions/browser/branding/camoufox/locales/en-US/brand.dtd",
		"additions/browser/base/content/aboutDialog.xhtml",
		"additions/browser/locales/en-US/chrome/overrides/appstrings.properties",
		"additions/browser/components/search/extensions/none/manifest.json",
		"patches/librewolf/disable-data-reporting-at-compile-time.patch",
		"patches/windows-theming-bug-modified.patch",
	}
	for _, name := range userFacingBrandFiles {
		content := readRepoFile(t, name)
		if !strings.Contains(content, "Vulpine") {
			t.Fatalf("%s should use the Vulpine browser brand", name)
		}
		if strings.Contains(content, "Camoufox") {
			t.Fatalf("%s still exposes the old Camoufox user-facing brand", name)
		}
	}
}

func TestRootPackageDeclaresBenchmarkAndHelperScripts(t *testing.T) {
	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "package.json")), &pkg); err != nil {
		t.Fatalf("parse package.json: %v", err)
	}
	if pkg.DevDependencies["playwright-core"] == "" {
		t.Fatal("benchmark:tokens imports playwright-core, so package.json must declare it")
	}
	for _, script := range []string{"foxbridge:docs:build", "foxbridge:puppeteer:test"} {
		if pkg.Scripts[script] == "" {
			t.Fatalf("package.json missing %s script", script)
		}
		if !strings.Contains(pkg.Scripts[script], "npm --prefix local/foxbridge/") {
			t.Fatalf("%s should run through the package-local npm prefix, got %q", script, pkg.Scripts[script])
		}
	}
}

func TestPuppeteerSuiteFailsFastWhenFoxbridgeIsMissing(t *testing.T) {
	script := readRepoFile(t, "local/foxbridge/test/puppeteer/test-all.js")
	for _, want := range []string{
		"preflightFoxbridge",
		"withTimeout",
		"net.createConnection",
		"Start foxbridge first",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("puppeteer test suite missing %q preflight contract", want)
		}
	}
}
