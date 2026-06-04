package juggler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClientCallSendsSessionMethodAndTypedParams(t *testing.T) {
	mt := newMemTransport()

	captured := make(chan *Message, 1)
	go func() {
		for {
			select {
			case <-mt.closed:
				return
			case req := <-mt.outgoing:
				captured <- req
				result, _ := json.Marshal(map[string]string{"echo": req.Method})
				mt.incoming <- &Message{
					ID:     req.ID,
					Result: result,
				}
			}
		}
	}()

	client := NewClient(mt)
	defer client.Close()

	type callParams struct {
		IncludeUserAgent bool `json:"includeUserAgent"`
	}

	result, err := client.Call("session-alpha", "Browser.getInfo", callParams{IncludeUserAgent: true})
	if err != nil {
		t.Fatalf("Call failed: %v", err)
	}

	select {
	case req := <-captured:
		if req.SessionID != "session-alpha" {
			t.Fatalf("SessionID = %q, want %q", req.SessionID, "session-alpha")
		}
		if req.Method != "Browser.getInfo" {
			t.Fatalf("Method = %q, want %q", req.Method, "Browser.getInfo")
		}
		var params callParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if !params.IncludeUserAgent {
			t.Fatal("expected IncludeUserAgent=true in params")
		}
	default:
		t.Fatal("no outgoing message captured")
	}

	var parsed map[string]string
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if parsed["echo"] != "Browser.getInfo" {
		t.Fatalf("method = %q, want %q", parsed["echo"], "Browser.getInfo")
	}
}

func TestClientCallReturnsProtocolError(t *testing.T) {
	mt := newMemTransport()
	go func() {
		for {
			select {
			case <-mt.closed:
				return
			case req := <-mt.outgoing:
				mt.incoming <- &Message{
					ID:    req.ID,
					Error: &Error{Message: "no such browser instance"},
				}
				return
			}
		}
	}()

	client := NewClient(mt)
	defer client.Close()

	_, err := client.Call("", "Browser.getInfo", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no such browser instance") {
		t.Fatalf("error = %q, want to contain %q", err.Error(), "no such browser instance")
	}
}
