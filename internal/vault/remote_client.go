package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type RemoteClient struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
	RevokedAt  time.Time `json:"revoked_at,omitempty"`
}

func (db *DB) CreateRemoteClient(label, token string) (*RemoteClient, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "remote client"
	}
	hash := remoteTokenHash(token)
	if hash == "" {
		return nil, fmt.Errorf("token is required")
	}
	id := uuid.New().String()
	now := time.Now().Unix()
	_, err := db.conn.Exec(
		`INSERT INTO remote_clients (id, label, token_hash, created_at, last_used_at)
		 VALUES (?, ?, ?, ?, ?)`,
		id, label, hash, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create remote client: %w", err)
	}
	return &RemoteClient{ID: id, Label: label, CreatedAt: time.Unix(now, 0), LastUsedAt: time.Unix(now, 0)}, nil
}

func (db *DB) ValidateRemoteClientToken(token string) bool {
	hash := remoteTokenHash(token)
	if hash == "" {
		return false
	}
	var id string
	err := db.conn.QueryRow(`SELECT id FROM remote_clients WHERE token_hash = ? AND revoked_at = 0`, hash).Scan(&id)
	if err != nil {
		return false
	}
	_, _ = db.conn.Exec(`UPDATE remote_clients SET last_used_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return true
}

func (db *DB) RevokeRemoteClient(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("client id is required")
	}
	_, err := db.conn.Exec(`UPDATE remote_clients SET revoked_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

func remoteTokenHash(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
