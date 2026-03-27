// Package costtrack tracks token usage and API costs per agent with budget limits.
package costtrack

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Usage tracks cumulative token usage and cost for an agent.
type Usage struct {
	AgentID       string    `json:"agentId"`
	InputTokens   int64     `json:"inputTokens"`
	OutputTokens  int64     `json:"outputTokens"`
	TotalTokens   int64     `json:"totalTokens"`
	EstimatedCost float64   `json:"estimatedCost"` // USD
	LastUpdated   time.Time `json:"lastUpdated"`
}

// Budget defines spending limits for an agent.
type Budget struct {
	AgentID       string  `json:"agentId"`
	MaxCostUSD    float64 `json:"maxCostUsd"`    // 0 = unlimited
	MaxTokens     int64   `json:"maxTokens"`     // 0 = unlimited
	AlertPercent  float64 `json:"alertPercent"`   // alert at this % of budget (default 80)
}

// CostEvent is emitted when usage changes.
type CostEvent struct {
	AgentID     string  `json:"agentId"`
	Type        string  `json:"type"` // "usage", "alert", "limit_reached"
	TotalCost   float64 `json:"totalCost"`
	TotalTokens int64   `json:"totalTokens"`
	Message     string  `json:"message"`
}

// ModelPricing defines cost per 1M tokens for a model.
type ModelPricing struct {
	InputPer1M  float64
	OutputPer1M float64
}

// Common model pricing (approximate, USD per 1M tokens)
var DefaultPricing = map[string]ModelPricing{
	"claude-sonnet-4-6":   {InputPer1M: 3.0, OutputPer1M: 15.0},
	"claude-opus-4-6":     {InputPer1M: 15.0, OutputPer1M: 75.0},
	"claude-haiku-4-5":    {InputPer1M: 0.80, OutputPer1M: 4.0},
	"gpt-4o":              {InputPer1M: 2.5, OutputPer1M: 10.0},
	"gpt-4o-mini":         {InputPer1M: 0.15, OutputPer1M: 0.6},
	"gemini-2.5-pro":      {InputPer1M: 1.25, OutputPer1M: 10.0},
	"gemini-2.5-flash":    {InputPer1M: 0.15, OutputPer1M: 0.6},
}

// Tracker manages cost tracking across agents.
type Tracker struct {
	mu       sync.RWMutex
	usage    map[string]*Usage  // agentID → usage
	budgets  map[string]*Budget // agentID → budget
	model    string             // current model for pricing
	events   chan CostEvent
}

// New creates a new cost tracker.
func New(model string) *Tracker {
	return &Tracker{
		usage:   make(map[string]*Usage),
		budgets: make(map[string]*Budget),
		model:   model,
		events:  make(chan CostEvent, 50),
	}
}

// Events returns the channel for cost events.
func (t *Tracker) Events() <-chan CostEvent {
	return t.events
}

// SetBudget configures a budget for an agent.
func (t *Tracker) SetBudget(agentID string, maxCost float64, maxTokens int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.budgets[agentID] = &Budget{
		AgentID:      agentID,
		MaxCostUSD:   maxCost,
		MaxTokens:    maxTokens,
		AlertPercent: 80,
	}
}

// RecordUsage adds token usage for an agent and returns whether budget allows it.
func (t *Tracker) RecordUsage(agentID string, inputTokens, outputTokens int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	u, ok := t.usage[agentID]
	if !ok {
		u = &Usage{AgentID: agentID}
		t.usage[agentID] = u
	}

	u.InputTokens += inputTokens
	u.OutputTokens += outputTokens
	u.TotalTokens += inputTokens + outputTokens
	u.LastUpdated = time.Now()

	// Calculate cost
	pricing := DefaultPricing[t.model]
	if pricing.InputPer1M == 0 {
		pricing = ModelPricing{InputPer1M: 1.0, OutputPer1M: 3.0} // fallback
	}
	u.EstimatedCost = float64(u.InputTokens)/1e6*pricing.InputPer1M +
		float64(u.OutputTokens)/1e6*pricing.OutputPer1M

	// Check budget
	budget := t.budgets[agentID]
	if budget != nil {
		// Check token limit
		if budget.MaxTokens > 0 && u.TotalTokens >= budget.MaxTokens {
			t.emitEvent(CostEvent{
				AgentID: agentID, Type: "limit_reached",
				TotalCost: u.EstimatedCost, TotalTokens: u.TotalTokens,
				Message: fmt.Sprintf("Token limit reached: %d/%d", u.TotalTokens, budget.MaxTokens),
			})
			return false
		}
		// Check cost limit
		if budget.MaxCostUSD > 0 && u.EstimatedCost >= budget.MaxCostUSD {
			t.emitEvent(CostEvent{
				AgentID: agentID, Type: "limit_reached",
				TotalCost: u.EstimatedCost, TotalTokens: u.TotalTokens,
				Message: fmt.Sprintf("Cost limit reached: $%.4f/$%.4f", u.EstimatedCost, budget.MaxCostUSD),
			})
			return false
		}
		// Check alert threshold
		alertPct := budget.AlertPercent
		if alertPct == 0 {
			alertPct = 80
		}
		if budget.MaxCostUSD > 0 && u.EstimatedCost >= budget.MaxCostUSD*alertPct/100 {
			t.emitEvent(CostEvent{
				AgentID: agentID, Type: "alert",
				TotalCost: u.EstimatedCost, TotalTokens: u.TotalTokens,
				Message: fmt.Sprintf("%.0f%% of budget used: $%.4f/$%.4f", u.EstimatedCost/budget.MaxCostUSD*100, u.EstimatedCost, budget.MaxCostUSD),
			})
		}
	}

	// Emit usage event
	t.emitEvent(CostEvent{
		AgentID: agentID, Type: "usage",
		TotalCost: u.EstimatedCost, TotalTokens: u.TotalTokens,
	})

	return true
}

// GetUsage returns usage for an agent.
func (t *Tracker) GetUsage(agentID string) *Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if u, ok := t.usage[agentID]; ok {
		cp := *u
		return &cp
	}
	return nil
}

// AllUsage returns usage for all agents.
func (t *Tracker) AllUsage() []Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]Usage, 0, len(t.usage))
	for _, u := range t.usage {
		result = append(result, *u)
	}
	return result
}

// TotalCost returns total cost across all agents.
func (t *Tracker) TotalCost() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total float64
	for _, u := range t.usage {
		total += u.EstimatedCost
	}
	return total
}

// MarshalJSON exports all tracking data.
func (t *Tracker) MarshalJSON() ([]byte, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return json.Marshal(struct {
		Usage   map[string]*Usage  `json:"usage"`
		Budgets map[string]*Budget `json:"budgets"`
		Model   string             `json:"model"`
	}{t.usage, t.budgets, t.model})
}

func (t *Tracker) emitEvent(ev CostEvent) {
	select {
	case t.events <- ev:
	default: // don't block if channel full
	}
}
