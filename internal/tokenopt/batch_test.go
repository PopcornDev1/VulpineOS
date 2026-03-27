package tokenopt

import (
	"encoding/json"
	"testing"
)

func TestBatchResultJSON(t *testing.T) {
	r := &BatchResult{
		Results: []BatchStepResult{
			{Tool: "vulpine_navigate", Success: true, Data: "Navigated to https://example.com"},
			{Tool: "vulpine_click", Success: true, Data: "Clicked at (100, 200)"},
		},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	var decoded BatchResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(decoded.Results))
	}
	if !decoded.Results[0].Success {
		t.Fatal("first result should be success")
	}
	if decoded.Results[0].Tool != "vulpine_navigate" {
		t.Fatalf("expected vulpine_navigate, got %s", decoded.Results[0].Tool)
	}
}

func TestBatchActionJSON(t *testing.T) {
	actions := []BatchAction{
		{
			Tool:   "vulpine_navigate",
			Params: map[string]interface{}{"url": "https://example.com"},
		},
		{
			Tool:   "vulpine_click",
			Params: map[string]interface{}{"x": 100.0, "y": 200.0},
		},
		{
			Tool:   "vulpine_type",
			Params: map[string]interface{}{"text": "hello world"},
		},
	}

	data, err := json.Marshal(actions)
	if err != nil {
		t.Fatal(err)
	}

	var decoded []BatchAction
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(decoded))
	}
	if decoded[0].Tool != "vulpine_navigate" {
		t.Fatalf("expected vulpine_navigate, got %s", decoded[0].Tool)
	}
	url, _ := decoded[0].Params["url"].(string)
	if url != "https://example.com" {
		t.Fatalf("expected url, got %s", url)
	}
}

func TestBatchResultWithErrors(t *testing.T) {
	r := &BatchResult{
		Results: []BatchStepResult{
			{Tool: "vulpine_navigate", Success: true, Data: "ok"},
			{Tool: "vulpine_click", Success: false, Data: "element not found"},
		},
		Errors: []string{"vulpine_click: element not found"},
	}

	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	var decoded BatchResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(decoded.Errors))
	}
}

func TestBatchStepResultOmitEmpty(t *testing.T) {
	r := BatchStepResult{Tool: "vulpine_click", Success: true}
	data, _ := json.Marshal(r)
	str := string(data)
	// Data should be omitted when empty
	if str != `{"tool":"vulpine_click","success":true}` {
		t.Fatalf("unexpected JSON: %s", str)
	}
}

func TestNewBatchExecutor(t *testing.T) {
	// nil client — just testing construction doesn't panic
	exec := NewBatchExecutor(nil)
	if exec == nil {
		t.Fatal("expected non-nil executor")
	}
}

func TestBatchExecuteUnknownTool(t *testing.T) {
	exec := NewBatchExecutor(nil)
	result := exec.executeOne("session1", BatchAction{
		Tool:   "vulpine_unknown",
		Params: map[string]interface{}{},
	})
	if result.Success {
		t.Fatal("unknown tool should fail")
	}
	if result.Data != "unknown tool: vulpine_unknown" {
		t.Fatalf("unexpected error: %s", result.Data)
	}
}
