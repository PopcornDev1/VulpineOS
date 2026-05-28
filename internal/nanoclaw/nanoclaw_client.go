package nanoclaw

import (
	"bufio"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"vulpineos/internal/config"
)

const nanoclawFirstResponseTimeout = 10 * time.Minute
const defaultNanoClawAgentGroupID = "vulpineos-main"

type NanoclawClient struct {
	socketPath string
}

type nanoclawDeliveryAddress struct {
	ChannelType string      `json:"channelType"`
	PlatformID  string      `json:"platformId"`
	ThreadID    interface{} `json:"threadId"`
}

type nanoclawSocketPayload struct {
	Text     string                   `json:"text"`
	To       *nanoclawDeliveryAddress `json:"to,omitempty"`
	ReplyTo  *nanoclawDeliveryAddress `json:"reply_to,omitempty"`
	Sender   string                   `json:"sender,omitempty"`
	SenderID string                   `json:"senderId,omitempty"`
}

func NewNanoclawClient(nanoclawDir string) *NanoclawClient {
	return &NanoclawClient{
		socketPath: filepath.Join(nanoclawDir, "data", "cli.sock"),
	}
}

func (c *NanoclawClient) IsRunning() bool {
	conn, err := net.DialTimeout("unix", c.socketPath, 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (c *NanoclawClient) SendMessage(message string, onChunk func(string, bool)) error {
	return c.sendPayload(nanoclawSocketPayload{Text: message}, onChunk)
}

func (c *NanoclawClient) SendAgentMessage(agentID, message string, onChunk func(string, bool)) error {
	platformID := vulpineAgentPlatformID(agentID)
	return c.sendPayload(nanoclawSocketPayload{
		Text: vulpineRuntimeMessage(message),
		To: &nanoclawDeliveryAddress{
			ChannelType: "cli",
			PlatformID:  platformID,
			ThreadID:    nil,
		},
		ReplyTo: &nanoclawDeliveryAddress{
			ChannelType: "cli",
			PlatformID:  platformID,
			ThreadID:    nil,
		},
		Sender:   "vulpine",
		SenderID: "vulpine:" + agentID,
	}, onChunk)
}

func (c *NanoclawClient) EnqueueAgentMessage(agentID, message string) error {
	platformID := vulpineAgentPlatformID(agentID)
	return c.writePayload(nanoclawSocketPayload{
		Text: vulpineRuntimeMessage(message),
		To: &nanoclawDeliveryAddress{
			ChannelType: "cli",
			PlatformID:  platformID,
			ThreadID:    nil,
		},
		ReplyTo: &nanoclawDeliveryAddress{
			ChannelType: "cli",
			PlatformID:  platformID,
			ThreadID:    nil,
		},
		Sender:   "vulpine",
		SenderID: "vulpine:" + agentID,
	})
}

func vulpineRuntimeMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Help with the assigned task."
	}
	return `VulpineOS runtime instructions:
- Complete the user message below directly.
- If the user asks for an exact reply or exact wording, perform any required action first, then send that exact reply and stop.
- For browser tasks, once the page state proves the requested action succeeded, do not keep inspecting or retrying. Send the requested final reply.
- If a browser/tool action fails or times out, report the exact failure instead of claiming success.
- Do not claim you already delivered a report, diagnosis, or result unless it appears in the visible chat history included below.

User message:
` + message
}

func (c *NanoclawClient) sendPayload(payload nanoclawSocketPayload, onChunk func(string, bool)) error {
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to nanoclaw CLI socket: %w", err)
	}
	defer conn.Close()

	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}
	_, err = conn.Write(append(encoded, '\n'))
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(nanoclawFirstResponseTimeout))

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return fmt.Errorf("timed out waiting for nanoclaw response")
			}
			if err != io.EOF {
				return fmt.Errorf("failed to read nanoclaw response: %w", err)
			}
			break
		}

		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		if text, ok := msg["text"].(string); ok && text != "" {
			onChunk(text, false)
			onChunk("", true)
			return nil
		}
	}

	return nil
}

