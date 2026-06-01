package agentcore

import "testing"

func TestBrowserToolsStripSessionIDAndCurate(t *testing.T) {
	tools := BrowserTools()
	if len(tools) == 0 {
		t.Fatal("expected browser tools")
	}

	byName := map[string]ToolDef{}
	for _, td := range tools {
		if td.Type != "function" {
			t.Errorf("tool %s type = %q, want function", td.Function.Name, td.Type)
		}
		byName[td.Function.Name] = td

		// sessionId must never be exposed to the model.
		props, _ := td.Function.Parameters["properties"].(map[string]interface{})
		if _, ok := props[sessionIDArg]; ok {
			t.Errorf("tool %s still exposes %s in properties", td.Function.Name, sessionIDArg)
		}
		if req, ok := td.Function.Parameters["required"].([]string); ok {
			for _, r := range req {
				if r == sessionIDArg {
					t.Errorf("tool %s still requires %s", td.Function.Name, sessionIDArg)
				}
			}
		}
	}

	// A representative core tool must be present and keep its non-session args.
	nav, ok := byName["vulpine_navigate"]
	if !ok {
		t.Fatal("vulpine_navigate missing from exposed tools")
	}
	props := nav.Function.Parameters["properties"].(map[string]interface{})
	if _, ok := props["url"]; !ok {
		t.Error("vulpine_navigate should still expose url")
	}
	req, _ := nav.Function.Parameters["required"].([]string)
	foundURL := false
	for _, r := range req {
		if r == "url" {
			foundURL = true
		}
	}
	if !foundURL {
		t.Error("vulpine_navigate should still require url")
	}

	// Lifecycle/image/extension tools must be excluded.
	for _, excluded := range []string{"vulpine_new_context", "vulpine_close_context", "vulpine_screenshot", "vulpine_annotated_screenshot"} {
		if _, ok := byName[excluded]; ok {
			t.Errorf("tool %s should be excluded from the native toolset", excluded)
		}
	}

	if !IsBrowserTool("vulpine_navigate") || IsBrowserTool("vulpine_new_context") {
		t.Error("IsBrowserTool allow-list mismatch")
	}
}
