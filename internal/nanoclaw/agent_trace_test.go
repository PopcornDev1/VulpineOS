package nanoclaw

import "testing"

func TestSummarizeToolCall(t *testing.T) {
	got := summarizeToolCall("browser", map[string]interface{}{
		"action": "open",
		"url":    "https://example.com",
	})
	want := "Running browser open https://example.com"
	if got != want {
		t.Fatalf("summarizeToolCall = %q, want %q", got, want)
	}
}

func TestSummarizeToolCallExec(t *testing.T) {
	got := summarizeToolCall("exec", map[string]interface{}{
		"command": "vulpineos-browser navigate \"https://example.com\"",
	})
	want := "Running tool: exec vulpineos-browser navigate \"https://example.com\""
	if got != want {
		t.Fatalf("summarizeToolCall = %q, want %q", got, want)
	}
}

func TestSummarizeToolCallRedactsSensitiveBrowserType(t *testing.T) {
	got := summarizeToolCall("browser", map[string]interface{}{
		"action":   "type",
		"selector": "input[type=password]",
		"text":     "correct-horse",
	})
	want := "Running browser type [redacted]"
	if got != want {
		t.Fatalf("summarizeToolCall = %q, want %q", got, want)
	}
}

func TestSummarizeToolCallRedactsTokenizedURL(t *testing.T) {
	got := summarizeToolCall("browser", map[string]interface{}{
		"action": "open",
		"url":    "https://example.com/login?token=remote-token&view=agents",
	})
	want := "Running browser open https://example.com/login?token=[redacted]&view=agents"
	if got != want {
		t.Fatalf("summarizeToolCall = %q, want %q", got, want)
	}
}

func TestSummarizeToolResultError(t *testing.T) {
	got := summarizeToolResult("browser", toolCallInfo{}, false, nil, map[string]interface{}{
		"status": "error",
		"error":  "gateway token mismatch",
	})
	want := "Tool failed: browser — gateway token mismatch"
	if got != want {
		t.Fatalf("summarizeToolResult = %q, want %q", got, want)
	}
}

func TestSummarizeToolResultRedactsSensitiveError(t *testing.T) {
	got := summarizeToolResult("browser", toolCallInfo{}, false, nil, map[string]interface{}{
		"status": "error",
		"error":  "request failed with Authorization: Bearer browser-token at https://example.com/?token=query-token payload {\"token\":\"json-token\"} cookie=session-token",
	})
	want := "Tool failed: browser — request failed with Authorization: Bearer [redacted] at https://example.com/?token=[redacted] payload {\"token\":\"[redacted]\"} cookie=[redacted]"
	if got != want {
		t.Fatalf("summarizeToolResult = %q, want %q", got, want)
	}
}

func TestSummarizeToolResultSuccessFromJSONText(t *testing.T) {
	content := []struct {
		Type      string                 `json:"type"`
		Text      string                 `json:"text"`
		Thinking  string                 `json:"thinking"`
		ID        string                 `json:"id"`
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}{
		{Type: "text", Text: `{"status":"completed"}`},
	}
	got := summarizeToolResult("web_fetch", toolCallInfo{}, false, content, nil)
	want := "Tool completed: web_fetch"
	if got != want {
		t.Fatalf("summarizeToolResult = %q, want %q", got, want)
	}
}

func TestSummarizeToolResultBrowserOpenSuccess(t *testing.T) {
	content := []struct {
		Type      string                 `json:"type"`
		Text      string                 `json:"text"`
		Thinking  string                 `json:"thinking"`
		ID        string                 `json:"id"`
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}{
		{Type: "text", Text: `{"status":"ok","targetId":"abc123","url":"https://pbtech.co.nz"}`},
	}
	got := summarizeToolResult("browser", toolCallInfo{}, false, content, nil)
	want := "Tool completed: browser — opened https://pbtech.co.nz"
	if got != want {
		t.Fatalf("summarizeToolResult = %q, want %q", got, want)
	}
}

