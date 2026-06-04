package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"vulpineos/internal/kernel"
)

func main() {
	k := kernel.New()
	err := k.Start(kernel.Config{
		BinaryPath: "/Users/rowan/.vulpineos/browser/Camoufox.app/Contents/MacOS/camoufox",
		Headless:   true,
	})
	if err != nil {
		fmt.Println("FAIL start:", err)
		return
	}
	defer k.Stop()
	client := k.Client()

	_, err = client.Call("", "Browser.enable", map[string]interface{}{
		"attachToDefaultContext": true,
	})
	if err != nil {
		fmt.Println("FAIL Browser.enable:", err)
		return
	}

	result, err := client.Call("", "Browser.createBrowserContext", map[string]interface{}{
		"removeOnDetach": true,
	})
	if err != nil {
		fmt.Println("FAIL createBrowserContext:", err)
		return
	}
	var ctx struct {
		BrowserContextID string `json:"browserContextId"`
	}
	json.Unmarshal(result, &ctx)
	fmt.Println("BrowserContextID:", ctx.BrowserContextID)

	sessionCh := make(chan string, 4)
	cancel := client.SubscribeWithCancel("Browser.attachedToTarget", func(_ string, params json.RawMessage) {
		var ev struct {
			SessionID string `json:"sessionId"`
		}
		json.Unmarshal(params, &ev)
		if ev.SessionID != "" {
			sessionCh <- ev.SessionID
		}
	})
	defer cancel()

	_, err = client.Call("", "Browser.newPage", map[string]interface{}{
		"browserContextId": ctx.BrowserContextID,
	})
	if err != nil {
		fmt.Println("FAIL newPage:", err)
		return
	}

	var sessionID string
	select {
	case sessionID = <-sessionCh:
	case <-time.After(10 * time.Second):
		fmt.Println("FAIL timeout waiting for session")
		return
	}
	fmt.Println("SessionID:", sessionID)
	time.Sleep(1 * time.Second)

	ctxT, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()

	// Try getOptimizedDOM
	domResult, err := client.CallWithContext(ctxT, sessionID, "Page.getOptimizedDOM", map[string]interface{}{
		"profile": "default",
	})
	if err != nil {
		fmt.Println("FAIL Page.getOptimizedDOM:", err)
	} else {
		fmt.Println("OK Page.getOptimizedDOM works! Result length:", len(domResult))
	}

	// Try navigate
	navResult, err := client.CallWithContext(ctxT, sessionID, "Page.navigate", map[string]interface{}{
		"url": "about:blank",
	})
	if err != nil {
		fmt.Println("FAIL Page.navigate:", err)
	} else {
		fmt.Println("OK Page.navigate works:", string(navResult))
	}

	// Try resolveRef
	refResult, err := client.CallWithContext(ctxT, sessionID, "Page.resolveRef", map[string]interface{}{
		"ref": "test",
	})
	if err != nil {
		fmt.Println("Page.resolveRef:", err)
	} else {
		fmt.Println("OK Page.resolveRef works:", string(refResult))
	}

	// Try insertText
	_, err = client.CallWithContext(ctxT, sessionID, "Page.insertText", map[string]interface{}{"text": "hello"})
	if err != nil {
		fmt.Println("Page.insertText:", err)
	} else {
		fmt.Println("OK Page.insertText works")
	}

	// Try Accessibility.getFullAXTree
	axResult, err := client.CallWithContext(ctxT, sessionID, "Accessibility.getFullAXTree", nil)
	if err != nil {
		fmt.Println("FAIL Accessibility.getFullAXTree:", err)
	} else {
		fmt.Println("OK Accessibility.getFullAXTree works. Result length:", len(axResult))
	}

	fmt.Println("\nDONE")
}
