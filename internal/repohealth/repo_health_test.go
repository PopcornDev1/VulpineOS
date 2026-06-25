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
	if !strings.Contains(script, "install_browser_launchers()") {
		t.Fatal("install.sh must create platform-aware browser launchers")
	}
	for _, want := range []string{
		`"${root}/vulpine"`,
		`"${root}/Vulpine.app/Contents/MacOS/vulpine"`,
		`local primary_launcher="${bin_dir}/vulpine"`,
		`local legacy_launcher="${bin_dir}/camoufox"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("install.sh missing Vulpine browser compatibility contract %q", want)
		}
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

func TestBrowserBuildConfigUsesVulpineExecutableName(t *testing.T) {
	configure := readRepoFile(t, "additions/browser/branding/camoufox/configure.sh")
	for _, want := range []string{
		"MOZ_APP_NAME=vulpine",
		"MOZ_APP_BASENAME=Vulpine",
		"MOZ_APP_DISPLAYNAME=Vulpine",
		"MOZ_APP_REMOTINGNAME=vulpine",
	} {
		if !strings.Contains(configure, want) {
			t.Fatalf("configure.sh missing %q", want)
		}
	}
	compilePatch := readRepoFile(t, "patches/librewolf/disable-data-reporting-at-compile-time.patch")
	if !strings.Contains(compilePatch, `imply_option("MOZ_APP_PROFILE", "vulpine")`) {
		t.Fatal("compile-time browser profile name should be vulpine")
	}
	mozconfig := readRepoFile(t, "assets/base.mozconfig")
	if !strings.Contains(mozconfig, "ac_add_options --with-app-name=vulpine") {
		t.Fatal("base mozconfig should build the browser executable as vulpine")
	}
	if strings.Contains(mozconfig, "ac_add_options --with-app-name=camoufox") {
		t.Fatal("base mozconfig still forces the legacy camoufox executable name")
	}
}

func TestBrowserAutoconfigResourceUsesVulpineName(t *testing.T) {
	localSettings := readRepoFile(t, "settings/defaults/pref/local-settings.js")
	if !strings.Contains(localSettings, `pref("general.config.filename", "vulpine.cfg");`) {
		t.Fatal("browser autoconfig should load vulpine.cfg")
	}

	for _, name := range []string{
		"patches/config.patch",
		"scripts/copy-additions.sh",
	} {
		content := readRepoFile(t, name)
		if !strings.Contains(content, "vulpine.cfg") {
			t.Fatalf("%s should package vulpine.cfg", name)
		}
		if strings.Contains(content, "camoufox.cfg") {
			t.Fatalf("%s should not package the legacy camoufox.cfg resource name", name)
		}
	}
}

func TestMacPackagingStripsLegacyHelperExecutables(t *testing.T) {
	packageScript := readRepoFile(t, "scripts/package.py")
	for _, want := range []string{
		"MACOS_HELPER_EXECUTABLES",
		"Camoufox GPU Helper",
		"Vulpine GPU Helper",
		"Camoufox Media Plugin Helper",
		"Vulpine Media Plugin Helper",
		"os.remove(old_helper)",
		"helper_plist['CFBundleExecutable'] = new_name",
	} {
		if !strings.Contains(packageScript, want) {
			t.Fatalf("scripts/package.py missing mac helper packaging contract %q", want)
		}
	}
}

func TestPackagingCleansStaleLegacyBrowserPackages(t *testing.T) {
	packageScript := readRepoFile(t, "scripts/package.py")
	for _, want := range []string{
		"cleanup_stale_browser_packages",
		"glob.glob(os.path.join(dist_dir, pattern))",
		"os.remove(path)",
		"camoufox-{version}-{release}",
	} {
		if !strings.Contains(packageScript, want) {
			t.Fatalf("scripts/package.py missing stale package cleanup contract %q", want)
		}
	}
}

func TestNoSearchEnginesPatchUsesSearchConfigV2Records(t *testing.T) {
	patch := readRepoFile(t, "patches/no-search-engines.patch")
	for _, want := range []string{
		`recordType: "engine"`,
		`identifier: "none"`,
		`recordType: "defaultEngines"`,
		`globalDefault: "none"`,
		`recordType: "engineOrders"`,
		`recordType: "availableLocales"`,
	} {
		if !strings.Contains(patch, want) {
			t.Fatalf("patches/no-search-engines.patch missing search-config-v2 contract %q", want)
		}
	}
	for _, legacy := range []string{`"appliesTo"`, `none@mozilla.org`} {
		if strings.Contains(patch, legacy) {
			t.Fatalf("patches/no-search-engines.patch still contains legacy invalid search config shape %q", legacy)
		}
	}
}

func TestDistributionPolicyDoesNotDuplicateNoneSearchEngine(t *testing.T) {
	var policy struct {
		Policies struct {
			SearchEngines map[string]any `json:"SearchEngines"`
		} `json:"policies"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "settings/distribution/policies.json")), &policy); err != nil {
		t.Fatalf("parse settings/distribution/policies.json: %v", err)
	}
	searchEngines := policy.Policies.SearchEngines
	if searchEngines["Default"] != "None" {
		t.Fatalf("SearchEngines.Default should remain None, got %#v", searchEngines["Default"])
	}
	if _, ok := searchEngines["Add"]; ok {
		t.Fatal("SearchEngines.Add must not add a duplicate None engine; the no-search patch supplies the inert v2 engine")
	}
}

func TestReleaseWorkflowDoesNotRewrapUpstreamBrowser(t *testing.T) {
	workflow := readRepoFile(t, ".github/workflows/build.yml")
	for _, forbidden := range []string{
		"CAMOUFOX_REPO",
		"daijro/camoufox",
		"Download upstream browser binary",
		"camoufox.zip",
		"camoufox-*-${{ matrix.plat }}.${{ matrix.arch }}.zip",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("release workflow must not rewrap upstream browser assets; found %q", forbidden)
		}
	}
	for _, want := range []string{
		"VulpineOS CLI",
		"Upload CLI artifact",
		"Create checksums",
		"Trusted browser artifacts are uploaded manually before publishing",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("release workflow missing trusted release contract %q", want)
		}
	}
}

func TestReleaseChecklistMatchesTrustedBrowserArtifactFlow(t *testing.T) {
	checklist := readRepoFile(t, "docs/release-checklist.md")
	for _, want := range []string{
		"does not build or rewrap browser packages",
		"Upload trusted browser artifacts manually",
		"Do not publish the draft release until",
	} {
		if !strings.Contains(checklist, want) {
			t.Fatalf("release checklist missing browser artifact gate %q", want)
		}
	}
}

func TestPublicHistoryAuditAllowlistsReviewedHistoricalPaths(t *testing.T) {
	audit := readRepoFile(t, "scripts/public-history-audit.py")
	for _, want := range []string{
		"KNOWN_DIFF_HISTORY_FINDINGS",
		`":(glob,exclude)**/vendor/**"`,
		"36212ffa5488fbb87eacc2c9eba4d3f74bc7e1a7",
		"3092fd279bd96043c072e4178e9e51b3fd1dbf15",
		"6f56cebe42820e3ac7182357c00283f83066d107",
		"2f669b136646ae17ba398d53cab4fcb698213e5c",
		"2c9d59f8e0564022d79fdea34e86403d26f547a9",
		"reviewed historical absolute path removal",
	} {
		if !strings.Contains(audit, want) {
			t.Fatalf("public history audit missing reviewed finding contract %q", want)
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