func TestSummarizeToolResultTreatsNonZeroExitCodeAsError(t *testing.T) {
	content := []struct {
		Type      string                 `json:"type"`
		Text      string                 `json:"text"`
		Thinking  string                 `json:"thinking"`
		ID        string                 `json:"id"`
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}{
		{Type: "text", Text: "node: command not found"},
	}
	got := summarizeToolResult("exec", toolCallInfo{}, false, content, map[string]interface{}{
		"status":     "completed",
		"exitCode":   1,
		"aggregated": "node: command not found",
	})
	want := "Tool failed: exec — node: command not found"
	if got != want {
		t.Fatalf("summarizeToolResult = %q, want %q", got, want)
	}
}

func TestSummarizeToolResultContentFailureOverridesWrapperCompleted(t *testing.T) {
	content := []struct {
		Type      string                 `json:"type"`
		Text      string                 `json:"text"`
		Thinking  string                 `json:"thinking"`
		ID        string                 `json:"id"`
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}{
		{Type: "text", Text: `{"status":"error","error":"gateway token mismatch"}`},
	}
	got := summarizeToolResult("browser", toolCallInfo{}, false, content, map[string]interface{}{
		"status": "completed",
	})
	want := "Tool failed: browser — gateway token mismatch"
	if got != want {
		t.Fatalf("summarizeToolResult = %q, want %q", got, want)
	}
}

func TestSummarizeToolResultContentExitCodeOverridesWrapperCompleted(t *testing.T) {
	content := []struct {
		Type      string                 `json:"type"`
		Text      string                 `json:"text"`
		Thinking  string                 `json:"thinking"`
		ID        string                 `json:"id"`
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}{
		{Type: "text", Text: `{"exitCode":2,"stderr":"permission denied"}`},
	}
	got := summarizeToolResult("exec", toolCallInfo{}, false, content, map[string]interface{}{
		"status": "completed",
	})
	want := "Tool failed: exec — permission denied"
	if got != want {
		t.Fatalf("summarizeToolResult = %q, want %q", got, want)
	}
}

func TestSummarizeToolResultIncomplete(t *testing.T) {
	got := summarizeToolResult("browser", toolCallInfo{
		Name: "browser",
		Arguments: map[string]interface{}{
			"action":   "click",
			"selector": "button.buy",
		},
	}, true, nil, map[string]interface{}{
		"status": "incomplete",
		"error":  "target became detached before click completed",
	})
	want := "Tool incomplete: browser click button.buy — target became detached before click completed"
	if got != want {
		t.Fatalf("summarizeToolResult = %q, want %q", got, want)
	}
}

func TestSummarizeToolResultTimeout(t *testing.T) {
	got := summarizeToolResult("browser", toolCallInfo{
		Name: "browser",
		Arguments: map[string]interface{}{
			"action": "open",
			"url":    "https://example.com",
		},
	}, true, nil, map[string]interface{}{
		"status": "timeout",
		"error":  "navigation exceeded 30s timeout",
	})
	want := "Tool timed out: browser open https://example.com — navigation exceeded 30s timeout"
	if got != want {
		t.Fatalf("summarizeToolResult = %q, want %q", got, want)
	}
}

func TestSummarizeToolResultSuccessUsesOutputSnippet(t *testing.T) {
	got := summarizeToolResult("exec", toolCallInfo{
		Name: "exec",
		Arguments: map[string]interface{}{
			"command": "curl https://example.com",
		},
	}, true, nil, map[string]interface{}{
		"status":     "completed",
		"aggregated": "HTTP/2 200",
	})
	want := "Tool completed: exec curl https://example.com — HTTP/2 200"
	if got != want {
		t.Fatalf("summarizeToolResult = %q, want %q", got, want)
	}
}

func TestHandleSessionLogLine_EmitsAssistantText(t *testing.T) {
	agent := newAgent("agent-1", "ctx-1", make(chan AgentStatus, 1))

	line := `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"STATUS:clicked"}]}}`
	agent.handleSessionLogLine(line)

	select {
	case msg := <-agent.conversationCh:
		if msg.Role != "assistant" {
			t.Fatalf("role = %q, want assistant", msg.Role)
		}
		if msg.Content != "STATUS:clicked" {
			t.Fatalf("content = %q, want STATUS:clicked", msg.Content)
		}
	default:
		t.Fatal("expected assistant text to be emitted from session log")
	}
}

