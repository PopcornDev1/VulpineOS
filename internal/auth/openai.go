package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vulpineos/internal/config"
)

func OpenAICredentialPath() string {
	return filepath.Join(config.Dir(), "credentials", "openai-oauth.json")
}

type OAuthCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	AccountID    string `json:"account_id,omitempty"`
}

type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	Interval        int    `json:"interval"`
	ExpiresIn       int    `json:"expires_in"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

const clientID = "app_EMoamEEZ73f0CkXaXp7hrann"

func LoginOpenAI(w io.Writer) error {
	dcResp, err := requestDeviceCode()
	if err != nil {
		return fmt.Errorf("request device code: %w", err)
	}

	fmt.Fprintf(w, "\nOpenAI Codex Device Code Authentication\n")
	fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(w, "\n1. Open this URL in any browser:\n   %s\n", dcResp.VerificationURI)
	fmt.Fprintf(w, "\n2. Enter this one-time code:\n   %s\n", dcResp.UserCode)
	fmt.Fprintf(w, "\n3. Sign in with your ChatGPT Plus/Pro account.\n")
	fmt.Fprintf(w, "\nWaiting for authorization...\n")

	tokenResp, err := pollForToken(dcResp.DeviceCode, dcResp.Interval, dcResp.ExpiresIn)
	if err != nil {
		return fmt.Errorf("poll for token: %w", err)
	}

	accountID := extractAccountID(tokenResp.AccessToken)

	creds := OAuthCredentials{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Unix() + int64(tokenResp.ExpiresIn),
		AccountID:    accountID,
	}
	if err := writeCredentials(creds); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}

	fmt.Fprintf(w, "\n✓ Authentication successful!\n")
	fmt.Fprintf(w, "  Credentials saved to %s\n", OpenAICredentialPath())
	return nil
}

func LogoutOpenAI() error {
	path := OpenAICredentialPath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove credentials: %w", err)
	}
	return nil
}

func OpenAIStatus() (*OAuthCredentials, error) {
	creds, err := readCredentials()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return creds, nil
}

func RefreshOpenAIToken() (*OAuthCredentials, error) {
	creds, err := readCredentials()
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}

	data := url.Values{
		"client_id":     {clientID},
		"refresh_token": {creds.RefreshToken},
		"grant_type":    {"refresh_token"},
	}

	resp, err := http.PostForm("https://auth.openai.com/oauth/token", data)
	if err != nil {
		return nil, fmt.Errorf("refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read refresh response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token refresh failed: %s", string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	updated := OAuthCredentials{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Unix() + int64(tokenResp.ExpiresIn),
		AccountID:    creds.AccountID,
	}
	if err := writeCredentials(updated); err != nil {
		return nil, fmt.Errorf("write refreshed credentials: %w", err)
	}

	return &updated, nil
}

func IsOAuthTokenExpired(creds *OAuthCredentials, buffer time.Duration) bool {
	return time.Now().Add(buffer).Unix() > creds.ExpiresAt
}

func ValidAccessToken() (string, error) {
	creds, err := readCredentials()
	if err != nil {
		return "", fmt.Errorf("not logged in: run 'vulpine auth login --provider openai' first")
	}
	if IsOAuthTokenExpired(creds, 5*time.Minute) {
		creds, err = RefreshOpenAIToken()
		if err != nil {
			return "", fmt.Errorf("token refresh failed: %w", err)
		}
	}
	return creds.AccessToken, nil
}

// --- internal helpers ---

func requestDeviceCode() (*DeviceCodeResponse, error) {
	data := url.Values{
		"client_id": {clientID},
		"scope":     {"openid profile email offline_access"},
	}
	resp, err := http.PostForm("https://auth.openai.com/oauth/device/code", data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("device code request failed: %s", string(body))
	}

	var dc DeviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return nil, err
	}
	return &dc, nil
}

func pollForToken(deviceCode string, interval int, expiresIn int) (*TokenResponse, error) {
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	pollInterval := time.Duration(interval) * time.Second
	if pollInterval < 2*time.Second {
		pollInterval = 5 * time.Second
	}

	for time.Now().Before(deadline) {
		time.Sleep(pollInterval)

		data := url.Values{
			"client_id":   {clientID},
			"device_code": {deviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}

		resp, err := http.PostForm("https://auth.openai.com/oauth/token", data)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 200 {
			var token TokenResponse
			if err := json.Unmarshal(body, &token); err != nil {
				return nil, err
			}
			return &token, nil
		}

		if resp.StatusCode == 404 {
			return nil, fmt.Errorf("device code expired, please run auth login again")
		}
	}

	return nil, fmt.Errorf("authorization timed out after %d seconds", expiresIn)
}

func extractAccountID(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return ""
	}

	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}

	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return ""
	}
	return claims.Sub
}

func readCredentials() (*OAuthCredentials, error) {
	path := OpenAICredentialPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var creds OAuthCredentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return &creds, nil
}

func writeCredentials(creds OAuthCredentials) error {
	path := OpenAICredentialPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
