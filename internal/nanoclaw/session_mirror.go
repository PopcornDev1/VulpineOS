package nanoclaw

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	messageOpenRe  = regexp.MustCompile(`<message\s+[^>]*>`)
	messageCloseRe = regexp.MustCompile(`</message>`)
	internalBlockRe = regexp.MustCompile(`<internal>[\s\S]*?</internal>`)
)

func stripMessageTags(s string) string {
	s = messageOpenRe.ReplaceAllString(s, "")
	s = messageCloseRe.ReplaceAllString(s, "")
	s = internalBlockRe.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

const nanoclawMirrorPollInterval = 250 * time.Millisecond

type NanoClawSessionMirror struct {
	nanoclawDir       string
	agentID           string
	sessionLogPath    string
	agent             *Agent
	seen              map[string]struct{}
	loadedSeen        bool

	streamDir         string            // resolved per-session
	streamOffsets     map[string]int64  // sessionID → bytes read
	streamAccumulated map[string]string // sessionID → accumulated text
	streamDone        map[string]bool   // sessionID → stream completed
	lastStreamEmit    time.Time         // throttle: max 1 emit per 50ms
}

type nanoClawSessionRef struct {
	AgentGroupID string
	SessionID    string
}

type nanoClawDBMessage struct {
	ID        string
	Seq       sql.NullInt64
	Kind      string
	Timestamp string
	Content   string
}

type normalizedSessionLogEntry struct {
	Type              string                      `json:"type"`
	Source            string                      `json:"source,omitempty"`
	NanoClawMessageID string                      `json:"nanoclawMessageId,omitempty"`
	Timestamp         string                      `json:"timestamp,omitempty"`
	Message           normalizedSessionLogMessage `json:"message"`
}

type normalizedSessionLogMessage struct {
	Role       string                        `json:"role"`
	ToolName   string                        `json:"toolName,omitempty"`
	ToolCallID string                        `json:"toolCallId,omitempty"`
	IsError    bool                          `json:"isError,omitempty"`
	Content    []normalizedSessionLogContent `json:"content,omitempty"`
	Details    map[string]interface{}        `json:"details,omitempty"`
}

type normalizedSessionLogContent struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text,omitempty"`
	Thinking  string                 `json:"thinking,omitempty"`
	ID        string                 `json:"id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

func NewNanoClawSessionMirror(nanoclawDir, agentID, sessionLogPath string, agent *Agent) *NanoClawSessionMirror {
	return &NanoClawSessionMirror{
		nanoclawDir:       strings.TrimSpace(nanoclawDir),
		agentID:           strings.TrimSpace(agentID),
		sessionLogPath:    strings.TrimSpace(sessionLogPath),
		agent:             agent,
		seen:              make(map[string]struct{}),
		streamOffsets:     make(map[string]int64),
		streamAccumulated: make(map[string]string),
		streamDone:        make(map[string]bool),
	}
}

func (m *NanoClawSessionMirror) Run(ctx context.Context, completionAfter time.Time, onAssistant func()) {
	ticker := time.NewTicker(nanoclawMirrorPollInterval)
	defer ticker.Stop()
	completed := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			assistantSeen, _ := m.MirrorOnce(completionAfter)
			if assistantSeen && !completed {
				completed = true
				if onAssistant != nil {
					onAssistant()
				}
			}
		}
	}
}

func (m *NanoClawSessionMirror) MirrorOnce(completionAfter time.Time) (bool, error) {
	if m == nil || m.nanoclawDir == "" || m.agentID == "" || m.sessionLogPath == "" {
		return false, nil
	}
	if err := m.loadSeenFromExistingLog(); err != nil {
		return false, err
	}
	session, ok, err := m.resolveSession()
	if err != nil || !ok {
		return false, err
	}

	completed := false
	if err := m.mirrorInbound(session); err != nil {
		return false, err
	}
	if err := m.mirrorClaudeTranscripts(session); err != nil {
		return false, err
	}

	streamActivity := m.pollStreamFile(session)

	assistantSeen, err := m.mirrorOutbound(session, completionAfter)
	if err != nil {
		return false, err
	}
	if assistantSeen {
		completed = true
	}
	return streamActivity || completed, nil
}

func (m *NanoClawSessionMirror) loadSeenFromExistingLog() error {
	if m.loadedSeen {
		return nil
	}
	m.loadedSeen = true
	file, err := os.Open(m.sessionLogPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry struct {
			NanoClawMessageID string `json:"nanoclawMessageId"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil && entry.NanoClawMessageID != "" {
			m.seen[entry.NanoClawMessageID] = struct{}{}
		}
	}
	return scanner.Err()
}

