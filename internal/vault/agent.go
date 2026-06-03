package vault

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// NewID returns a fresh, unique identifier (UUIDv4). Callers should mint this
// BEFORE generating a fingerprint and use it as BOTH the row id and the
// fingerprint seed, so each agent gets a guaranteed-unique, stable fingerprint
// even when display names collide (names have no uniqueness constraint).
func NewID() string { return uuid.New().String() }

// CreateAgent creates a new persistent agent profile with an auto-generated id.
// Prefer CreateAgentWithID + NewID so the fingerprint can be seeded by the id.
func (db *DB) CreateAgent(name, task, fingerprint string) (*Agent, error) {
	return db.CreateAgentWithID(uuid.New().String(), name, task, fingerprint)
}

// CreateAgentWithID creates an agent with a caller-supplied id (from NewID),
// which should also have been used as the fingerprint seed for per-agent uniqueness.
func (db *DB) CreateAgentWithID(id, name, task, fingerprint string) (*Agent, error) {
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
		ID:             id,
		Name:           name,
		Task:           task,
		Fingerprint:    fingerprint,
		Status:         "created",
		TotalTokens:    0,
		CreatedAt:      time.Unix(now, 0),
		LastActive:     time.Unix(now, 0),
		LastSelectedAt: time.Time{},
		Metadata:       "{}",
	}, nil
}

// GetAgent retrieves an agent by ID.
func (db *DB) GetAgent(id string) (*Agent, error) {
	row := db.conn.QueryRow(
		`SELECT id, name, task, fingerprint, proxy_config, locale, timezone,
		        status, total_tokens, created_at, last_active, last_selected_at, metadata
		 FROM agents WHERE id = ?`, id,
	)

	var a Agent
	var createdAt, lastActive, lastSelectedAt int64
	err := row.Scan(&a.ID, &a.Name, &a.Task, &a.Fingerprint, &a.ProxyConfig,
		&a.Locale, &a.Timezone, &a.Status, &a.TotalTokens,
		&createdAt, &lastActive, &lastSelectedAt, &a.Metadata)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	a.CreatedAt = time.Unix(createdAt, 0)
	a.LastActive = time.Unix(lastActive, 0)
	if lastSelectedAt > 0 {
		a.LastSelectedAt = time.Unix(lastSelectedAt, 0)
	}
	return &a, nil
}

// ListAgents returns all agents ordered by last_active DESC.
func (db *DB) ListAgents() ([]Agent, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, task, fingerprint, proxy_config, locale, timezone,
		        status, total_tokens, created_at, last_active, last_selected_at, metadata
		 FROM agents ORDER BY last_active DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		var createdAt, lastActive, lastSelectedAt int64
		if err := rows.Scan(&a.ID, &a.Name, &a.Task, &a.Fingerprint, &a.ProxyConfig,
			&a.Locale, &a.Timezone, &a.Status, &a.TotalTokens,
			&createdAt, &lastActive, &lastSelectedAt, &a.Metadata); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		a.CreatedAt = time.Unix(createdAt, 0)
		a.LastActive = time.Unix(lastActive, 0)
		if lastSelectedAt > 0 {
			a.LastSelectedAt = time.Unix(lastSelectedAt, 0)
		}
		agents = append(agents, a)
	}
	return agents, nil
}

