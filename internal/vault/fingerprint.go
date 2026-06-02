package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"vulpineos/internal/extensions"
)

// browserForgeWarnOnce ensures the "BrowserForge unavailable" warning is logged
// at most once per process.
var browserForgeWarnOnce sync.Once

const fallbackFirefoxVersion = "146.0"

// FingerprintData holds the key fields we display in the TUI and the
// per-context surfaces driven through Page.applyFingerprintOverrides / the
// per-context init script (webgl/audio/fonts/hwconc/oscpu). Keys are the
// camoufox dotted/colon config namespace shared by the BrowserForge bridge and
// the offline fallback.
type FingerprintData struct {
	UserAgent    string   `json:"navigator.userAgent,omitempty"`
	Platform     string   `json:"navigator.platform,omitempty"`
	OsCPU        string   `json:"navigator.oscpu,omitempty"`
	ScreenWidth  int      `json:"screen.width,omitempty"`
	ScreenHeight int      `json:"screen.height,omitempty"`
	Language     string   `json:"navigator.language,omitempty"`
	Languages    []string `json:"navigator.languages,omitempty"`
	Timezone     string   `json:"timezone,omitempty"`
	DeviceScale  float64  `json:"window.devicePixelRatio,omitempty"`

	// Per-context high-entropy surfaces (currently global without per-context
	// application). Present in BrowserForge configs; the offline fallback emits
	// seeds + fonts but not webgl vendor/renderer.
	Fonts               []string `json:"fonts,omitempty"`
	AudioSeed           uint32   `json:"audio:seed,omitempty"`
	FontSpacingSeed     uint32   `json:"fonts:spacing_seed,omitempty"`
	WebGLVendor         string   `json:"webGl:vendor,omitempty"`
	WebGLRenderer       string   `json:"webGl:renderer,omitempty"`
	HardwareConcurrency int      `json:"navigator.hardwareConcurrency,omitempty"`
}

