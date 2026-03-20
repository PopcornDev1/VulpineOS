package juggler

// Domain-specific types for the Juggler protocol.

// BrowserInfo is the response from Browser.getInfo.
type BrowserInfo struct {
	UserAgent string `json:"userAgent"`
	Version   string `json:"version"`
}

// TargetInfo describes a page target.
type TargetInfo struct {
	TargetID         string `json:"targetId"`
	Type             string `json:"type"`
	BrowserContextID string `json:"browserContextId"`
	URL              string `json:"url"`
	OpenerId         string `json:"openerId,omitempty"`
}

// AttachedToTarget is the Browser.attachedToTarget event payload.
type AttachedToTarget struct {
	SessionID  string     `json:"sessionId"`
	TargetInfo TargetInfo `json:"targetInfo"`
}

// DetachedFromTarget is the Browser.detachedFromTarget event payload.
type DetachedFromTarget struct {
	SessionID string `json:"sessionId"`
	TargetID  string `json:"targetId"`
}

// TelemetryUpdate is the Browser.telemetryUpdate event payload.
type TelemetryUpdate struct {
	MemoryMB           float64 `json:"memoryMB"`
	EventLoopLagMs     float64 `json:"eventLoopLagMs"`
	DetectionRiskScore float64 `json:"detectionRiskScore"`
	ActiveContexts     int     `json:"activeContexts"`
	ActivePages        int     `json:"activePages"`
	Timestamp          float64 `json:"timestamp"`
}

// InjectionAttempt is the Browser.injectionAttemptDetected event payload.
type InjectionAttempt struct {
	BrowserContextID string  `json:"browserContextId,omitempty"`
	URL              string  `json:"url"`
	AttemptType      string  `json:"attemptType"`
	Details          string  `json:"details"`
	Timestamp        float64 `json:"timestamp"`
	Blocked          bool    `json:"blocked"`
}

// TrustWarmingState is the Browser.trustWarmingStateChanged event payload.
type TrustWarmingState struct {
	State       string `json:"state"`
	CurrentSite string `json:"currentSite,omitempty"`
}

// CreateBrowserContextResult is the response from Browser.createBrowserContext.
type CreateBrowserContextResult struct {
	BrowserContextID string `json:"browserContextId"`
}

// NewPageResult is the response from Browser.newPage.
type NewPageResult struct {
	TargetID string `json:"targetId"`
}

// TrustWarmingStatus is the response from Browser.getTrustWarmingStatus.
type TrustWarmingStatus struct {
	State       string  `json:"state"`
	SitesWarmed int     `json:"sitesWarmed"`
	CurrentSite string  `json:"currentSite,omitempty"`
	LastVisit   float64 `json:"lastVisit,omitempty"`
}