func TestHandleSessionLogLine_EmitsThinkingIntoTrace(t *testing.T) {
	agent := newAgent("agent-1", "ctx-1", make(chan AgentStatus, 1))

	line := `{"type":"message","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Inspecting the loaded page state"},{"type":"text","text":"Done"}]}}`
	agent.handleSessionLogLine(line)

	msg1 := <-agent.conversationCh
	if msg1.Role != "system" || msg1.Content != "Thinking: Inspecting the loaded page state" {
		t.Fatalf("first message = %#v", msg1)
	}

	msg2 := <-agent.conversationCh
	if msg2.Role != "assistant" || msg2.Content != "Done" {
		t.Fatalf("second message = %#v", msg2)
	}
}

func TestHandleSessionLogLine_WarnsAfterFailedToolThenAssistantReply(t *testing.T) {
	agent := newAgent("agent-1", "ctx-1", make(chan AgentStatus, 1))

	toolResult := `{"type":"message","message":{"role":"toolResult","toolName":"browser","content":[{"type":"text","text":"{\"status\":\"error\",\"error\":\"gateway token mismatch\"}"}],"details":{"status":"error","error":"gateway token mismatch"}}}`
	assistant := `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"STATUS:clicked"}]}}`

	agent.handleSessionLogLine(toolResult)
	agent.handleSessionLogLine(assistant)

	msg1 := <-agent.conversationCh
	if msg1.Role != "system" || msg1.Content != "Tool failed: browser — gateway token mismatch" {
		t.Fatalf("first message = %#v", msg1)
	}

	msg2 := <-agent.conversationCh
	if msg2.Role != "system" || msg2.Content != "Warning: assistant replied after failed browser action — gateway token mismatch" {
		t.Fatalf("second message = %#v", msg2)
	}

	msg3 := <-agent.conversationCh
	if msg3.Role != "assistant" || msg3.Content != "STATUS:clicked" {
		t.Fatalf("third message = %#v", msg3)
	}
}

func TestHandleSessionLogLine_SuccessfulToolClearsFailureWarning(t *testing.T) {
	agent := newAgent("agent-1", "ctx-1", make(chan AgentStatus, 1))

	failed := `{"type":"message","message":{"role":"toolResult","toolName":"browser","content":[{"type":"text","text":"{\"status\":\"error\",\"error\":\"gateway token mismatch\"}"}],"details":{"status":"error","error":"gateway token mismatch"}}}`
	success := `{"type":"message","message":{"role":"toolResult","toolName":"browser","content":[{"type":"text","text":"{\"status\":\"ok\",\"url\":\"https://example.com\"}"}],"details":{"status":"ok","url":"https://example.com"}}}`
	assistant := `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"Done"}]}}`

	agent.handleSessionLogLine(failed)
	agent.handleSessionLogLine(success)
	agent.handleSessionLogLine(assistant)

	msg1 := <-agent.conversationCh
	if msg1.Content != "Tool failed: browser — gateway token mismatch" {
		t.Fatalf("first message = %#v", msg1)
	}

	msg2 := <-agent.conversationCh
	if msg2.Content != "Tool completed: browser — opened https://example.com" {
		t.Fatalf("second message = %#v", msg2)
	}

	msg3 := <-agent.conversationCh
	if msg3.Role != "assistant" || msg3.Content != "Done" {
		t.Fatalf("third message = %#v", msg3)
	}

	select {
	case extra := <-agent.conversationCh:
		t.Fatalf("unexpected extra warning after successful tool: %#v", extra)
	default:
	}
}

