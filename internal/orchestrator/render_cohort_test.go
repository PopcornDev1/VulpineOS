package orchestrator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"vulpineos/internal/extensions"
	"vulpineos/internal/juggler"
	"vulpineos/internal/testutil"
)

type fakeRenderCohortProvider struct {
	cohorts []extensions.RenderCohort
	avail   bool
	calls   int
}

func (f *fakeRenderCohortProvider) Available() bool { return f.avail }
func (f *fakeRenderCohortProvider) Cohorts(context.Context) ([]extensions.RenderCohort, error) {
	f.calls++
	if !f.avail {
		return nil, extensions.ErrUnavailable
	}
	return f.cohorts, nil
}

// prefName extracts the single roverfox pref name set by a setContextFingerprint
// call (each cohort call carries exactly one pref).
func prefNamesValues(t *testing.T, call testutil.JugglerCall) map[string]string {
	t.Helper()
	var params struct {
		Prefs []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"prefs"`
	}
	if err := json.Unmarshal(call.Params, &params); err != nil {
		t.Fatalf("unmarshal setContextFingerprint params: %v", err)
	}
	out := map[string]string{}
	for _, p := range params.Prefs {
		out[p.Name] = p.Value
	}
	return out
}

func TestApplyRenderCohortDeliversBlobsOnceAndPinsContext(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.setContextFingerprint", map[string]any{})
	client := juggler.NewClient(fake)
	defer client.Close()

	provider := &fakeRenderCohortProvider{
		avail: true,
		cohorts: []extensions.RenderCohort{
			{Key: "mac_a_aaa", ProfileID: "p-mac-a", OS: "mac", ConfigBlob: "blobA"},
			{Key: "mac_b_bbb", ProfileID: "p-mac-b", OS: "mac", ConfigBlob: "blobB"},
			{Key: "win_a_ccc", ProfileID: "p-win-a", OS: "win", ConfigBlob: "blobC"},
			{Key: "skip_empty", ProfileID: "p-x", OS: "mac", ConfigBlob: ""}, // dropped
		},
	}
	extensions.Registry.SetRenderCohort(provider)
	t.Cleanup(func() { extensions.Registry.SetRenderCohort(nil) })

	o := &Orchestrator{Client: client}

	// First two agents (mac) on distinct contexts.
	o.applyRenderCohort(7, "agent-1", "MacIntel")
	o.applyRenderCohort(8, "agent-2", "MacIntel")
	// A windows agent.
	o.applyRenderCohort(9, "agent-3", "Win32")

	if provider.calls != 1 {
		t.Fatalf("provider.Cohorts should be queried exactly once, got %d", provider.calls)
	}

	calls := fake.CallsByMethod("Browser.setContextFingerprint")

	var (
		enabledSet  bool
		cfgKeys     = map[string]string{}
		cohortPins  = map[string]string{} // pref name -> value
	)
	for _, c := range calls {
		for name, val := range prefNamesValues(t, c) {
			switch {
			case name == "roverfox.s.renderlab_pc_enabled":
				enabledSet = enabledSet || val == "1"
			case strings.HasPrefix(name, "roverfox.s.renderlab_cfg_"):
				cfgKeys[name] = val
			case strings.HasPrefix(name, "roverfox.s.renderlab_cohort_"):
				cohortPins[name] = val
			}
		}
	}

	if !enabledSet {
		t.Fatal("per-context render mode flag was not set")
	}
	// Three valid cohorts delivered (the empty-blob one dropped), each once.
	if len(cfgKeys) != 3 {
		t.Fatalf("expected 3 cohort config blobs, got %d: %v", len(cfgKeys), cfgKeys)
	}
	if cfgKeys["roverfox.s.renderlab_cfg_mac_a_aaa"] != "blobA" {
		t.Fatalf("cohort blob mismatch: %v", cfgKeys)
	}
	// Each context pinned to exactly one cohort selector.
	for _, ucid := range []string{"7", "8", "9"} {
		name := "roverfox.s.renderlab_cohort_" + ucid
		val, ok := cohortPins[name]
		if !ok {
			t.Fatalf("context %s was not pinned to a cohort", ucid)
		}
		if _, isCfg := cfgKeys["roverfox.s.renderlab_cfg_"+val]; !isCfg {
			t.Fatalf("context %s pinned to unknown cohort key %q", ucid, val)
		}
	}
	// The windows agent must be pinned to the windows cohort.
	if cohortPins["roverfox.s.renderlab_cohort_9"] != "win_a_ccc" {
		t.Fatalf("windows agent pinned to non-windows cohort: %q", cohortPins["roverfox.s.renderlab_cohort_9"])
	}
}

func TestApplyRenderCohortNoopWhenProviderUnavailable(t *testing.T) {
	fake := testutil.NewFakeJugglerTransport(t)
	fake.RespondJSON("Browser.setContextFingerprint", map[string]any{})
	client := juggler.NewClient(fake)
	defer client.Close()

	extensions.Registry.SetRenderCohort(&fakeRenderCohortProvider{avail: false})
	t.Cleanup(func() { extensions.Registry.SetRenderCohort(nil) })

	o := &Orchestrator{Client: client}
	o.applyRenderCohort(1, "agent", "MacIntel")

	if got := len(fake.CallsByMethod("Browser.setContextFingerprint")); got != 0 {
		t.Fatalf("expected no delivery when provider unavailable, got %d calls", got)
	}
}
