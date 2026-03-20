// demo-agent simulates an OpenClaw agent by printing JSON-lines status updates to stdout.
// Used for testing the VulpineOS TUI agent panel without needing real OpenClaw installed.
//
// Usage: go run ./cmd/demo-agent
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"
)

type output struct {
	Type      string `json:"type"`
	Status    string `json:"status,omitempty"`
	Objective string `json:"objective,omitempty"`
	Tokens    int    `json:"tokens,omitempty"`
	Message   string `json:"message,omitempty"`
}

func emit(o output) {
	data, _ := json.Marshal(o)
	fmt.Println(string(data))
	os.Stdout.Sync()
}

func main() {
	objectives := []string{
		"Searching for competitor pricing on Amazon",
		"Navigating to product page",
		"Extracting price data from results",
		"Comparing prices across 3 vendors",
		"Compiling results into summary",
		"Writing final report",
	}

	tokens := 0

	emit(output{Type: "status", Status: "starting", Objective: "Initializing agent..."})
	time.Sleep(time.Duration(500+rand.Intn(1000)) * time.Millisecond)

	emit(output{Type: "status", Status: "running", Objective: objectives[0], Tokens: 150})
	tokens = 150

	for i, obj := range objectives {
		duration := time.Duration(2000+rand.Intn(3000)) * time.Millisecond
		time.Sleep(duration)

		tokens += 200 + rand.Intn(500)
		status := "running"
		if i%2 == 1 {
			status = "thinking"
		}

		emit(output{Type: "status", Status: status, Objective: obj, Tokens: tokens})
		emit(output{Type: "log", Message: fmt.Sprintf("Step %d/%d: %s", i+1, len(objectives), obj)})
	}

	time.Sleep(1 * time.Second)
	emit(output{Type: "result", Message: "Task completed successfully. Found 3 pricing data points."})
	emit(output{Type: "status", Status: "completed", Objective: "Task complete", Tokens: tokens})
}
