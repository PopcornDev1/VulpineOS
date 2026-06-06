package vault

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RoleSeed stores a reusable role identity for sub-agents.
type RoleSeed struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Content string    `json:"content"`
	Tags    string    `json:"tags"` // JSON array of tags
	Created time.Time `json:"created"`
	Used    int       `json:"used"`
}

// CreateRoleSeed saves a new role seed.
func (db *DB) CreateRoleSeed(name, content string, tags []string) (*RoleSeed, error) {
	id := uuid.New().String()
	now := time.Now().Unix()

	tagsJSON := marshalStringSlice(tags)

	_, err := db.conn.Exec(
		`INSERT INTO role_seeds (id, name, content, tags, created, used) VALUES (?, ?, ?, ?, ?, 0)`,
		id, name, content, tagsJSON, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create role seed: %w", err)
	}

	return &RoleSeed{
		ID:      id,
		Name:    name,
		Content: content,
		Tags:    tagsJSON,
		Created: time.Unix(now, 0),
	}, nil
}

// GetRoleSeedByName retrieves a role seed by its unique name.
func (db *DB) GetRoleSeedByName(name string) (*RoleSeed, error) {
	row := db.conn.QueryRow(
		`SELECT id, name, content, tags, created, used FROM role_seeds WHERE name = ?`, name,
	)
	var s RoleSeed
	var created int64
	err := row.Scan(&s.ID, &s.Name, &s.Content, &s.Tags, &created, &s.Used)
	if err != nil {
		return nil, fmt.Errorf("get role seed by name: %w", err)
	}
	s.Created = time.Unix(created, 0)
	return &s, nil
}

// ListRoleSeeds returns all stored role seeds.
func (db *DB) ListRoleSeeds() ([]RoleSeed, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, content, tags, created, used FROM role_seeds ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list role seeds: %w", err)
	}
	defer rows.Close()

	var seeds []RoleSeed
	for rows.Next() {
		var s RoleSeed
		var created int64
		if err := rows.Scan(&s.ID, &s.Name, &s.Content, &s.Tags, &created, &s.Used); err != nil {
			return nil, err
		}
		s.Created = time.Unix(created, 0)
		seeds = append(seeds, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return seeds, nil
}

// FindRoleSeeds searches role seeds by name or tag match (LIKE query).
func (db *DB) FindRoleSeeds(query string) ([]RoleSeed, error) {
	like := "%" + strings.ToLower(query) + "%"
	rows, err := db.conn.Query(
		`SELECT id, name, content, tags, created, used FROM role_seeds
		 WHERE LOWER(name) LIKE ? OR LOWER(tags) LIKE ? ORDER BY used DESC`,
		like, like,
	)
	if err != nil {
		return nil, fmt.Errorf("find role seeds: %w", err)
	}
	defer rows.Close()

	var seeds []RoleSeed
	for rows.Next() {
		var s RoleSeed
		var created int64
		if err := rows.Scan(&s.ID, &s.Name, &s.Content, &s.Tags, &created, &s.Used); err != nil {
			return nil, err
		}
		s.Created = time.Unix(created, 0)
		seeds = append(seeds, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return seeds, nil
}

// IncrementRoleSeedUse increments the usage counter for a role seed.
func (db *DB) IncrementRoleSeedUse(id string) error {
	_, err := db.conn.Exec(`UPDATE role_seeds SET used = used + 1 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("increment role seed use: %w", err)
	}
	return nil
}

// DeleteRoleSeed removes a role seed by ID.
func (db *DB) DeleteRoleSeed(id string) error {
	_, err := db.conn.Exec(`DELETE FROM role_seeds WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete role seed: %w", err)
	}
	return nil
}

// marshalStringSlice marshals a []string to a JSON string without importing
// encoding/json, keeping the vault package's dependency footprint minimal.
func marshalStringSlice(s []string) string {
	if len(s) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range s {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		b.WriteString(v)
		b.WriteByte('"')
	}
	b.WriteByte(']')
	return b.String()
}
