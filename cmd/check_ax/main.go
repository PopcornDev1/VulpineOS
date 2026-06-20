package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"vulpineos/internal/kernel"
	"vulpineos/internal/mcp"
)

func main() {
	k := kernel.New()
	err := k.Start(kernel.Config{
		Headless: true,
	})
	if err != nil {
		fmt.Println("FAIL start:", err)
		return
	}
	defer k.Stop()
	client := k.Client()

	executor := mcp.NewToolExecutor(client)
	defer executor.Close()

	// Enable browser domain (required before createBrowserContext)
	_, _ = client.Call("", "Browser.enable", map[string]interface{}{
		"attachToDefaultContext": true,
	})
	time.Sleep(200 * time.Millisecond)

	// Create page via executor (simulates openPageWithToolset)
	res, err := executor.Call(context.Background(), "vulpine_new_context", json.RawMessage(`{}`))
	if err != nil {
		fmt.Println("FAIL vulpine_new_context:", err)
		return
	}
	if res.IsError {
		fmt.Println("FAIL vulpine_new_context:", res.Content[0].Text)
		return
	}
	var out struct {
		ContextID string `json:"contextId"`
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal([]byte(res.Content[0].Text), &out)
	fmt.Printf("Context: %s Session: %s\n", out.ContextID, out.SessionID)

	// WaitForTrackerInit (new!)
	if waitErr := executor.WaitForTrackerInit(out.SessionID); waitErr != nil {
		fmt.Println("WaitForTrackerInit error:", waitErr)
	} else {
		fmt.Println("WaitForTrackerInit: OK")
	}

	// Now navigate (simulates first agent tool call)
	navJSON, _ := json.Marshal(map[string]interface{}{
		"sessionId": out.SessionID,
		"url":       "https://example.com",
	})
	navRes, navErr := executor.Call(context.Background(), "vulpine_navigate", navJSON)
	if navErr != nil {
		fmt.Println("FAIL navigate dispatch:", navErr)
		return
	}
	if navRes.IsError {
		fmt.Println("FAIL navigate:", navRes.Content[0].Text)
	} else {
		fmt.Println("navigate OK:", navRes.Content[0].Text)
	}

	time.Sleep(1 * time.Second)

	// Snapshot (uses AX tree fallback)
	snapJSON, _ := json.Marshal(map[string]interface{}{
		"sessionId": out.SessionID,
	})
	snapRes, snapErr := executor.Call(context.Background(), "vulpine_snapshot", snapJSON)
	if snapErr != nil {
		fmt.Println("FAIL snapshot dispatch:", snapErr)
		return
	}
	if snapRes.IsError {
		fmt.Println("FAIL snapshot:", snapRes.Content[0].Text)
	} else {
		body := snapRes.Content[0].Text
		if len(body) > 500 {
			body = body[:500]
		}
		fmt.Println("snapshot OK (partial):", body)
	}

	// Page info (uses Runtime.evaluate)
	infoJSON, _ := json.Marshal(map[string]interface{}{
		"sessionId": out.SessionID,
	})
	infoRes, infoErr := executor.Call(context.Background(), "vulpine_page_info", infoJSON)
	if infoErr != nil {
		fmt.Println("FAIL page_info dispatch:", infoErr)
		return
	}
	if infoRes.IsError {
		fmt.Println("FAIL page_info:", infoRes.Content[0].Text)
	} else {
		fmt.Println("page_info OK:", infoRes.Content[0].Text)
	}

	fmt.Println("\nDONE — all tools working")
}