func (c *NanoclawClient) writePayload(payload nanoclawSocketPayload) error {
	conn, err := net.DialTimeout("unix", c.socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to nanoclaw CLI socket: %w", err)
	}
	defer conn.Close()

	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to encode message: %w", err)
	}
	if _, err := conn.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	return nil
}

func vulpineAgentPlatformID(agentID string) string {
	return "vulpine:" + strings.TrimSpace(agentID)
}

func ensureVulpineAgentRoute(nanoclawDir, agentID string) error {
	dbPath := filepath.Join(nanoclawDir, "data", "v2.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open nanoclaw database: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable nanoclaw foreign keys: %w", err)
	}

	agentGroupID, err := selectedNanoClawAgentGroupID(db)
	if err != nil {
		return err
	}
	if err := ensureNanoClawContainerConfig(db, agentGroupID, "", ""); err != nil {
		return err
	}
	hasAgentDestinations, err := tableExists(db, "agent_destinations")
	if err != nil {
		return fmt.Errorf("check nanoclaw agent_destinations table: %w", err)
	}

	platformID := vulpineAgentPlatformID(agentID)
	messagingGroupID := vulpineMessagingGroupID(agentID)
	wiringID := vulpineWiringID(agentID, agentGroupID)
	now := time.Now().UTC().Format(time.RFC3339)

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin nanoclaw route transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
INSERT INTO messaging_groups (id, channel_type, platform_id, name, is_group, unknown_sender_policy, created_at)
VALUES (?, 'cli', ?, ?, 0, 'public', ?)
ON CONFLICT(channel_type, platform_id) DO UPDATE SET
  name = excluded.name,
  unknown_sender_policy = excluded.unknown_sender_policy`, messagingGroupID, platformID, "Vulpine "+agentID, now)
	if err != nil {
		return fmt.Errorf("ensure nanoclaw messaging group: %w", err)
	}

	var existingMessagingGroupID string
	if err := tx.QueryRow(`SELECT id FROM messaging_groups WHERE channel_type = 'cli' AND platform_id = ?`, platformID).Scan(&existingMessagingGroupID); err != nil {
		return fmt.Errorf("lookup nanoclaw messaging group: %w", err)
	}

	_, err = tx.Exec(`
INSERT INTO messaging_group_agents (id, messaging_group_id, agent_group_id, session_mode, priority, created_at, engage_mode, engage_pattern, sender_scope, ignored_message_policy)
VALUES (?, ?, ?, 'shared', 0, ?, 'pattern', '.', 'all', 'drop')
ON CONFLICT(messaging_group_id, agent_group_id) DO UPDATE SET
  session_mode = excluded.session_mode,
  engage_mode = excluded.engage_mode,
  engage_pattern = excluded.engage_pattern,
  sender_scope = excluded.sender_scope,
  ignored_message_policy = excluded.ignored_message_policy`, wiringID, existingMessagingGroupID, agentGroupID, now)
	if err != nil {
		return fmt.Errorf("ensure nanoclaw agent wiring: %w", err)
	}

	if hasAgentDestinations {
		_, err = tx.Exec(`
