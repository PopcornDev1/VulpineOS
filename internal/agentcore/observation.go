package agentcore

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ObservationObserved   = "observed"
	ObservationUnverified = "unverified"
	ObservationLost       = "lost"
)

// ObservationState describes the agent's browser evidence state. It is emitted
// for operator surfaces and used to keep final replies grounded after failures.
type ObservationState struct {
	Confidence          string
	LastObservedTool    string
	LastObservedSummary string
	LastFailedTool      string
	LastFailure         string
	URL                 string
	Title               string
	Lost                bool
}

type observationStateEvents interface {
	OnObservationState(ObservationState)
}

func emitObservationState(events Events, state ObservationState) {
	if sink, ok := events.(observationStateEvents); ok {
		sink.OnObservationState(state)
	}
}

func observedStateFromTool(name, result string) ObservationState {
	state := ObservationState{
		Confidence:          ObservationObserved,
		LastObservedTool:    name,
		LastObservedSummary: compactObservationSummary(name, result),
	}
	if report, ok := parseObserveObservation(result); ok {
		state.Confidence = report.Confidence
		state.URL = report.URL
		state.Title = report.Title
		state.LastObservedSummary = report.summary(name)
		if state.Confidence == ObservationLost {
			state.Lost = true
		}
		return state
	}
	if info, ok := parsePageInfoObservation(result); ok {
		state.URL = info.URL
		state.Title = info.Title
		state.LastObservedSummary = info.summary(name)
		if info.isLost() {
			state.Confidence = ObservationLost
			state.Lost = true
		}
	}
	return state
}

func unverifiedStateAfterFailure(last ObservationState, tool, result string) ObservationState {
	state := last
	state.Confidence = ObservationUnverified
	state.LastFailedTool = strings.TrimSpace(tool)
	state.LastFailure = compactObservationText(result)
	return state
}

func compactObservationSummary(name, result string) string {
	result = compactObservationText(result)
	if result == "" {
		return name
	}
	return fmt.Sprintf("%s: %s", name, result)
}

func compactObservationText(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " "))
	if s == "" {
		return ""
	}
	return truncate(s, 220)
}

type pageInfoObservation struct {
	URL        string `json:"url"`
	Title      string `json:"title"`
	ReadyState string `json:"readyState"`
	Forms      int    `json:"forms"`
	Inputs     int    `json:"inputs"`
	Buttons    int    `json:"buttons"`
	Links      int    `json:"links"`
}

type observeObservation struct {
	Confidence string   `json:"confidence"`
	URL        string   `json:"url"`
	Title      string   `json:"title"`
	PageInfo   string   `json:"page_info"`
	Errors     []string `json:"errors"`
	Guidance   string   `json:"guidance"`
}

func parseObserveObservation(result string) (observeObservation, bool) {
	var report observeObservation
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &report); err != nil {
		return observeObservation{}, false
	}
	switch report.Confidence {
	case ObservationObserved, ObservationUnverified, ObservationLost:
	default:
		return observeObservation{}, false
	}
	return report, true
}

func (o observeObservation) summary(tool string) string {
	parts := []string{tool, "confidence=" + o.Confidence}
	if strings.TrimSpace(o.URL) != "" {
		parts = append(parts, "url="+o.URL)
	}
	if strings.TrimSpace(o.Title) != "" {
		parts = append(parts, "title="+o.Title)
	}
	if strings.TrimSpace(o.PageInfo) != "" {
		parts = append(parts, "page_info="+truncate(o.PageInfo, 120))
	}
	if len(o.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("errors=%d", len(o.Errors)))
	}
	if strings.TrimSpace(o.Guidance) != "" {
		parts = append(parts, "guidance="+truncate(o.Guidance, 120))
	}
	return strings.Join(parts, " ")
}

func parsePageInfoObservation(result string) (pageInfoObservation, bool) {
	var info pageInfoObservation
	if err := json.Unmarshal([]byte(strings.TrimSpace(result)), &info); err != nil {
		return pageInfoObservation{}, false
	}
	return info, true
}

func (p pageInfoObservation) summary(tool string) string {
	parts := []string{tool}
	if strings.TrimSpace(p.URL) != "" {
		parts = append(parts, "url="+p.URL)
	}
	if strings.TrimSpace(p.Title) != "" {
		parts = append(parts, "title="+p.Title)
	}
	if p.ReadyState != "" {
		parts = append(parts, "readyState="+p.ReadyState)
	}
	parts = append(parts, fmt.Sprintf("forms=%d inputs=%d buttons=%d links=%d", p.Forms, p.Inputs, p.Buttons, p.Links))
	return strings.Join(parts, " ")
}

func (p pageInfoObservation) isLost() bool {
	return strings.EqualFold(strings.TrimSpace(p.URL), "about:blank") &&
		strings.TrimSpace(p.Title) == "" &&
		p.Forms == 0 &&
		p.Inputs == 0 &&
		p.Buttons == 0 &&
		p.Links == 0
}