// ListAgentsByStatus returns agents filtered by status, ordered by last_active DESC.
func (db *DB) ListAgentsByStatus(status string) ([]Agent, error) {
	rows, err := db.conn.Query(
		`SELECT id, name, task, fingerprint, proxy_config, locale, timezone,
		        status, total_tokens, created_at, last_active, last_selected_at, metadata
		 FROM agents WHERE status = ? ORDER BY last_active DESC`, status,
	)
	if err != nil {
		return nil, fmt.Errorf("list agents by status: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		var createdAt, lastActive, lastSelectedAt int64
		if err := rows.Scan(&a.ID, &a.Name, &a.Task, &a.Fingerprint, &a.ProxyConfig,
			&a.Locale, &a.Timezone, &a.Status, &a.TotalTokens,
			&createdAt, &lastActive, &lastSelectedAt, &a.Metadata); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		a.CreatedAt = time.Unix(createdAt, 0)
		a.LastActive = time.Unix(lastActive, 0)
		if lastSelectedAt > 0 {
			a.LastSelectedAt = time.Unix(lastSelectedAt, 0)
		}
		agents = append(agents, a)
	}
	return agents, nil
}

// MarkAgentSelected records that the user just switched to this agent.
// Pass time.Time{} to clear the timestamp.
func (db *DB) MarkAgentSelected(id string, t time.Time) error {
	var ts int64
	if !t.IsZero() {
		ts = t.Unix()
	}
	_, err := db.conn.Exec(
		`UPDATE agents SET last_selected_at = ? WHERE id = ?`,
		ts, id,
	)
	return err
}

// UpdateAgentStatus updates the status and last_active timestamp of an agent.
func (db *DB) UpdateAgentStatus(id, status string) error {
	_, err := db.conn.Exec(
		`UPDATE agents SET status = ?, last_active = ? WHERE id = ?`,
		status, time.Now().Unix(), id,
	)
	return err
}

// UpdateAgentName updates the name of an agent.
func (db *DB) UpdateAgentName(id, name string) error {
	_, err := db.conn.Exec(
		`UPDATE agents SET name = ?, last_active = ? WHERE id = ?`,
		name, time.Now().Unix(), id,
	)
	return err
}

// ReconcileNonTerminalAgents marks persisted agents left in an in-flight
// state as interrupted after a process restart. Paused agents are resumable and
// must survive restart unchanged.
func (db *DB) ReconcileNonTerminalAgents(status string) error {
	if status == "" {
		status = "interrupted"
	}
	_, err := db.conn.Exec(
		`UPDATE agents
		 SET status = ?, last_active = ?
		 WHERE status NOT IN ('paused', 'completed', 'error', 'failed', 'interrupted')`,
		status, time.Now().Unix(),
	)
	return err
}

// UpdateAgentFingerprint updates the fingerprint JSON for an agent.
func (db *DB) UpdateAgentFingerprint(id, fingerprint string) error {
	_, err := db.conn.Exec(
		`UPDATE agents SET fingerprint = ? WHERE id = ?`,
		fingerprint, id,
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

// UpdateAgentMetadata updates the metadata JSON for an agent.
func (db *DB) UpdateAgentMetadata(id, metadata string) error {
	if metadata == "" {
		metadata = "{}"
	}
	_, err := db.conn.Exec(
		`UPDATE agents SET metadata = ? WHERE id = ?`,
		metadata, id,
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
	return db.AppendMessageWithDisplay(agentID, role, content, "", tokens)
}

// RenderSessionLog builds a JSON-lines transcript of an agent's conversation
// from the vault — one JSON object per message. This is the native runtime's
// source for the operator-facing raw session log (the native runtime persists
// turns to the vault rather than to an on-disk session file). Returns the
// rendered bytes and whether any messages exist.
func (db *DB) RenderSessionLog(agentID string) ([]byte, bool, error) {
	messages, err := db.GetMessages(agentID)
	if err != nil {
		return nil, false, err
	}
	if len(messages) == 0 {
		return nil, false, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, m := range messages {
		content := m.Content
		if content == "" {
			content = m.DisplayContent
		}
		line := map[string]interface{}{
			"role":      m.Role,
			"content":   content,
			"tokens":    m.Tokens,
			"timestamp": m.Timestamp.UTC().Format(time.RFC3339),
		}
		if err := enc.Encode(line); err != nil {
			return nil, false, err
		}
	}
	return buf.Bytes(), true, nil
}

// AppendMessageWithDisplay inserts a message with optional operator-facing display text.
func (db *DB) AppendMessageWithDisplay(agentID, role, content, displayContent string, tokens int) error {
	_, err := db.conn.Exec(
		`INSERT INTO agent_messages (agent_id, role, content, display_content, tokens, timestamp)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		agentID, role, content, displayContent, tokens, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("append message: %w", err)
	}
	return nil
}

// GetMessages returns all messages for an agent ordered by timestamp.
func (db *DB) GetMessages(agentID string) ([]AgentMessage, error) {
	rows, err := db.conn.Query(
		`SELECT id, agent_id, role, content, COALESCE(display_content, ''), tokens, timestamp
		 FROM agent_messages WHERE agent_id = ? ORDER BY timestamp ASC, id ASC`, agentID,
	)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	defer rows.Close()

	var messages []AgentMessage
	for rows.Next() {
		var m AgentMessage
		var ts int64
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Role, &m.Content, &m.DisplayContent, &m.Tokens, &ts); err != nil {
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
		`SELECT id, agent_id, role, content, COALESCE(display_content, ''), tokens, timestamp
		 FROM agent_messages WHERE agent_id = ?
		 ORDER BY timestamp DESC, id DESC LIMIT ?`, agentID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("get recent messages: %w", err)
	}
	defer rows.Close()

	var messages []AgentMessage
	for rows.Next() {
		var m AgentMessage
		var ts int64
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Role, &m.Content, &m.DisplayContent, &m.Tokens, &ts); err != nil {
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

// ParseAgentMetadata parses the JSON metadata for an agent.
func ParseAgentMetadata(raw string) (AgentMetadata, error) {
	if raw == "" {
		return AgentMetadata{}, nil
	}
	var meta AgentMetadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return AgentMetadata{}, err
	}
	return meta, nil
}

// MarshalAgentMetadata encodes agent metadata to JSON.
func MarshalAgentMetadata(meta AgentMetadata) string {
	data, err := json.Marshal(meta)
	if err != nil {
		return "{}"
	}
	return string(data)
}
