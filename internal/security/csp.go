package security

import (
	"encoding/json"
	"fmt"
	"strings"
	"vulpineos/internal/juggler"
)

// CSPConfig controls Content-Security-Policy header injection.
type CSPConfig struct {
	BlockInlineScripts   bool   // default true
	BlockExternalScripts bool   // default false (would break most sites)
	BlockDataURIs        bool   // default false
	CustomDirectives     string // additional CSP directives
}

// DefaultCSPConfig returns a CSPConfig with safe defaults.
func DefaultCSPConfig() CSPConfig {
	return CSPConfig{
		BlockInlineScripts:   true,
		BlockExternalScripts: false,
		BlockDataURIs:        false,
	}
}

// GenerateCSP returns a Content-Security-Policy header value for the given config.
func GenerateCSP(cfg CSPConfig) string {
	var directives []string

	// Build script-src directive
	var scriptSources []string
	scriptSources = append(scriptSources, "'self'")
	if !cfg.BlockInlineScripts {
		scriptSources = append(scriptSources, "'unsafe-inline'")
	}
	if cfg.BlockExternalScripts {
		// Only allow self, no external domains
		directives = append(directives, "script-src "+strings.Join(scriptSources, " "))
	} else {
		// Allow external scripts via https:
		scriptSources = append(scriptSources, "https:")
		directives = append(directives, "script-src "+strings.Join(scriptSources, " "))
	}

	if cfg.BlockDataURIs {
		directives = append(directives, "default-src 'self' https:")
		directives = append(directives, "img-src 'self' https: blob:")
	}

	if cfg.CustomDirectives != "" {
		directives = append(directives, cfg.CustomDirectives)
	}

	return strings.Join(directives, "; ")
}

// InjectCSP sets the CSP header on a browser context via Browser.setExtraHTTPHeaders.
func InjectCSP(client *juggler.Client, contextID string, cfg CSPConfig) error {
	csp := GenerateCSP(cfg)
	if csp == "" {
		return fmt.Errorf("generated CSP is empty")
	}

	params := map[string]interface{}{
		"browserContextId": contextID,
		"headers": []map[string]string{
			{
				"name":  "Content-Security-Policy",
				"value": csp,
			},
		},
	}

	_, err := client.Call("", "Browser.setExtraHTTPHeaders", params)
	if err != nil {
		return fmt.Errorf("inject CSP: %w", err)
	}
	return nil
}

// InjectCSPRaw is a lower-level version that returns the raw JSON params
// that would be sent to Browser.setExtraHTTPHeaders (useful for testing).
func InjectCSPRaw(contextID string, cfg CSPConfig) (json.RawMessage, error) {
	csp := GenerateCSP(cfg)
	if csp == "" {
		return nil, fmt.Errorf("generated CSP is empty")
	}

	params := map[string]interface{}{
		"browserContextId": contextID,
		"headers": []map[string]string{
			{
				"name":  "Content-Security-Policy",
				"value": csp,
			},
		},
	}

	return json.Marshal(params)
}
