package vault

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreateTemplate creates a new agent template.
func (db *DB) CreateTemplate(name, description, sop, interactionMode, allowedDomains, constraints string) (*Template, error) {
	id := uuid.New().String()
	now := time.Now().Unix()

	if interactionMode == "" {
		interactionMode = "full"
	}
	if allowedDomains == "" {
		allowedDomains = "[]"
	}
	if constraints == "" {
		constraints = "{}"
	}

	_, err := db.conn.Exec(
		`INSERT INTO templates (id, name, description, sop, interaction_mode, allowed_domains, constraints, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, name, description, sop, interactionMode, allowedDomains, constraints, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create template: %w", err)
	}

	return &Template{
		ID:              id,
		Name:            name,
		Description:     description,
		SOP:             sop,
		InteractionMode: interactionMode,
		AllowedDomains:  allowedDomains,
		Constraints:     constraints,
		CreatedAt:       time.Unix(now, 0),
		UpdatedAt:       time.Unix(now, 0),
	}, nil
}

// GetTemplate retrieves a template by ID.
func (db *DB) GetTemplate(id string) (*Template, error) {
	row := db.conn.QueryRow(
		`SELECT id, name, description, sop, interaction_mode, allowed_domains, constraints, created_at, updated_at
		 FROM templates WHERE id = ?`, id,
	)

	var t Template
	var createdAt, updatedAt int64
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.SOP,
		&t.InteractionMode, &t.AllowedDomains, &t.Constraints,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get template: %w", err)
	}
	t.CreatedAt = time.Unix(createdAt, 0)
	t.UpdatedAt = time.Unix(updatedAt, 0)
	return &t, nil
}

// GetTemplateByName retrieves a template by name.
func (db *DB) GetTemplateByName(name string) (*Template, error) {
	row := db.conn.QueryRow(
		`SELECT id, name, description, sop, interaction_mode, allowed_domains, constraints, created_at, updated_at
		 FROM templates WHERE name = ?`, name,
	)

	var t Template
	var createdAt, updatedAt int64
	err := row.Scan(&t.ID, &t.Name, &t.Description, &t.SOP,
		&t.InteractionMode, &t.AllowedDomains, &t.Constraints,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("get template by name: %w", err)
	}
	t.CreatedAt = time.Unix(createdAt, 0)
	t.UpdatedAt = time.Unix(updatedAt, 0)
	return &t, nil
}

// ListTemplates returns all templates.
func (db *DB) ListTemplates() ([]Template, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, description, sop, interaction_mode, allowed_domains, constraints, created_at, updated_at
		 FROM templates ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var templates []Template
	for rows.Next() {
		var t Template
		var createdAt, updatedAt int64
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.SOP,
			&t.InteractionMode, &t.AllowedDomains, &t.Constraints,
			&createdAt, &updatedAt); err != nil {
			return nil, err
		}
		t.CreatedAt = time.Unix(createdAt, 0)
		t.UpdatedAt = time.Unix(updatedAt, 0)
		templates = append(templates, t)
	}
	return templates, nil
}

// UpdateTemplate updates a template's SOP and settings.
func (db *DB) UpdateTemplate(id, sop, interactionMode, allowedDomains, constraints string) error {
	_, err := db.conn.Exec(
		`UPDATE templates SET sop = ?, interaction_mode = ?, allowed_domains = ?, constraints = ?, updated_at = ?
		 WHERE id = ?`,
		sop, interactionMode, allowedDomains, constraints, time.Now().Unix(), id,
	)
	return err
}

// DeleteTemplate removes a template.
func (db *DB) DeleteTemplate(id string) error {
	_, err := db.conn.Exec(`DELETE FROM templates WHERE id = ?`, id)
	return err
}