INSERT INTO agent_destinations (agent_group_id, local_name, target_type, target_id, created_at)
VALUES (?, ?, 'channel', ?, ?)
ON CONFLICT(agent_group_id, local_name) DO UPDATE SET
  target_type = excluded.target_type,
  target_id = excluded.target_id`, agentGroupID, vulpineDestinationName(agentID), existingMessagingGroupID, now)
		if err != nil {
			return fmt.Errorf("ensure nanoclaw agent destination: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit nanoclaw route transaction: %w", err)
	}
	return nil
}

func selectedNanoClawAgentGroupID(db *sql.DB) (string, error) {
	if configured := strings.TrimSpace(os.Getenv("VULPINE_NANOCLAW_AGENT_GROUP_ID")); configured != "" {
		var id string
		if err := db.QueryRow(`SELECT id FROM agent_groups WHERE id = ?`, configured).Scan(&id); err != nil {
			return "", fmt.Errorf("configured VULPINE_NANOCLAW_AGENT_GROUP_ID %q was not found in NanoClaw", configured)
		}
		return id, nil
	}

	rows, err := db.Query(`SELECT id FROM agent_groups ORDER BY created_at ASC`)
	if err != nil {
		return "", fmt.Errorf("list nanoclaw agent groups: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", fmt.Errorf("scan nanoclaw agent group: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate nanoclaw agent groups: %w", err)
	}
	if len(ids) == 0 {
		return createDefaultNanoClawAgentGroup(db)
	}
	if len(ids) > 1 {
		return "", fmt.Errorf("multiple NanoClaw agent groups found; set VULPINE_NANOCLAW_AGENT_GROUP_ID")
	}
	return ids[0], nil
}

func createDefaultNanoClawAgentGroup(db *sql.DB) (string, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`
INSERT INTO agent_groups (id, name, folder, agent_provider, created_at)
VALUES (?, 'VulpineOS', 'vulpineos', NULL, ?)
ON CONFLICT(id) DO UPDATE SET
  name = excluded.name,
  folder = excluded.folder`, defaultNanoClawAgentGroupID, now); err != nil {
		return "", fmt.Errorf("create default nanoclaw agent group: %w", err)
	}
	return defaultNanoClawAgentGroupID, nil
}

func vulpineMessagingGroupID(agentID string) string {
	return "vulpine-" + shortHash(agentID)
}

func vulpineWiringID(agentID, agentGroupID string) string {
	return "vulpine-" + shortHash(agentID+":"+agentGroupID)
}

func vulpineDestinationName(agentID string) string {
	return "vulpine-" + shortHash(agentID)
}

func shortHash(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func LookupNanoclawAgentGroupID(nanoclawDir string) (string, error) {
	dbPath := filepath.Join(nanoclawDir, "data", "v2.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", fmt.Errorf("open nanoclaw database: %w", err)
	}
	defer db.Close()
	return selectedNanoClawAgentGroupID(db)
}

// RepairVulpineProfileDatabase makes the VulpineOS-owned NanoClaw profile
// runnable after upstream migrations have created data/v2.db. NanoClaw's
// current daemon ignores NANOCLAW_CONFIG_PATH, so provider/model state must
// also be mirrored into the upstream DB-backed container config. Browser CDP
// routing is written into the selected group workspace for agent-browser.
func RepairVulpineProfileDatabase(nanoclawDir, provider, model, cdpURL string) error {
	dbPath := filepath.Join(nanoclawDir, "data", "v2.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open nanoclaw database: %w", err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable nanoclaw foreign keys: %w", err)
	}

	agentGroupID, err := selectedNanoClawAgentGroupID(db)
	if err != nil {
		return err
	}
	if err := ensureNanoClawContainerConfig(db, agentGroupID, provider, model); err != nil {
		return err
	}
	folder, err := nanoClawAgentGroupFolder(db, agentGroupID)
	if err != nil {
		return err
	}
	if err := writeAgentBrowserConfig(nanoclawDir, folder, cdpURL); err != nil {
		return err
	}
	return nil
}

func RepairVulpineProfileDatabaseFromConfig(nanoclawDir, configPath string) error {
	configPath = strings.TrimSpace(configPath)
	if configPath == "" {
		return nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read NanoClaw runtime config: %w", err)
	}
	var cfg struct {
		Agents struct {
			Defaults struct {
				Model struct {
					Primary string `json:"primary"`
				} `json:"model"`
			} `json:"defaults"`
		} `json:"agents"`
		Browser struct {
			CDPURL string `json:"cdpUrl"`
		} `json:"browser"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parse NanoClaw runtime config: %w", err)
	}
	model := strings.TrimSpace(cfg.Agents.Defaults.Model.Primary)
	return RepairVulpineProfileDatabase(nanoclawDir, providerFromRuntimeModel(model), model, cfg.Browser.CDPURL)
}

func providerFromRuntimeModel(model string) string {
	provider, _, ok := strings.Cut(strings.TrimSpace(model), "/")
	if !ok {
		return ""
	}
	return provider
}

func findNanoclawDir() string {
	nanoclawDir := VulpineNanoclawDir()
	if _, err := os.Stat(filepath.Join(nanoclawDir, "data", "cli.sock")); err == nil {
		return nanoclawDir
	}
	return ""
}