func TestHandleSessionLogLine_TreatsNonZeroExitCodeAsFailure(t *testing.T) {
	agent := newAgent("agent-1", "ctx-1", make(chan AgentStatus, 1))

	toolResult := `{"type":"message","message":{"role":"toolResult","toolName":"exec","isError":false,"content":[{"type":"text","text":"node: command not found"}],"details":{"status":"completed","exitCode":1,"aggregated":"node: command not found"}}}`
	assistant := `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"Done"}]}}`

	agent.handleSessionLogLine(toolResult)
	agent.handleSessionLogLine(assistant)

	msg1 := <-agent.conversationCh
	if msg1.Role != "system" || msg1.Content != "Tool failed: exec — node: command not found" {
		t.Fatalf("first message = %#v", msg1)
	}

	msg2 := <-agent.conversationCh
	if msg2.Role != "system" || msg2.Content != "Warning: assistant replied after failed exec action — node: command not found" {
		t.Fatalf("second message = %#v", msg2)
	}

	msg3 := <-agent.conversationCh
	if msg3.Role != "assistant" || msg3.Content != "Done" {
		t.Fatalf("third message = %#v", msg3)
	}
}

func TestHandleSessionLogLine_UsesToolCallContextInResultSummary(t *testing.T) {
	agent := newAgent("agent-1", "ctx-1", make(chan AgentStatus, 1))

	call := `{"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"browser","arguments":{"action":"click","selector":"button.buy"}}]}}`
	result := `{"type":"message","message":{"role":"toolResult","toolName":"browser","toolCallId":"call-1","content":[{"type":"text","text":"{\"status\":\"completed\"}"}],"details":{"status":"completed"}}}`

	agent.handleSessionLogLine(call)
	agent.handleSessionLogLine(result)

	msg1 := <-agent.conversationCh
	if msg1.Content != "Running browser click button.buy" {
		t.Fatalf("first message = %#v", msg1)
	}

	msg2 := <-agent.conversationCh
	if msg2.Content != "Tool completed: browser click button.buy" {
		t.Fatalf("second message = %#v", msg2)
	}
}

func TestHandleSessionLogLine_UsesTimeoutClassification(t *testing.T) {
	agent := newAgent("agent-1", "ctx-1", make(chan AgentStatus, 1))

	call := `{"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","id":"call-1","name":"browser","arguments":{"action":"open","url":"https://example.com"}}]}}`
	result := `{"type":"message","message":{"role":"toolResult","toolName":"browser","toolCallId":"call-1","content":[{"type":"text","text":"{\"status\":\"timeout\",\"error\":\"navigation exceeded 30s timeout\"}"}],"details":{"status":"timeout","error":"navigation exceeded 30s timeout"}}}`

	agent.handleSessionLogLine(call)
	agent.handleSessionLogLine(result)

	msg1 := <-agent.conversationCh
	if msg1.Content != "Running browser open https://example.com" {
		t.Fatalf("first message = %#v", msg1)
	}

	msg2 := <-agent.conversationCh
	if msg2.Content != "Tool timed out: browser open https://example.com — navigation exceeded 30s timeout" {
		t.Fatalf("second message = %#v", msg2)
	}
}

func TestHandleSessionLogLine_WarnsAfterIncompleteToolThenAssistantReply(t *testing.T) {
	agent := newAgent("agent-1", "ctx-1", make(chan AgentStatus, 1))

	toolResult := `{"type":"message","message":{"role":"toolResult","toolName":"browser","content":[{"type":"text","text":"{\"status\":\"incomplete\",\"error\":\"target became detached before click completed\"}"}],"details":{"status":"incomplete","error":"target became detached before click completed"}}}`
	assistant := `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"Done"}]}}`

	agent.handleSessionLogLine(toolResult)
	agent.handleSessionLogLine(assistant)

	msg1 := <-agent.conversationCh
	if msg1.Role != "system" || msg1.Content != "Tool incomplete: browser — target became detached before click completed" {
		t.Fatalf("first message = %#v", msg1)
	}

	msg2 := <-agent.conversationCh
	if msg2.Role != "system" || msg2.Content != "Warning: assistant replied after incomplete browser action — target became detached before click completed" {
		t.Fatalf("second message = %#v", msg2)
	}

	msg3 := <-agent.conversationCh
	if msg3.Role != "assistant" || msg3.Content != "Done" {
		t.Fatalf("third message = %#v", msg3)
	}
}
