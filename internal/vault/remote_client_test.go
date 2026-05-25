package vault

import (
	"path/filepath"
	"testing"
)

func TestRemoteClientTokenValidationAndRevoke(t *testing.T) {
	db, err := OpenPath(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatalf("open vault: %v", err)
	}
	defer db.Close()

	client, err := db.CreateRemoteClient("laptop", "token-123")
	if err != nil {
		t.Fatalf("create remote client: %v", err)
	}
	if !db.ValidateRemoteClientToken("token-123") {
		t.Fatal("expected token to validate")
	}
	if db.ValidateRemoteClientToken("wrong") {
		t.Fatal("unexpected validation for wrong token")
	}
	if err := db.RevokeRemoteClient(client.ID); err != nil {
		t.Fatalf("revoke remote client: %v", err)
	}
	if db.ValidateRemoteClientToken("token-123") {
		t.Fatal("revoked token should not validate")
	}
}
