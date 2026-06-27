package agentcore

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLive_NativeAgent_ReliabilityGauntlet(t *testing.T) {
	if os.Getenv("VULPINE_AGENTCORE_GAUNTLET") == "" {
		t.Skip("set VULPINE_AGENTCORE_GAUNTLET=1 with VULPINEOS_RUN_LIVE=1 and VULPINE_AGENTCORE_LIVE=1 to run unattended local-fixture gauntlet")
	}
	if os.Getenv("VULPINE_AGENTCORE_BROWSER") == "" {
		t.Skip("set VULPINE_AGENTCORE_BROWSER=1 to run the browser-action gauntlet")
	}

	server := httptest.NewServer(reliabilityFixtureHandler())
	defer server.Close()

	k, client := startLiveKernel(t)
	defer k.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	contextID, sessionID, err := openPage(ctx, client)
	if err != nil {
		t.Fatalf("openPage: %v", err)
	}
	defer cleanupContext(client, contextID)

	toolset := NewBrowserToolset(client, contextID, sessionID)
	defer toolset.Close()
	cfg := liveConfig(t)
	cfg.MaxIterations = 18

	for _, scenario := range DefaultReliabilityGauntlet() {
		t.Run(scenario.Name, func(t *testing.T) {
			task := fmt.Sprintf("%s Open this exact URL: %s%s. Finish with exactly %s and no extra prose.", scenario.Task, server.URL, scenario.Fixture, scenario.ExpectedAnswer)
			final, err := RunBrowserAgentWithToolset(ctx, toolset, cfg, task, liveLogEvents{t})
			if err != nil {
				t.Fatalf("RunBrowserAgentWithToolset: %v", err)
			}
			if !strings.Contains(final, scenario.ExpectedAnswer) {
				t.Fatalf("final = %q, want %q", final, scenario.ExpectedAnswer)
			}
		})
	}
}

func reliabilityFixtureHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/dynamic-dashboard", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><title>Dynamic Dashboard</title><main><h1>Dashboard</h1><div id="status">READY_42</div><script>setInterval(()=>{document.body.dataset.tick=String(Date.now())}, 100)</script></main>`)
	})
	mux.HandleFunc("/shadow-form", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><title>Shadow Form</title><h1>Signup Fixture</h1><shadow-signup></shadow-signup><script>
customElements.define('shadow-signup', class extends HTMLElement {
	connectedCallback() {
		const root = this.attachShadow({mode:'open'});
		root.innerHTML = '<form><label>Email <input name="email" autocomplete="email" placeholder="you@example.com" required></label><button type="button" id="save">Save</button><output id="result"></output></form>';
		root.getElementById('save').addEventListener('click', () => root.getElementById('result').textContent = root.querySelector('input').value === 'alice@example.com' ? 'FORM_OK' : 'FORM_BAD');
	}
});
</script>`)
	})
	mux.HandleFunc("/modal-overlay", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><title>Modal Fixture</title><div role="dialog" aria-modal="true" id="modal"><p>Notice</p><button id="close">Close</button></div><main><button id="main" disabled>Main action</button><output id="out"></output></main><script>
document.getElementById('close').onclick = () => { document.getElementById('modal').remove(); const btn = document.getElementById('main'); btn.disabled = false; btn.textContent = 'MODAL_OK'; document.getElementById('out').textContent = 'MODAL_OK'; };
</script>`)
	})
	mux.HandleFunc("/virtual-list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><title>Virtual List</title><main style="height:4200px">`)
		for i := 1; i <= 100; i++ {
			fmt.Fprintf(w, `<div style="margin-top:35px" id="item-%d">Item %d%s</div>`, i, i, map[bool]string{true: " ITEM_80_OK", false: ""}[i == 80])
		}
		fmt.Fprint(w, `</main>`)
	})
	mux.HandleFunc("/mock-challenge", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><title>Mock Challenge</title><main><h1>Manual review fixture</h1><p data-challenge="mock">This controlled local page represents a user-action gate.</p><strong>NEEDS_USER_ACTION</strong></main>`)
	})
	return mux
}