func (m *NanoClawSessionMirror) resolveSession() (nanoClawSessionRef, bool, error) {
	db, err := sql.Open("sqlite", filepath.Join(m.nanoclawDir, "data", "v2.db"))
	if err != nil {
		return nanoClawSessionRef{}, false, fmt.Errorf("open nanoclaw database: %w", err)
	}
	defer db.Close()
	if ok, err := tableExists(db, "sessions"); err != nil || !ok {
		return nanoClawSessionRef{}, false, err
	}

	agentGroupID, err := selectedNanoClawAgentGroupID(db)
	if err != nil {
		return nanoClawSessionRef{}, false, err
	}
	platformID := vulpineAgentPlatformID(m.agentID)
	var ref nanoClawSessionRef
	err = db.QueryRow(`
SELECT s.agent_group_id, s.id
FROM sessions s
JOIN messaging_groups mg ON mg.id = s.messaging_group_id
WHERE s.agent_group_id = ?
  AND mg.channel_type = 'cli'
  AND mg.platform_id = ?
ORDER BY COALESCE(s.last_active, s.created_at) DESC, s.created_at DESC
LIMIT 1`, agentGroupID, platformID).Scan(&ref.AgentGroupID, &ref.SessionID)
	if err == sql.ErrNoRows {
		return nanoClawSessionRef{}, false, nil
	}
	if err != nil {
		return nanoClawSessionRef{}, false, fmt.Errorf("resolve nanoclaw session: %w", err)
	}
	return ref, true, nil
}

