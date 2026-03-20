package vault

import "time"

// Citizen is a long-lived browser identity.
type Citizen struct {
	ID              string    `json:"id"`
	Label           string    `json:"label"`
	Fingerprint     string    `json:"fingerprint"`      // JSON blob (BrowserForge config)
	ProxyConfig     string    `json:"proxy_config"`      // JSON blob
	Locale          string    `json:"locale"`
	Timezone        string    `json:"timezone"`
	CreatedAt       time.Time `json:"created_at"`
	LastUsedAt      time.Time `json:"last_used_at"`
	TotalSessions   int       `json:"total_sessions"`
	DetectionEvents int       `json:"detection_events"`
}

// CitizenCookies stores cookies for a citizen per domain.
type CitizenCookies struct {
	CitizenID string    `json:"citizen_id"`
	Domain    string    `json:"domain"`
	Cookies   string    `json:"cookies"`   // JSON array of Juggler Cookie objects
	UpdatedAt time.Time `json:"updated_at"`
}

// CitizenStorage stores localStorage snapshots per origin.
type CitizenStorage struct {
	CitizenID string    `json:"citizen_id"`
	Origin    string    `json:"origin"`
	Data      string    `json:"data"` // JSON object
	UpdatedAt time.Time `json:"updated_at"`
}

// Template defines an agent configuration preset.
type Template struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	SOP             string    `json:"sop"`              // OpenClaw Standard Operating Procedure
	InteractionMode string    `json:"interaction_mode"` // readonly, form_fill, full
	AllowedDomains  string    `json:"allowed_domains"`  // JSON array of domain patterns
	Constraints     string    `json:"constraints"`      // JSON blob
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// NomadSession tracks an ephemeral agent session.
type NomadSession struct {
	ID          string    `json:"id"`
	TemplateID  string    `json:"template_id"`
	Fingerprint string    `json:"fingerprint"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Status      string    `json:"status"` // active, completed, failed
	Result      string    `json:"result"` // JSON blob
}
