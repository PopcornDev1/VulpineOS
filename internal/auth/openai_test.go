package auth

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpenAICredentialPath(t *testing.T) {
	path := OpenAICredentialPath()
	if !strings.Contains(path, ".vulpineos/credentials/openai-oauth.json") {
		t.Errorf("unexpected path: %s", path)
	}
}

func TestWriteReadCredentials(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	creds := OAuthCredentials{
		AccessToken:  "test-access",
		RefreshToken: "test-refresh",
		ExpiresAt:    time.Now().Unix() + 3600,
		AccountID:    "user-test",
	}
	if err := writeCredentials(creds); err != nil {
		t.Fatal(err)
	}

	got, err := readCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "test-access" {
		t.Errorf("AccessToken = %q, want %q", got.AccessToken, "test-access")
	}
	if got.RefreshToken != "test-refresh" {
		t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, "test-refresh")
	}
	if got.AccountID != "user-test" {
		t.Errorf("AccountID = %q, want %q", got.AccountID, "user-test")
	}
}

func TestIsOAuthTokenExpired(t *testing.T) {
	tests := []struct {
		name   string
		creds  OAuthCredentials
		buffer time.Duration
		want   bool
	}{
		{"already expired", OAuthCredentials{ExpiresAt: time.Now().Unix() - 100}, 0, true},
		{"within buffer", OAuthCredentials{ExpiresAt: time.Now().Unix() + 120}, 5 * time.Minute, true},
		{"far from expiry", OAuthCredentials{ExpiresAt: time.Now().Unix() + 3600}, 5 * time.Minute, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsOAuthTokenExpired(&tt.creds, tt.buffer); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractAccountID(t *testing.T) {
	payload := `{"sub":"user-abc123"}`
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
	jwt := "header." + encoded + ".signature"

	id := extractAccountID(jwt)
	if id != "user-abc123" {
		t.Errorf("got %q, want %q", id, "user-abc123")
	}
}

func TestExtractAccountIDInvalid(t *testing.T) {
	if id := extractAccountID("invalid"); id != "" {
		t.Errorf("expected empty, got %q", id)
	}
	if id := extractAccountID("a.b"); id != "" {
		t.Errorf("expected empty for short JWT, got %q", id)
	}
}

func TestLogoutRemovesFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	creds := OAuthCredentials{AccessToken: "test", RefreshToken: "test", ExpiresAt: time.Now().Unix() + 3600}
	if err := writeCredentials(creds); err != nil {
		t.Fatal(err)
	}

	if err := LogoutOpenAI(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(OpenAICredentialPath()); !os.IsNotExist(err) {
		t.Error("expected credential file to be removed")
	}
}