func (m *NanoClawSessionMirror) mirrorInbound(session nanoClawSessionRef) error {
	db, err := sql.Open("sqlite", filepath.Join(m.sessionDir(session), "inbound.db"))
	if err != nil {
		return fmt.Errorf("open nanoclaw inbound db: %w", err)
	}
	defer db.Close()
	if ok, err := tableExists(db, "messages_in"); err != nil || !ok {
		return err
	}
	rows, err := db.Query(`SELECT id, seq, kind, timestamp, content FROM messages_in ORDER BY seq ASC, timestamp ASC`)
	if err != nil {
		return fmt.Errorf("query nanoclaw inbound messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var msg nanoClawDBMessage
		if err := rows.Scan(&msg.ID, &msg.Seq, &msg.Kind, &msg.Timestamp, &msg.Content); err != nil {
			return fmt.Errorf("scan nanoclaw inbound message: %w", err)
		}
		seenID := "in:" + msg.ID
		if m.isSeen(seenID) || m.isSeen(msg.ID) {
			continue
		}
		text := nanoClawContentText(msg.Content)
		if text == "" {
			text = msg.Content
		}
		entry := normalizedSessionLogEntry{
			Type:              "message",
			Source:            "nanoclaw-db",
			NanoClawMessageID: seenID,
			Timestamp:         msg.Timestamp,
			Message: normalizedSessionLogMessage{
				Role:    "user",
				Content: []normalizedSessionLogContent{{Type: "text", Text: text}},
			},
		}
		if err := m.append(entry); err != nil {
			return err
		}
		m.markSeen(seenID)
		m.markSeen(msg.ID)
	}
	return rows.Err()
}

func (m *NanoClawSessionMirror) mirrorOutbound(session nanoClawSessionRef, completionAfter time.Time) (bool, error) {
	db, err := sql.Open("sqlite", filepath.Join(m.sessionDir(session), "outbound.db"))
	if err != nil {
		return false, fmt.Errorf("open nanoclaw outbound db: %w", err)
	}
	defer db.Close()
	if ok, err := tableExists(db, "messages_out"); err != nil || !ok {
		return false, err
	}
	rows, err := db.Query(`SELECT id, seq, kind, timestamp, content FROM messages_out ORDER BY seq ASC, timestamp ASC`)
	if err != nil {
		return false, fmt.Errorf("query nanoclaw outbound messages: %w", err)
	}
	defer rows.Close()
	completed := false
	for rows.Next() {
		var msg nanoClawDBMessage
		if err := rows.Scan(&msg.ID, &msg.Seq, &msg.Kind, &msg.Timestamp, &msg.Content); err != nil {
			return false, fmt.Errorf("scan nanoclaw outbound message: %w", err)
		}
		seenID := "out:" + msg.ID
		if m.isSeen(seenID) || m.isSeen(msg.ID) {
			continue
		}
		text := nanoClawContentText(msg.Content)
		if text == "" {
			text = msg.Content
		}
		entry := normalizedSessionLogEntry{
			Type:              "message",
			Source:            "nanoclaw-db",
			NanoClawMessageID: seenID,
			Timestamp:         msg.Timestamp,
			Message: normalizedSessionLogMessage{
				Role:    "assistant",
				Content: []normalizedSessionLogContent{{Type: "text", Text: text}},
			},
		}
		if err := m.append(entry); err != nil {
			return false, err
		}
		m.markSeen(seenID)
		m.markSeen(msg.ID)
		if messageAtOrAfter(msg.Timestamp, completionAfter) {
			completed = true
		}
	}
	return completed, rows.Err()
}

func (m *NanoClawSessionMirror) mirrorClaudeTranscripts(session nanoClawSessionRef) error {
	continuations, err := m.continuationIDs(session)
	if err != nil || len(continuations) == 0 {
		return err
	}
	for _, continuation := range continuations {
		paths, err := filepath.Glob(filepath.Join(m.nanoclawDir, "data", "v2-sessions", session.AgentGroupID, ".claude-shared", "projects", "*", continuation+".jsonl"))
		if err != nil {
			return err
		}
		for _, path := range paths {
			if err := m.mirrorClaudeTranscript(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *NanoClawSessionMirror) continuationIDs(session nanoClawSessionRef) ([]string, error) {
	db, err := sql.Open("sqlite", filepath.Join(m.sessionDir(session), "outbound.db"))
	if err != nil {
		return nil, fmt.Errorf("open nanoclaw outbound db: %w", err)
	}
	defer db.Close()
	if ok, err := tableExists(db, "session_state"); err != nil || !ok {
		return nil, err
	}
	rows, err := db.Query(`SELECT value FROM session_state WHERE key LIKE 'continuation:%'`)
	if err != nil {
		return nil, fmt.Errorf("query nanoclaw continuation state: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan nanoclaw continuation: %w", err)
		}
		id = strings.TrimSpace(id)
		if id != "" && !strings.ContainsAny(id, `/\`) {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

func (m *NanoClawSessionMirror) mirrorClaudeTranscript(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	offset := int64(0)
	for {
		line, err := reader.ReadString('\n')
		nextOffset := offset + int64(len(line))
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			seenID := fmt.Sprintf("transcript:%s:%d", path, offset)
			if !m.isSeen(seenID) {
				entries := normalizeClaudeTranscriptLine(trimmed)
				for i := range entries {
					entries[i].Source = "nanoclaw-transcript"
					entries[i].NanoClawMessageID = seenID
					if err := m.append(entries[i]); err != nil {
						return err
					}
				}
				m.markSeen(seenID)
			}
		}
		offset = nextOffset
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func normalizeClaudeTranscriptLine(line string) []normalizedSessionLogEntry {
	var event struct {
		Type    string `json:"type"`
		Message *struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil || event.Message == nil {
		return nil
	}
	switch event.Message.Role {
	case "assistant":
		var content []normalizedSessionLogContent
		for _, raw := range event.Message.Content {
			item := parseClaudeContentItem(raw)
			switch item.Type {
			case "thinking":
				if strings.TrimSpace(item.Thinking) != "" {
					content = append(content, normalizedSessionLogContent{Type: "thinking", Thinking: strings.TrimSpace(item.Thinking)})
				}
			case "tool_use":
				content = append(content, normalizedSessionLogContent{Type: "toolCall", ID: item.ID, Name: item.Name, Arguments: item.Input})
			}
		}
		if len(content) == 0 {
			return nil
		}
		return []normalizedSessionLogEntry{{
			Type:    "message",
			Message: normalizedSessionLogMessage{Role: "assistant", Content: content},
		}}
	case "user":
		var entries []normalizedSessionLogEntry
		for _, raw := range event.Message.Content {
			item := parseClaudeContentItem(raw)
			if item.Type != "tool_result" {
				continue
			}
			text := item.ContentText()
			details := map[string]interface{}{}
			if text != "" {
				var payload map[string]interface{}
				if json.Unmarshal([]byte(text), &payload) == nil {
					details = payload
				}
			}
			entries = append(entries, normalizedSessionLogEntry{
				Type: "message",
				Message: normalizedSessionLogMessage{
					Role:       "toolResult",
					ToolCallID: item.ToolUseID,
					IsError:    item.IsError,
					Content:    []normalizedSessionLogContent{{Type: "text", Text: text}},
					Details:    details,
				},
			})
		}
		return entries
	default:
		return nil
	}
}

type claudeContentItem struct {
	Type      string                 `json:"type"`
	Text      string                 `json:"text"`
	Thinking  string                 `json:"thinking"`
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Input     map[string]interface{} `json:"input"`
	ToolUseID string                 `json:"tool_use_id"`
	Content   interface{}            `json:"content"`
	IsError   bool                   `json:"is_error"`
}

func parseClaudeContentItem(raw json.RawMessage) claudeContentItem {
	var item claudeContentItem
	_ = json.Unmarshal(raw, &item)
	return item
}

func (i claudeContentItem) ContentText() string {
	switch v := i.Content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, item := range v {
			obj, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if text, _ := obj["text"].(string); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case nil:
		return i.Text
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func (m *NanoClawSessionMirror) append(entry normalizedSessionLogEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.sessionLogPath), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(m.sessionLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if m.agent != nil {
		m.agent.handleSessionLogLine(string(data))
	}
	return nil
}

func (m *NanoClawSessionMirror) sessionDir(session nanoClawSessionRef) string {
	return filepath.Join(m.nanoclawDir, "data", "v2-sessions", session.AgentGroupID, session.SessionID)
}

func (m *NanoClawSessionMirror) streamFilePath(session nanoClawSessionRef) string {
	return filepath.Join(m.sessionDir(session), "stream.jsonl")
}

func (m *NanoClawSessionMirror) pollStreamFile(session nanoClawSessionRef) bool {
	path := m.streamFilePath(session)
	if path == "" {
		return false
	}

	sid := session.SessionID
	if m.streamDone[sid] {
		return false
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		return false
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return false
	}
	offset := m.streamOffsets[sid]
	if fi.Size() < offset {
		offset = 0
		m.streamOffsets[sid] = 0
		m.streamAccumulated[sid] = ""
	}
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return false
		}
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	newContent := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		offset += int64(len(line)) + 1

		var entry struct {
			T    string `json:"t"`
			Done string `json:"done"`
			Tool string `json:"tool"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if entry.T != "" {
			m.streamAccumulated[sid] += entry.T
			newContent = true
		}
		if entry.Done != "" {
			m.streamAccumulated[sid] = stripMessageTags(entry.Done)
			m.streamDone[sid] = true
			newContent = true
		}
	}

	m.streamOffsets[sid] = offset

	if !newContent {
		return false
	}

	accumulated := m.streamAccumulated[sid]
	if accumulated == "" {
		return false
	}

	if time.Since(m.lastStreamEmit) < 50*time.Millisecond {
		return m.streamDone[sid]
	}
	m.lastStreamEmit = time.Now()

	if m.streamDone[sid] {
		_ = os.Remove(path)
		delete(m.streamOffsets, sid)
		delete(m.streamAccumulated, sid)
		return true
	}

	m.agent.handleStreamContent(stripMessageTags(accumulated), true)
	return true
}

func (m *NanoClawSessionMirror) isSeen(id string) bool {
	_, ok := m.seen[id]
	return ok
}

func (m *NanoClawSessionMirror) markSeen(id string) {
	if strings.TrimSpace(id) != "" {
		m.seen[id] = struct{}{}
	}
}

func nanoClawContentText(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return content
	}
	if text, _ := payload["text"].(string); text != "" {
		return text
	}
	if fallback, _ := payload["fallbackText"].(string); fallback != "" {
		return fallback
	}
	return content
}

func messageAtOrAfter(raw string, threshold time.Time) bool {
	if threshold.IsZero() {
		return true
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		t, err := time.Parse(layout, raw)
		if err == nil {
			return !t.Before(threshold)
		}
	}
	return false
}
