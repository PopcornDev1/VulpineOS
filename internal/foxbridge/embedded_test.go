package foxbridge

import (
	"fmt"
	"testing"
)

func TestStartEmbeddedNilClient(t *testing.T) {
	_, err := StartEmbedded(nil, 0)
	if err == nil {
		t.Fatal("expected error for nil client")
	}
	if err.Error() != "juggler client is nil" {
		t.Errorf("error = %q, want %q", err.Error(), "juggler client is nil")
	}
}

func TestCDPURL(t *testing.T) {
	es := &EmbeddedServer{port: 9222}
	url := es.CDPURL()
	expected := "ws://127.0.0.1:9222"
	if url != expected {
		t.Errorf("CDPURL() = %q, want %q", url, expected)
	}
}

func TestCDPURLCustomPort(t *testing.T) {
	es := &EmbeddedServer{port: 12345}
	url := es.CDPURL()
	expected := "ws://127.0.0.1:12345"
	if url != expected {
		t.Errorf("CDPURL() = %q, want %q", url, expected)
	}
}

func TestPort(t *testing.T) {
	es := &EmbeddedServer{port: 8080}
	if es.Port() != 8080 {
		t.Errorf("Port() = %d, want 8080", es.Port())
	}
}

func TestStopDoesNotPanic(t *testing.T) {
	es := &EmbeddedServer{port: 9222, done: make(chan struct{})}
	// Should not panic even without a running server
	es.Stop()
}

func TestCDPURLFormat(t *testing.T) {
	ports := []int{9222, 0, 1, 65535}
	for _, port := range ports {
		es := &EmbeddedServer{port: port}
		expected := fmt.Sprintf("ws://127.0.0.1:%d", port)
		if es.CDPURL() != expected {
			t.Errorf("CDPURL() with port %d = %q, want %q", port, es.CDPURL(), expected)
		}
	}
}

func TestJugglerAdapterClose(t *testing.T) {
	// jugglerAdapter.Close() should be a no-op (not close the underlying client)
	a := &jugglerAdapter{client: nil}
	if err := a.Close(); err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}
