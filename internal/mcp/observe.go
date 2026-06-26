package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"vulpineos/internal/juggler"
)

type observeReport struct {
	Confidence     string   `json:"confidence"`
	URL            string   `json:"url,omitempty"`
	Title          string   `json:"title,omitempty"`
	PageInfo       string   `json:"page_info,omitempty"`
	Snapshot       string   `json:"snapshot,omitempty"`
	VisualFallback string   `json:"visual_fallback,omitempty"`
	Errors         []string `json:"errors,omitempty"`
	Guidance       string   `json:"guidance,omitempty"`
}

type observePageInfo struct {
	URL        string `json:"url"`
	Title      string `json:"title"`
	ReadyState string `json:"readyState"`
	Forms      int    `json:"forms"`
	Inputs     int    `json:"inputs"`
	Buttons    int    `json:"buttons"`
	Links      int    `json:"links"`
}

func handleObserve(ctx context.Context, client *juggler.Client, tracker *ContextTracker, args json.RawMessage) (*ToolCallResult, error) {
	var p struct {
		SessionID   string `json:"sessionId"`
		Profile     string `json:"profile"`
		Visual      *bool  `json:"visual"`
		MaxElements int    `json:"maxElements"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return errorResult(err), nil
	}
	if strings.TrimSpace(p.SessionID) == "" {
		return errorResult(fmt.Errorf("sessionId is required")), nil
	}
	profile := strings.TrimSpace(p.Profile)
	if profile == "" {
		profile = "compact"
	}
	maxElements := p.MaxElements
	if maxElements <= 0 {
		maxElements = 50
	}
	useVisual := true
	if p.Visual != nil {
		useVisual = *p.Visual
	}

	report := observeReport{Confidence: "unverified"}

	pageArgs := mustJSON(map[string]interface{}{"sessionId": p.SessionID})
	pageInfo, _ := handleGetPageInfo(client, tracker, pageArgs)
	pageInfoText := compactToolResultText(pageInfo, 700)
	if pageInfo != nil && !pageInfo.IsError {
		report.PageInfo = pageInfoText
		if info, ok := parseObservePageInfo(pageInfoText); ok {
			report.URL = info.URL
			report.Title = info.Title
			report.Confidence = "observed"
			if info.isLost() {
				report.Confidence = "lost"
				report.Guidance = "The page appears blank/about:blank. Retry navigation or ask the user to show/take over the browser before claiming progress."
			}
		} else {
			report.Confidence = "observed"
		}
	} else if pageInfoText != "" {
		report.Errors = append(report.Errors, "page_info: "+pageInfoText)
	}

	snapshotArgs := mustJSON(map[string]interface{}{
		"sessionId": p.SessionID,
		"profile":   profile,
	})
	snapshot, _ := handleSnapshot(client, snapshotArgs)
	snapshotText := compactToolResultText(snapshot, 1400)
	if snapshot != nil && !snapshot.IsError {
		report.Snapshot = snapshotText
		if report.Confidence == "unverified" {
			report.Confidence = "observed"
		}
	} else if snapshotText != "" {
		report.Errors = append(report.Errors, "snapshot: "+snapshotText)
	}

	needsVisual := useVisual && (report.Confidence == "lost" || snapshot == nil || snapshot.IsError || pageInfo == nil || pageInfo.IsError)
	if needsVisual {
		visualArgs := mustJSON(map[string]interface{}{
			"sessionId":   p.SessionID,
			"maxElements": maxElements,
		})
		visual := handleAnnotatedScreenshot(ctx, client, visualArgs)
		visualText := compactToolResultText(visual, 900)
		if visual != nil && !visual.IsError {
			report.VisualFallback = visualText
			if report.Confidence == "unverified" && hasTextualVisualEvidence(visual) {
				report.Confidence = "observed"
			}
		} else if visualText != "" {
			report.Errors = append(report.Errors, "visual: "+visualText)
		}
	}

	if report.Confidence == "unverified" && len(report.Errors) > 0 && report.VisualFallback == "" {
		return errorResult(fmt.Errorf("observe failed: %s", strings.Join(report.Errors, "; "))), nil
	}
	if report.Guidance == "" && len(report.Errors) > 0 {
		report.Guidance = "Some observation paths failed; use the successful fields above and retry with visual:true or profile:\"expanded\" if needed."
	}
	if report.Guidance == "" && report.Confidence == "unverified" && report.VisualFallback != "" {
		report.Guidance = "Only a plain image fallback was captured; native agents cannot inspect image pixels. Use annotated labels, retry DOM/AX observation, or ask the user to take over before claiming success."
	}

	data, err := json.Marshal(report)
	if err != nil {
		return errorResult(err), nil
	}
	return textResult(string(data)), nil
}

func mustJSON(value interface{}) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func compactToolResultText(result *ToolCallResult, max int) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, strings.TrimSpace(block.Text))
			}
		case "image":
			parts = append(parts, "[image captured]")
		}
	}
	text := strings.Join(parts, "\n")
	text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " "))
	return truncate(text, max)
}

func hasTextualVisualEvidence(result *ToolCallResult) bool {
	if result == nil || result.IsError {
		return false
	}
	for _, block := range result.Content {
		if block.Type != "text" {
			continue
		}
		text := strings.TrimSpace(block.Text)
		if text != "" && text != "[]" && text != "{}" {
			return true
		}
	}
	return false
}

func parseObservePageInfo(text string) (observePageInfo, bool) {
	var info observePageInfo
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &info); err != nil {
		return observePageInfo{}, false
	}
	return info, true
}

func (p observePageInfo) isLost() bool {
	return strings.EqualFold(strings.TrimSpace(p.URL), "about:blank") &&
		strings.TrimSpace(p.Title) == "" &&
		p.Forms == 0 &&
		p.Inputs == 0 &&
		p.Buttons == 0 &&
		p.Links == 0
}