func VulpineNanoclawDir() string {
	if dir := strings.TrimSpace(os.Getenv("VULPINE_NANOCLAW_DIR")); dir != "" {
		return dir
	}
	return config.NanoClawProfileDir()
}

func VulpineNanoclawDataDir() string {
	return filepath.Join(VulpineNanoclawDir(), "data")
}

func VulpineNanoclawSocketPath() string {
	return filepath.Join(VulpineNanoclawDataDir(), "cli.sock")
}

func FindNanoclawSocket() (string, bool) {
	socketPath := VulpineNanoclawSocketPath()
	if _, err := os.Stat(socketPath); err == nil {
		return socketPath, true
	}
	return "", false
}

func GetNanoclawDir() string {
	return findNanoclawDir()
}

func SetContainerConfig(nanoclawDir, agentGroupID, provider, model string) error {
	dbPath := filepath.Join(nanoclawDir, "data", "v2.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open nanoclaw database: %w", err)
	}
	defer db.Close()

	if err := ensureNanoClawContainerConfig(db, agentGroupID, provider, model); err != nil {
		return fmt.Errorf("set container config: %w", err)
	}
	return nil
}

func ensureNanoClawContainerConfig(db *sql.DB, agentGroupID, provider, model string) error {
	ok, err := tableExists(db, "container_configs")
	if err != nil {
		return fmt.Errorf("check nanoclaw container_configs table: %w", err)
	}
	if !ok {
		return nil
	}

	provider = nanoClawContainerProvider(provider)
	model = strings.TrimSpace(model)
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`
INSERT INTO container_configs (agent_group_id, provider, model, updated_at)
VALUES (?, NULLIF(?, ''), NULLIF(?, ''), ?)
ON CONFLICT(agent_group_id) DO UPDATE SET
  provider = COALESCE(excluded.provider, container_configs.provider),
  model = COALESCE(excluded.model, container_configs.model),
  updated_at = excluded.updated_at`, agentGroupID, provider, model, now); err != nil {
		return err
	}
	return nil
}

func nanoClawContainerProvider(provider string) string {
	switch strings.TrimSpace(provider) {
	case "opencode-go":
		return "opencode"
	default:
		return strings.TrimSpace(provider)
	}
}

func nanoClawAgentGroupFolder(db *sql.DB, agentGroupID string) (string, error) {
	var folder string
	if err := db.QueryRow(`SELECT folder FROM agent_groups WHERE id = ?`, agentGroupID).Scan(&folder); err != nil {
		return "", fmt.Errorf("lookup nanoclaw agent group folder: %w", err)
	}
	folder = strings.TrimSpace(folder)
	if folder == "" || strings.ContainsAny(folder, `/\`) || folder == "." || folder == ".." {
		return "", fmt.Errorf("invalid NanoClaw agent group folder")
	}
	return folder, nil
}

func writeAgentBrowserConfig(nanoclawDir, folder, cdpURL string) error {
	if strings.TrimSpace(folder) == "" {
		return nil
	}
	path := filepath.Join(nanoclawDir, "groups", folder, "agent-browser.json")
	cdpURL = strings.TrimSpace(cdpURL)
	if cdpURL == "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove agent-browser config: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create agent-browser config dir: %w", err)
	}
	data, err := json.MarshalIndent(map[string]string{
		"cdp": containerReachableCDPURL(cdpURL),
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal agent-browser config: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write agent-browser config: %w", err)
	}
	return nil
}

func containerReachableCDPURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimSpace(raw)
	}
	host := strings.ToLower(u.Hostname())
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return u.String()
	}
	if port := u.Port(); port != "" {
		u.Host = net.JoinHostPort("host.docker.internal", port)
	} else {
		u.Host = "host.docker.internal"
	}
	return u.String()
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func CreateOpenRouterSecret(secretPath, apiKey string) error {
	content := fmt.Sprintf(`secrets:
  - host: openrouter.ai
    header:
      Authorization: "Bearer %s"
`, apiKey)
	return os.WriteFile(secretPath, []byte(content), 0600)
}
