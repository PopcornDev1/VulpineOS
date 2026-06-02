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
	clientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	authURL      = "https://auth.openai.com/oauth/authorize"
	tokenURL     = "https://auth.openai.com/oauth/token"
	redirectPort = 1455
	scope        = "openid profile email offline_access"
	redirectPath = "/auth/callback"
	loginTimeout = 5 * time.Minute
)

// OpenAILogin is an in-progress OpenAI (ChatGPT) OAuth login. Drive it from any
// front-end (TUI or terminal): show AuthURL to the user and/or call OpenBrowser,
// obtain the authorization code (WaitForCode while the local callback server is
// up, or CodeFromRedirectURL from a pasted redirect), then Complete to exchange
// and persist credentials. Always Close when finished.
type OpenAILogin struct {
	AuthURL       string // the authorize URL to open in a browser
	CallbackBound bool   // true if the localhost callback server is listening
	redirectURI   string
	verifier      string
	state         string
	codeCh        chan string
	cancel        func()
}

// BeginOpenAILogin starts an OAuth login: generates PKCE + state, tries to bind
// the localhost callback server, and builds the authorize URL.
func BeginOpenAILogin() (*OpenAILogin, error) {
	pkce, err := generatePKCE()
	if err != nil {
		return nil, fmt.Errorf("generate PKCE: %w", err)
	}
	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("generate state: %w", err)
	}
	redirectURI := fmt.Sprintf("http://localhost:%d%s", redirectPort, redirectPath)
	codeCh, cancel := tryStartCallbackServer()
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
		// OpenAI resolves the registered app from the (client_id, originator)
		// pair; this client_id is the public Codex CLI app, so the originator
		// must be "codex_cli_rs" to match it. Any other value (e.g. a custom
		// "vulpineos") makes the authorize endpoint return invalid_client.
		"originator": {"codex_cli_rs"},
	}
	return &OpenAILogin{
		AuthURL:       authURL + "?" + params.Encode(),
		CallbackBound: codeCh != nil,
		redirectURI:   redirectURI,
		verifier:      pkce.Verifier,
		state:         state,
		codeCh:        codeCh,
		cancel:        cancel,
	}, nil
}

// OpenBrowser best-effort opens the authorize URL in the user's browser.
func (l *OpenAILogin) OpenBrowser() error { return openBrowser(l.AuthURL) }

// WaitForCode blocks until the localhost callback receives the authorization
// code, or the timeout elapses. Errors if the callback server isn't bound.
func (l *OpenAILogin) WaitForCode(timeout time.Duration) (string, error) {
	if l.codeCh == nil {
		return "", fmt.Errorf("callback server not available; paste the redirect URL instead")
	}
	select {
	case code := <-l.codeCh:
		if code == "" {
			return "", fmt.Errorf("no authorization code received")
		}
		return code, nil
	case <-time.After(timeout):
		return "", fmt.Errorf("authorization timed out")
	}
}

// CodeFromRedirectURL extracts (and state-validates) the authorization code from
// a full redirect URL the user pastes when the callback server can't bind.
func (l *OpenAILogin) CodeFromRedirectURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" {
		return "", fmt.Errorf("invalid redirect URL")
	}
	if got := parsed.Query().Get("state"); got != "" && got != l.state {
		return "", fmt.Errorf("state mismatch: possible CSRF attack")
	}
	code := parsed.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("no authorization code in URL")
	}
	return code, nil
}

// Complete exchanges the authorization code for tokens and persists credentials.
func (l *OpenAILogin) Complete(code string) error {
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("no authorization code received")
	}
	tokenResp, err := exchangeAuthCodeForTokens(code, l.redirectURI, l.verifier)
	if err != nil {
		return fmt.Errorf("exchange authorization code: %w", err)
	}
	creds := OAuthCredentials{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Unix() + int64(tokenResp.ExpiresIn),
		AccountID:    extractAccountID(tokenResp.AccessToken),
	}
	if err := writeCredentials(creds); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	return nil
}

// Close releases the callback server.
func (l *OpenAILogin) Close() {
	if l.cancel != nil {
		l.cancel()
	}
}

// LoginOpenAI runs the full OAuth login from a terminal writer/stdin. Front-ends
// like the TUI use the OpenAILogin primitives directly instead.
func LoginOpenAI(w io.Writer) error {
	login, err := BeginOpenAILogin()
	if err != nil {
		return err
	}
	defer login.Close()

	fmt.Fprintf(w, "\nOpenAI Authentication\n")
	fmt.Fprintf(w, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Fprintf(w, "\nOpen this URL in your browser:\n   %s\n", login.AuthURL)
	fmt.Fprintf(w, "\nSign in with your ChatGPT Plus/Pro account.\n")
	if err := login.OpenBrowser(); err != nil {
		fmt.Fprintf(w, "\n(We tried to open your browser but couldn't. Open the URL above manually.)\n")
	}

	var code string
	if login.CallbackBound {
		fmt.Fprintf(w, "\nWaiting for authorization in browser...\n")
		code, err = login.WaitForCode(loginTimeout)
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintf(w, "\nAfter signing in, copy the FULL redirect URL and paste it below:\n> ")
		input, rerr := bufio.NewReader(os.Stdin).ReadString('\n')
		if rerr != nil {
			return fmt.Errorf("read input: %w", rerr)
		}
		code, err = login.CodeFromRedirectURL(input)
		if err != nil {
			return err
		}
	}

	if err := login.Complete(code); err != nil {
		return err
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
		return "", fmt.Errorf("not logged in: reconfigure in the TUI (press 'c') and choose OpenAI → Sign in")
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
