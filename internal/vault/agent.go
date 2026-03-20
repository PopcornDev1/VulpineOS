package vault

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreateAgent creates a new persistent agent profile.
func (db *DB) CreateAgent(name, task, fingerprint string) (*Agent, error) {
	id := uuid.New().String()
	now := time.Now().Unix()

	_, err := db.conn.Exec(
		`INSERT INTO agents (id, name, task, fingerprint, status, created_at, last_active)
		 VALUES (?, ?, ?, ?, 'created', ?, ?)`,
		id, name, task, fingerprint, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	return &Agent{
		ID:          id,
		Name:        name,
		Task:        task,
		Fingerprint: fingerprint,
		Status:      "created",
		TotalTokens: 0,
		CreatedAt:   time.Unix(now, 0),
		LastActive:  time.Unix(now, 0),
		Metadata:    "{}",
	}, nil
}

// GetAgent retrieves an agent by ID.
func (db *DB) GetAgent(id string) (*Agent, error) {
	row := db.conn.QueryRow(
		`SELECT id, name, task, fingerprint, proxy_config, locale, timezone,
		        status, total_tokens, created_at, last_active, metadata
		 FROM agents WHERE id = ?`, id,
	)

	var a Agent
	var createdAt, lastActive int64
	err := row.Scan(&a.ID, &a.Name, &a.Task, &a.Fingerprint, &a.ProxyConfig,
		&a.Locale, &a.Timezone, &a.Status, &a.TotalTokens,
		&createdAt, &lastActive, &a.Metadata)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	a.CreatedAt = time.Unix(createdAt, 0)
	a.LastActive = time.Unix(lastActive, 0)
	return &a, nil
}

// ListAgents returns all agents ordered by last_active DESC.
func (db *DB) ListAgents() ([]Agent, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, task, fingerprint, proxy_config, locale, timezone,
		        status, total_tokens, created_at, last_active, metadata
		 FROM agents ORDER BY last_active DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		var createdAt, lastActive int64
		if err := rows.Scan(&a.ID, &a.Name, &a.Task, &a.Fingerprint, &a.ProxyConfig,
			&a.Locale, &a.Timezone, &a.Status, &a.TotalTokens,
			&createdAt, &lastActive, &a.Metadata); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		a.CreatedAt = time.Unix(createdAt, 0)
		a.LastActive = time.Unix(lastActive, 0)
		agents = append(agents, a)
	}
	return agents, nil
}

// ListAgentsByStatus returns agents filtered by status, ordered by last_active DESC.
func (db *DB) ListAgentsByStatus(status string) ([]Agent, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, task, fingerprint, proxy_config, locale, timezone,
		        status, total_tokens, created_at, last_active, metadata
		 FROM agents WHERE status = ? ORDER BY last_active DESC`, status,
	)
	if err != nil {
		return nil, fmt.Errorf("list agents by status: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		var createdAt, lastActive int64
		if err := rows.Scan(&a.ID, &a.Name, &a.Task, &a.Fingerprint, &a.ProxyConfig,
			&a.Locale, &a.Timezone, &a.Status, &a.TotalTokens,
			&createdAt, &lastActive, &a.Metadata); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		a.CreatedAt = time.Unix(createdAt, 0)
		a.LastActive = time.Unix(lastActive, 0)
		agents = append(agents, a)
	}
	return agents, nil
}

// UpdateAgentStatus updates the status and last_active timestamp of an agent.
func (db *DB) UpdateAgentStatus(id, status string) error {
	_, err := db.conn.Exec(
		`UPDATE agents SET status = ?, last_active = ? WHERE id = ?`,
		status, time.Now().Unix(), id,
	)
	return err
}

// UpdateAgentTokens sets the total_tokens for an agent.
func (db *DB) UpdateAgentTokens(id string, tokens int) error {
	_, err := db.conn.Exec(
		`UPDATE agents SET total_tokens = ? WHERE id = ?`,
		tokens, id,
	)
	return err
}

// DeleteAgent removes an agent and all associated messages (cascade).
func (db *DB) DeleteAgent(id string) error {
	_, err := db.conn.Exec(`DELETE FROM agents WHERE id = ?`, id)
	return err
}

// AppendMessage inserts a message into an agent's conversation history.
func (db *DB) AppendMessage(agentID, role, content string, tokens int) error {
	_, err := db.conn.Exec(
		`INSERT INTO agent_messages (agent_id, role, content, tokens, timestamp)
		 VALUES (?, ?, ?, ?, ?)`,
		agentID, role, content, tokens, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("append message: %w", err)
	}
	return nil
}

// GetMessages returns all messages for an agent ordered by timestamp.
func (db *DB) GetMessages(agentID string) ([]AgentMessage, error) {
	rows, err := db.conn.Query(
		`SELECT id, agent_id, role, content, tokens, timestamp
		 FROM agent_messages WHERE agent_id = ? ORDER BY timestamp ASC`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var messages []AgentMessage
	for rows.Next() {
		var m AgentMessage
		var ts int64
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Role, &m.Content, &m.Tokens, &ts); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.Timestamp = time.Unix(ts, 0)
		messages = append(messages, m)
	}
	return messages, nil
}

// GetRecentMessages returns the last N messages for an agent.
func (db *DB) GetRecentMessages(agentID string, limit int) ([]AgentMessage, error) {
	rows, err := db.conn.Query(
		`SELECT id, agent_id, role, content, tokens, timestamp
		 FROM agent_messages WHERE agent_id = ?
		 ORDER BY timestamp DESC LIMIT ?`, agentID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get recent messages: %w", err)
	}
	defer rows.Close()

	var messages []AgentMessage
	for rows.Next() {
		var m AgentMessage
		var ts int64
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Role, &m.Content, &m.Tokens, &ts); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		m.Timestamp = time.Unix(ts, 0)
		messages = append(messages, m)
	}

	// Reverse to chronological order
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}