// UnmarshalJSON accepts both the current array form and the older comma-string
// navigator.languages value so persisted fingerprints remain readable.
func (fp *FingerprintData) UnmarshalJSON(data []byte) error {
	type rawFingerprintData FingerprintData
	var raw struct {
		rawFingerprintData
		Languages any `json:"navigator.languages,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*fp = FingerprintData(raw.rawFingerprintData)
	switch v := raw.Languages.(type) {
	case []any:
		fp.Languages = make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				fp.Languages = append(fp.Languages, strings.TrimSpace(s))
			}
		}
	case string:
		for _, part := range strings.Split(v, ",") {
			if s := strings.TrimSpace(part); s != "" {
				fp.Languages = append(fp.Languages, s)
			}
		}
	}
	return nil
}

// HostOS returns the Camoufox OS identifier for the current host.
func HostOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "mac"
	case "windows":
		return "win"
	default:
		return "lin"
	}
}

// DefaultPlatformForHostOS returns a platform string matching the host OS, suitable
// for use with profileFamilyForPlatform or other platform-based profile selection.
func DefaultPlatformForHostOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "MacIntel"
	case "windows":
		return "Win64"
	default:
		return "Linux x86_64"
	}
}

// GenerateFingerprint returns a per-identity fingerprint config (camoufox
// dotted-key JSON) for the host OS, seeded by the supplied stable id.
//
// It generates through three tiers, in priority order:
//
//  1. A registered private FingerprintProvider, when one is available AND it
//     can produce a real value for this identity. A provider that returns
//     ErrUnavailable is skipped — we never substitute a guessed provider value.
//  2. BrowserForge — the public default generator (pythonlib/genfp.py). Used
//     whenever the Python pipeline is installed.
//  3. The deterministic offline fallback — used when neither of the above is
//     available, so generation always succeeds even with no Python present.
func GenerateFingerprint(seed string) (string, error) {
	hostOS := HostOS()

	// Tier 1: private provider (never guess — fall through on unavailable/error).
	if p := extensions.Registry.Fingerprint(); p != nil && p.Available() {
		if fp, err := p.Generate(context.Background(), seed, hostOS); err == nil && strings.TrimSpace(fp) != "" {
			return fp, nil
		}
	}

	// Tier 2: BrowserForge public default (when the Python pipeline is present).
	if fp, err := generateBrowserForge(seed, hostOS); err == nil && strings.TrimSpace(fp) != "" {
		return fp, nil
	} else if err != nil {
		// Loudly warn once: production should run with BrowserForge so agents get
		// full, distribution-realistic fingerprints. The offline fallback is
		// per-context (incl. WebGL vendor/renderer) but lower fidelity.
		browserForgeWarnOnce.Do(func() {
			log.Printf("vault: WARNING — BrowserForge unavailable (%v); using offline fallback fingerprints (reduced realism). Install python3 + browserforge for production.", err)
		})
	}

	// Tier 3: deterministic offline fallback (always available).
	return generateFallback(seed, hostOS)
}

// DefaultUserAgentForHost returns a Firefox-compatible user agent for the
// requested host OS without exposing VulpineOS/Camoufox branding to websites.
func DefaultUserAgentForHost(hostOS string) string {
	switch hostOS {
	case "win":
		return fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:%s) Gecko/20100101 Firefox/%s", fallbackFirefoxVersion, fallbackFirefoxVersion)
	case "mac":
		return fmt.Sprintf("Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:%s) Gecko/20100101 Firefox/%s", fallbackFirefoxVersion, fallbackFirefoxVersion)
	default:
		return fmt.Sprintf("Mozilla/5.0 (X11; Linux x86_64; rv:%s) Gecko/20100101 Firefox/%s", fallbackFirefoxVersion, fallbackFirefoxVersion)
	}
}

// DefaultLocale returns the host locale as a BCP-47-ish tag, falling back to
// a timezone-derived English locale and finally en-US.
func DefaultLocale() string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if loc := normalizeLocale(os.Getenv(key)); loc != "" {
			return loc
		}
	}
	return localeForTimezone(DefaultTimezone())
}

// DefaultTimezone returns the best IANA timezone available without shelling out.
func DefaultTimezone() string {
	if tz := strings.TrimSpace(os.Getenv("TZ")); tz != "" && tz != "Local" {
		return tz
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if idx := strings.LastIndex(target, "zoneinfo/"); idx >= 0 {
			if tz := strings.TrimPrefix(target[idx+len("zoneinfo/"):], "/"); tz != "" {
				return tz
			}
		}
	}
	if loc := time.Now().Location(); loc != nil {
		if name := loc.String(); name != "" && name != "Local" {
			return name
		}
	}
	return "UTC"
}

// LanguagesForLocale returns navigator.languages-style values for a locale.
func LanguagesForLocale(locale string) []string {
	locale = normalizeLocale(locale)
	if locale == "" {
		locale = "en-US"
	}
	parts := strings.Split(locale, "-")
	if len(parts) == 0 || parts[0] == "" || parts[0] == locale {
		return []string{locale}
	}
	return []string{locale, parts[0]}
}

// generateFallback creates a small deterministic profile for the requested OS.
func generateFallback(seed, hostOS string) (string, error) {
	r := rand.New(rand.NewSource(fingerprintSeed(seed, hostOS)))

	type osProfile struct {
		UA       string
		Platform string
		OsCPU    string
		Fonts    []string
		// GL holds {UNMASKED_VENDOR, UNMASKED_RENDERER} pairs in the real
		// per-OS Firefox WebGL format, picked per-seed so offline fallback
		// fingerprints still differ per agent rather than all sharing one
		// global GL string. (BrowserForge is the richer primary source.)
		GL [][2]string
	}

	profileMap := map[string]osProfile{
		"win": {
			UA:       DefaultUserAgentForHost("win"),
			Platform: "Win32",
			OsCPU:    "Windows NT 10.0; Win64; x64",
			Fonts:    []string{"Arial", "Calibri", "Cambria", "Consolas", "Segoe UI", "Tahoma", "Times New Roman", "Verdana"},
			GL: [][2]string{
				{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) UHD Graphics 630 (0x00003E9B) Direct3D11 vs_5_0 ps_5_0, D3D11)"},
				{"Google Inc. (NVIDIA)", "ANGLE (NVIDIA, NVIDIA GeForce RTX 3060 (0x00002503) Direct3D11 vs_5_0 ps_5_0, D3D11)"},
				{"Google Inc. (AMD)", "ANGLE (AMD, AMD Radeon RX 6600 (0x000073FF) Direct3D11 vs_5_0 ps_5_0, D3D11)"},
				{"Google Inc. (Intel)", "ANGLE (Intel, Intel(R) Iris(R) Xe Graphics (0x00009A49) Direct3D11 vs_5_0 ps_5_0, D3D11)"},
			},
		},
		"mac": {
			UA:       DefaultUserAgentForHost("mac"),
			Platform: "MacIntel",
			OsCPU:    "Intel Mac OS X 10.15",
			Fonts:    []string{"Arial", "Helvetica", "Helvetica Neue", "Menlo", "Monaco", "San Francisco", "Times New Roman"},
			GL: [][2]string{
				{"Apple", "Apple M1"},
				{"Apple", "Apple M2"},
				{"Apple", "Apple M3"},
				{"Apple", "Apple M1 Pro"},
				{"Apple", "Apple M2 Pro"},
			},
		},
		"lin": {
			UA:       DefaultUserAgentForHost("lin"),
			Platform: "Linux x86_64",
			OsCPU:    "Linux x86_64",
			Fonts:    []string{"Arial", "DejaVu Sans", "Liberation Mono", "Liberation Sans", "Noto Sans", "Ubuntu"},
			GL: [][2]string{
				{"Mesa", "Mesa Intel(R) UHD Graphics 620 (KBL GT2)"},
				{"Mesa", "Mesa Intel(R) Xe Graphics (TGL GT2)"},
				{"AMD", "AMD Radeon RX 6600 (radeonsi, navi23, LLVM 15.0.7, DRM 3.49, 6.2.0)"},
			},
		},
	}

	p, ok := profileMap[hostOS]
	if !ok {
		p = profileMap["lin"]
	}
	resolutions := [][2]int{{1920, 1080}, {2560, 1440}, {1366, 768}, {1536, 864}, {1440, 900}}
	res := resolutions[r.Intn(len(resolutions))]
	locale := DefaultLocale()
	languages := LanguagesForLocale(locale)
	timezone := DefaultTimezone()
	deviceScale := []float64{1.0, 1.0, 1.5, 2.0}[r.Intn(4)]

	config := map[string]interface{}{
		"navigator.userAgent":           p.UA,
		"navigator.platform":            p.Platform,
		"navigator.oscpu":               p.OsCPU,
		"navigator.language":            locale,
		"navigator.languages":           languages,
		"navigator.hardwareConcurrency": []int{4, 8, 8, 12, 16}[r.Intn(5)],
		"navigator.maxTouchPoints":      0,
		"screen.width":                  res[0],
		"screen.height":                 res[1],
		"screen.colorDepth":             24,
		"screen.pixelDepth":             24,
		"screen.availWidth":             res[0],
		"screen.availHeight":            res[1] - 48,
		"window.outerWidth":             res[0] - r.Intn(20),
		"window.outerHeight":            res[1] - 100 - r.Intn(40),
		"window.devicePixelRatio":       deviceScale,
		"timezone":                      timezone,
		"fonts":                         p.Fonts,
		"canvas:seed":                   r.Uint32(),
		"audio:seed":                    r.Uint32(),
		"fonts:spacing_seed":            r.Uint32(),
	}

	// Per-seed WebGL vendor/renderer so even offline (no BrowserForge) two
	// agents do not collide on the high-entropy GL strings.
	if len(p.GL) > 0 {
		gl := p.GL[r.Intn(len(p.GL))]
		config["webGl:vendor"] = gl[0]
		config["webGl:renderer"] = gl[1]
	}

	data, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal fallback fingerprint: %w", err)
	}
	return string(data), nil
}

func normalizeLocale(locale string) string {
	locale = strings.TrimSpace(locale)
	if locale == "" || locale == "C" || locale == "POSIX" {
		return ""
	}
	if idx := strings.IndexAny(locale, ".@"); idx >= 0 {
		locale = locale[:idx]
	}
	if locale == "" || locale == "C" || locale == "POSIX" {
		return ""
	}
	locale = strings.ReplaceAll(locale, "_", "-")
	parts := strings.Split(locale, "-")
	if len(parts) == 1 {
		if parts[0] == "" {
			return ""
		}
		return strings.ToLower(parts[0])
	}
	for i := range parts {
		if i == 0 {
			parts[i] = strings.ToLower(parts[i])
			continue
		}
		if len(parts[i]) == 2 {
			parts[i] = strings.ToUpper(parts[i])
		}
	}
	return strings.Join(parts, "-")
}

func localeForTimezone(timezone string) string {
	switch {
	case strings.HasPrefix(timezone, "Australia/"):
		return "en-AU"
	case strings.HasPrefix(timezone, "Europe/London"):
		return "en-GB"
	case strings.HasPrefix(timezone, "Europe/"):
		return "en-GB"
	case strings.HasPrefix(timezone, "America/Toronto"), strings.HasPrefix(timezone, "America/Vancouver"):
		return "en-CA"
	default:
		return "en-US"
	}
}

func fingerprintSeed(seed, hostOS string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(hostOS))
	return int64(h.Sum64())
}

// ParseFingerprint extracts key display fields from a fingerprint JSON blob.
func ParseFingerprint(fpJSON string) (*FingerprintData, error) {
	var fp FingerprintData
	if err := json.Unmarshal([]byte(fpJSON), &fp); err != nil {
		return nil, fmt.Errorf("parse fingerprint: %w", err)
	}
	return &fp, nil
}

// FingerprintSummary returns a human-readable one-liner for a fingerprint.
func FingerprintSummary(fpJSON string) string {
	// Try parsing as Camoufox config format (dot-separated keys)
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(fpJSON), &raw); err != nil {
		return "unknown"
	}

	ua, _ := raw["navigator.userAgent"].(string)
	platform, _ := raw["navigator.platform"].(string)
	sw, _ := raw["screen.width"].(float64)
	sh, _ := raw["screen.height"].(float64)

	osName := "Unknown"
	switch {
	case strings.Contains(platform, "Win"):
		osName = "Windows"
	case strings.Contains(platform, "Mac"):
		osName = "macOS"
	case strings.Contains(platform, "Linux"):
		osName = "Linux"
	}

	browser := "Firefox"
	if strings.Contains(ua, "rv:") {
		// Extract Firefox version
		idx := strings.Index(ua, "rv:")
		if idx > 0 {
			end := strings.IndexByte(ua[idx:], ')')
			if end > 0 {
				browser = "Firefox " + ua[idx+3:idx+end]
			}
		}
	}

	if sw > 0 && sh > 0 {
		return fmt.Sprintf("%s / %s / %dx%d", osName, browser, int(sw), int(sh))
	}
	return fmt.Sprintf("%s / %s", osName, browser)
}
