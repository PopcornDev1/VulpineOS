package runtimeaudit

import (
	"strings"
	"testing"
	"time"

	"vulpineos/internal/vault"
)

func TestManagerLogAndSubscribe(t *testing.T) {
	db, err := vault.OpenPath(t.TempDir() + "/vault.db")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	manager := New(db)
	t.Cleanup(manager.Close)

	sub := manager.Subscribe()
	_, err = manager.Log("kernel", "error", "crashed", "kernel exited", map[string]string{"pid": "44"})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	select {
	case event := <-sub:
		if event.Component != "kernel" || event.Event != "crashed" {
			t.Fatalf("unexpected event: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runtime event")
	}

	events, err := manager.List(vault.RuntimeEventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}

	settings, err := manager.SetRetention(64)
	if err != nil {
		t.Fatalf("SetRetention: %v", err)
	}
	if settings.Retention != 64 {
		t.Fatalf("retention = %d, want 64", settings.Retention)
	}
}

func TestManagerLogRedactsSensitiveMetadata(t *testing.T) {
	db, err := vault.OpenPath(t.TempDir() + "/vault.db")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	manager := New(db)
	t.Cleanup(manager.Close)

	event, err := manager.Log("gateway", "info", "started", "gateway started: http://127.0.0.1:8443/?token=message-token", map[string]string{
		"gateway_token": "token-123",
		"remote_url":    "http://127.0.0.1:8443/?token=token-123&view=agents",
		"header":        "Authorization: Bearer token-123",
		"pid":           "44",
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if event.Metadata["gateway_token"] != "[redacted]" {
		t.Fatalf("gateway_token = %q, want redacted", event.Metadata["gateway_token"])
	}
	if strings.Contains(event.Metadata["remote_url"], "token-123") {
		t.Fatalf("remote_url leaked token: %q", event.Metadata["remote_url"])
	}
	if strings.Contains(event.Metadata["header"], "token-123") || !strings.Contains(event.Metadata["header"], "Bearer [redacted]") {
		t.Fatalf("header was not redacted: %q", event.Metadata["header"])
	}
	if event.Metadata["pid"] != "44" {
		t.Fatalf("pid = %q, want unchanged", event.Metadata["pid"])
	}
	if strings.Contains(event.Message, "message-token") || !strings.Contains(event.Message, "token=[redacted]") {
		t.Fatalf("message was not redacted: %q", event.Message)
	}

	events, err := manager.List(vault.RuntimeEventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}
	for key, value := range events[0].Metadata {
		if strings.Contains(value, "token-123") {
			t.Fatalf("stored metadata %s leaked token: %q", key, value)
		}
	}
	if strings.Contains(events[0].Message, "message-token") {
		t.Fatalf("stored message leaked token: %q", events[0].Message)
	}
}

func TestManagerLogRedactsInlineSecretFragments(t *testing.T) {
	db, err := vault.OpenPath(t.TempDir() + "/vault.db")
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	manager := New(db)
	t.Cleanup(manager.Close)

	event, err := manager.Log(
		"gateway",
		"error",
		"failed",
		`request failed with token=plain-token cookie=session-cookie payload {"token":"json-token"} but gateway token mismatch remained visible`,
		map[string]string{
			"detail":    `secret=metadata-secret payload {"password":"json-password"}`,
			"sessionId": "vulpine-agent-1",
		},
	)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	for _, leaked := range []string{"plain-token", "session-cookie", "json-token", "metadata-secret", "json-password"} {
		if strings.Contains(event.Message, leaked) {
			t.Fatalf("message leaked %q: %q", leaked, event.Message)
		}
		if strings.Contains(event.Metadata["detail"], leaked) {
			t.Fatalf("metadata leaked %q: %q", leaked, event.Metadata["detail"])
		}
	}
	if !strings.Contains(event.Message, "gateway token mismatch") {
		t.Fatalf("non-secret diagnostic was over-redacted: %q", event.Message)
	}
	if event.Metadata["sessionId"] != "vulpine-agent-1" {
		t.Fatalf("sessionId = %q, want unchanged", event.Metadata["sessionId"])
	}
}
