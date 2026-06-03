// Package rendercohort holds the public, deterministic logic that assigns an
// agent to one of the available real-device render cohorts. The cohort DATA
// (and the opaque render config blobs) come from an optional private provider
// via the extensions seam; this package only decides which available cohort a
// given agent should present, so the decision is auditable in the open-source
// tree without exposing any render-fingerprint detail.
package rendercohort

import (
	"hash/fnv"
	"sort"
	"strings"

	"vulpineos/internal/extensions"
)

// Assign deterministically selects a render cohort for an agent from the
// cohorts whose OS matches the agent's claimed OS. The same (seed, claimedOS,
// cohort set) always yields the same cohort, so an agent keeps a stable render
// identity across restarts and context recycling. Returns ok=false when no
// cohort matches the claimed OS — the caller then leaves per-context render
// isolation off for that agent and falls back to the synthetic surfaces, rather
// than presenting a mismatched-OS render profile (which would be incoherent).
func Assign(cohorts []extensions.RenderCohort, seed, claimedOS string) (extensions.RenderCohort, bool) {
	os := NormalizeOS(claimedOS)
	if os == "" {
		return extensions.RenderCohort{}, false
	}
	pool := make([]extensions.RenderCohort, 0, len(cohorts))
	for _, c := range cohorts {
		if strings.TrimSpace(c.Key) == "" {
			continue
		}
		if NormalizeOS(c.OS) == os {
			pool = append(pool, c)
		}
	}
	if len(pool) == 0 {
		return extensions.RenderCohort{}, false
	}
	// Sort by key so selection is stable regardless of provider enumeration order.
	sort.Slice(pool, func(i, j int) bool { return pool[i].Key < pool[j].Key })

	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	// A stable fraction in [0,1) derived from the agent seed.
	frac := float64(h.Sum32()) / float64(1<<32)

	// Prevalence-weighted selection only when the data is COMPLETE for this OS
	// pool (every cohort has a positive Weight). Mixed/absent weights would
	// silently drop cohorts, so we fall back to uniform unless all are known —
	// this keeps the fleet's device mix matched to the real population once
	// prevalence data is available, and unbiased uniform until then.
	total := 0.0
	allWeighted := true
	for _, c := range pool {
		if c.Weight <= 0 {
			allWeighted = false
			break
		}
		total += c.Weight
	}
	if allWeighted && total > 0 {
		point := frac * total
		acc := 0.0
		for _, c := range pool {
			acc += c.Weight
			if point < acc {
				return c, true
			}
		}
		return pool[len(pool)-1], true // floating-point guard
	}

	idx := int(h.Sum32() % uint32(len(pool)))
	return pool[idx], true
}

// NormalizeOS maps a platform / OS / user-agent fragment to the camoufox OS
// identifier ("mac"/"win"/"lin"), or "" when it cannot be classified. It accepts
// the values that show up across the fingerprint surfaces: navigator.platform
// ("MacIntel", "Win32", "Linux x86_64"), camoufox OS ids, and OS names.
func NormalizeOS(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "mac") || strings.Contains(s, "darwin") ||
		strings.Contains(s, "iphone") || strings.Contains(s, "ipad"):
		return "mac"
	case strings.Contains(s, "win"):
		return "win"
	case strings.Contains(s, "lin") || strings.Contains(s, "x11") ||
		strings.Contains(s, "ubuntu") || strings.Contains(s, "fedora") ||
		strings.Contains(s, "debian"):
		return "lin"
	default:
		return ""
	}
}
