package recording

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Viewer displays recorded browser actions as a formatted timeline.
type Viewer struct {
	recorder *Recorder
	output   io.Writer
}

// NewViewer creates a Viewer that reads from the given Recorder and writes to output.
func NewViewer(recorder *Recorder, output io.Writer) *Viewer {
	return &Viewer{
		recorder: recorder,
		output:   output,
	}
}

// Show prints the full timeline for the given agent to the output writer.
// Each action is formatted as: [MM:SS.mmm] TYPE  summary
func (v *Viewer) Show(agentID string) error {
	timeline := v.recorder.GetTimeline(agentID)
	if len(timeline) == 0 {
		fmt.Fprintf(v.output, "No actions recorded for agent %s\n", agentID)
		return nil
	}

	start := timeline[0].Timestamp
	for _, action := range timeline {
		line := formatAction(action, start)
		fmt.Fprintln(v.output, line)
	}
	return nil
}

// ShowLive prints the timeline with real-time delays between actions.
func (v *Viewer) ShowLive(agentID string) error {
	timeline := v.recorder.GetTimeline(agentID)
	if len(timeline) == 0 {
		fmt.Fprintf(v.output, "No actions recorded for agent %s\n", agentID)
		return nil
	}

	start := timeline[0].Timestamp
	for i, action := range timeline {
		if i > 0 {
			gap := action.Timestamp.Sub(timeline[i-1].Timestamp)
			if gap > 0 {
				time.Sleep(gap)
			}
		}
		line := formatAction(action, start)
		fmt.Fprintln(v.output, line)
	}
	return nil
}

// formatAction renders a single action as a timeline entry.
func formatAction(a Action, start time.Time) string {
	elapsed := a.Timestamp.Sub(start)
	mins := int(elapsed.Minutes())
	secs := int(elapsed.Seconds()) % 60
	millis := int(elapsed.Milliseconds()) % 1000

	ts := fmt.Sprintf("[%02d:%02d.%03d]", mins, secs, millis)
	label := strings.ToUpper(string(a.Type))
	summary := actionSummary(a)

	return fmt.Sprintf("%s %-10s %s", ts, label, summary)
}

// actionSummary extracts a human-readable summary from the action data.
func actionSummary(a Action) string {
	var data map[string]interface{}
	if len(a.Data) > 0 {
		if err := json.Unmarshal(a.Data, &data); err != nil {
			return "(invalid data)"
		}
	}

	switch a.Type {
	case ActionNavigate:
		if url, ok := data["url"].(string); ok {
			return url
		}
		return "(no url)"

	case ActionClick:
		selector, _ := data["selector"].(string)
		if selector != "" {
			return selector
		}
		x, _ := data["x"].(float64)
		y, _ := data["y"].(float64)
		return fmt.Sprintf("(%d, %d)", int(x), int(y))

	case ActionType_:
		selector, _ := data["selector"].(string)
		text, _ := data["text"].(string)
		if selector != "" && text != "" {
			return fmt.Sprintf("%s %q", selector, text)
		}
		if text != "" {
			return fmt.Sprintf("%q", text)
		}
		return "(no text)"

	case ActionScroll:
		dx, _ := data["deltaX"].(float64)
		dy, _ := data["deltaY"].(float64)
		return fmt.Sprintf("(%d, %d)", int(dx), int(dy))

	case ActionScreenshot:
		w, _ := data["width"].(float64)
		h, _ := data["height"].(float64)
		if w > 0 && h > 0 {
			return fmt.Sprintf("(%dx%d)", int(w), int(h))
		}
		return "(full page)"

	case ActionEvaluate:
		expr, _ := data["expression"].(string)
		if len(expr) > 60 {
			expr = expr[:57] + "..."
		}
		return expr

	case ActionSetContent:
		html, _ := data["html"].(string)
		if len(html) > 60 {
			html = html[:57] + "..."
		}
		return html

	default:
		return ""
	}
}
