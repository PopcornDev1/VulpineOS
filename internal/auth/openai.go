package auth

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token,omitempty"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type PKCEPair struct {
	Verifier  string
	Challenge string
}

const (
	clientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	authURL       = "https://auth.openai.com/oauth/authorize"
	tokenURL      = "https://auth.openai.com/oauth/token"
	redirectPort  = 1455
	scope         = "openid profile email offline_access"
	redirectPath  = "/auth/callback"
	loginTimeout  = 5 * time.Minute
)

func LoginOpenAI(w io.Writer) error {
	pkce, err := generatePKCE()
	if err != nil {
		return fmt.Errorf("generate PKCE: %w", err)
	}

	state, err := generateState()
	if err != nil {
		return fmt.Errorf("generate state: %w", err)
	}

	redirectURI := fmt.Sprintf("http://localhost:%d%s", redirectPort, redirectPath)

	callbackCh, cancel := tryStartCallbackServer()
	defer cancel()

	params := url.Values{
		"response_type":              {"code"},
		"client_id":                  {clientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {scope},
		"code_challenge":             {pkce.Challenge},
		"code_challenge_method":      {"S256"},
		"state":                      {state},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		"originator":                 {"vulpineos"},
	}
	authorizeURL := authURL + "?" + params.Encode()

	fmt.Fprintf(w, "\nOpenAI Authentication\n")
	fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(w, "\nOpen this URL in your browser:\n   %s\n", authorizeURL)
	fmt.Fprintf(w, "\nSign in with your ChatGPT Plus/Pro account.\n")

	if err := openBrowser(authorizeURL); err != nil {
		fmt.Fprintf(w, "\n(We tried to open your browser but couldn't. Open the URL above manually.)\n")
	}

	var code string
	if callbackCh != nil {
		fmt.Fprintf(w, "\nWaiting for authorization in browser...\n")
		select {
		case cb := <-callbackCh:
			code = cb
		case <-time.After(loginTimeout):
			return fmt.Errorf("authorization timed out after 5 minutes")
		}
	} else {
		fmt.Fprintf(w, "\nAfter signing in, your browser will try to redirect to a local URL.\n")
		fmt.Fprintf(w, "Copy that FULL URL from the address bar and paste it below:\n> ")

		reader := bufio.NewReader(os.Stdin)
		input, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}
		input = strings.TrimSpace(input)
		if input == "" {
			return fmt.Errorf("no input provided")
		}

		parsed, err := url.Parse(input)
		if err != nil || parsed.Scheme == "" {
			return fmt.Errorf("invalid URL: %s", input)
		}

		code = parsed.Query().Get("code")
		gotState := parsed.Query().Get("state")
		if gotState != "" && gotState != state {
			return fmt.Errorf("state mismatch: possible CSRF attack")
		}
	}

	if code == "" {
		return fmt.Errorf("no authorization code received")
	}

	tokenResp, err := exchangeAuthCodeForTokens(code, redirectURI, pkce.Verifier)
	if err != nil {
		return fmt.Errorf("exchange authorization code: %w", err)
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

	resp, err := postForm("https://auth.openai.com/oauth/token", data)
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

	refreshToken := tokenResp.RefreshToken
	if refreshToken == "" {
		refreshToken = creds.RefreshToken
	}

	updated := OAuthCredentials{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: refreshToken,
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

func generatePKCE() (*PKCEPair, error) {
	verifier := make([]byte, 64)
	if _, err := rand.Read(verifier); err != nil {
		return nil, err
	}
	verStr := base64.RawURLEncoding.EncodeToString(verifier)

	hash := sha256.Sum256([]byte(verStr))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEPair{Verifier: verStr, Challenge: challenge}, nil
}

func generateState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowser(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "linux":
		return exec.Command("xdg-open", u).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", u).Start()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func tryStartCallbackServer() (chan string, func()) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", redirectPort))
	if err != nil {
		return nil, func() {}
	}

	ch := make(chan string, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(redirectPath, func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code != "" {
			ch <- code
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `<html><body><h1>Authorization received!</h1><p>You can close this window and return to the terminal.</p></body></html>`)
		} else {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "No authorization code received.")
		}
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(listener)

	return ch, func() {
		srv.Close()
		listener.Close()
	}
}

func exchangeAuthCodeForTokens(code, redirectURI, codeVerifier string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	}

	resp, err := postForm(tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" || tokenResp.RefreshToken == "" {
		return nil, fmt.Errorf("token response missing required fields")
	}

	return &tokenResp, nil
}

func postForm(urlStr string, data url.Values) (*http.Response, error) {
	body := strings.NewReader(data.Encode())
	req, err := http.NewRequest("POST", urlStr, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Origin", "https://auth.openai.com")
	req.Header.Set("Referer", "https://auth.openai.com/")
	return http.DefaultClient.Do(req)
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
