package remote

import (
	"crypto/subtle"
	"net/http"
)

// Authenticator validates incoming connections.
type Authenticator struct {
	apiKey string
}

// NewAuthenticator creates an authenticator with the given API key.
func NewAuthenticator(apiKey string) *Authenticator {
	return &Authenticator{apiKey: apiKey}
}

// Validate checks the request for a valid API key.
// Accepts: Authorization: Bearer <key> header or ?token=<key> query param.
func (a *Authenticator) Validate(r *http.Request) bool {
	if a.apiKey == "" {
		return true // no auth configured
	}

	// Check Authorization header
	auth := r.Header.Get("Authorization")
	if len(auth) > 7 && auth[:7] == "Bearer " {
		token := auth[7:]
		if subtle.ConstantTimeCompare([]byte(token), []byte(a.apiKey)) == 1 {
			return true
		}
	}

	// Check query parameter
	token := r.URL.Query().Get("token")
	if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(a.apiKey)) == 1 {
		return true
	}

	return false
}
