package vault

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreateNomadSession creates an ephemeral agent session.
func (db *DB) CreateNomadSession(templateID, fingerprint string) (*NomadSession, error) {
	id := uuid.New().String()
	now := time.Now().Unix()

	_, err := db.conn.Exec(
		`INSERT INTO nomad_sessions (id, template_id, fingerprint, started_at, status)
		 VALUES (?, ?, ?, ?, 'active')`,
		id, templateID, fingerprint, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create nomad session: %w", err)
	}

	return &NomadSession{
		ID:          id,
		TemplateID:  templateID,
		Fingerprint: fingerprint,
		StartedAt:   time.Unix(now, 0),
		Status:      "active",
	}, nil
}

// CompleteNomadSession marks a session as completed with result.
func (db *DB) CompleteNomadSession(id, status, result string) error {
	_, err := db.conn.Exec(
		`UPDATE nomad_sessions SET status = ?, result = ?, completed_at = ? WHERE id = ?`,
		status, result, time.Now().Unix(), id,
	)
	return err
}

// GetNomadSession retrieves a nomad session.
func (db *DB) GetNomadSession(id string) (*NomadSession, error) {
	row := db.conn.QueryRow(
		`SELECT id, template_id, fingerprint, started_at, completed_at, status, result
		 FROM nomad_sessions WHERE id = ?`, id,
	)

	var ns NomadSession
	var startedAt, completedAt int64
	err := row.Scan(&ns.ID, &ns.TemplateID, &ns.Fingerprint,
		&startedAt, &completedAt, &ns.Status, &ns.Result)
	if err != nil {
		return nil, fmt.Errorf("get nomad session: %w", err)
	}
	ns.StartedAt = time.Unix(startedAt, 0)
	ns.CompletedAt = time.Unix(completedAt, 0)
	return &ns, nil
}

// ListActiveNomadSessions returns all active nomad sessions.
func (db *DB) ListActiveNomadSessions() ([]NomadSession, error) {
	rows, err := db.conn.Query(
		`SELECT id, template_id, fingerprint, started_at, completed_at, status, result
		 FROM nomad_sessions WHERE status = 'active' ORDER BY started_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []NomadSession
	for rows.Next() {
		var ns NomadSession
		var startedAt, completedAt int64
		if err := rows.Scan(&ns.ID, &ns.TemplateID, &ns.Fingerprint,
			&startedAt, &completedAt, &ns.Status, &ns.Result); err != nil {
			return nil, err
		}
		ns.StartedAt = time.Unix(startedAt, 0)
		ns.CompletedAt = time.Unix(completedAt, 0)
		sessions = append(sessions, ns)
	}
	return sessions, nil
}

// CleanupCompletedSessions removes completed sessions older than the given duration.
func (db *DB) CleanupCompletedSessions(olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan).Unix()
	result, err := db.conn.Exec(
		`DELETE FROM nomad_sessions WHERE status != 'active' AND completed_at < ? AND completed_at > 0`,
		cutoff,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
