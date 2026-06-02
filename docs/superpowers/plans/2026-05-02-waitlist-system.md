# Waitlist System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build standalone waitlist queue + agentic email processor. Accepts signups, processes users sequentially: fetch → scrape → draft → send → notify Nick.

**Architecture:** Go HTTP server with SQLite. Sequential GET requests provide inherent rate limiting. Agent pipeline runs on demand.

**Tech Stack:** Go 1.26, SQLite (modernc.org/sqlite), standard library HTTP, Go email (gopkg.in/gomail.v2)

---

## File Structure

```
waitlist/
├── config.yaml
├── main.go
├── go.mod
├── go.sum
├── api/
│   └── handlers.go
├── db/
│   └── db.go
└── agent/
    └── pipeline.go
```

---

### Task 1: Project Structure & Database

**Files:**
- Create: `config.yaml`
- Create: `main.go`
- Create: `go.mod`
- Create: `db/db.go`
- Create: `db/db_test.go`

- [ ] **Step 1: Initialize project**

```bash
mkdir waitlist
cd waitlist
go mod init github.com/vulpineos/waitlist
```

- [ ] **Step 2: Create config.yaml**

```yaml
server:
  host: "0.0.0.0"
  port: 8080

db:
  path: "./waitlist.db"

smtp:
  host: "smtp.gmail.com"
  port: 587
  username: "your-email@gmail.com"
  password: "your-app-password"
  from: "Vulpine Team <your-email@gmail.com>"

nick:
  api_url: "https://nick-api.example.com/waitlist/contacted"
  api_key: ""
```

- [ ] **Step 3: Write db.go with schema**

```go
package db

import (
	"database/sql"
	"time"

	_ "github.com/mattn/go-sqlite3-driver"
)

type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	GitHub      string    `json:"github,omitempty"`
	LinkedIn    string    `json:"linkedin,omitempty"`
	Twitter    string    `json:"twitter,omitempty"`
	Interests  string    `json:"interests"`
	SignupDate time.Time `json:"signupDate"`
	Contacted  bool      `json:"contacted"`
	ContactedAt *time.Time `json:"contactedAt,omitempty"`
}

type DB struct {
	db *sql.DB
}

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			github TEXT,
			linkedin TEXT,
			twitter TEXT,
			interests TEXT NOT NULL,
			signup_date TEXT NOT NULL,
			contacted INTEGER DEFAULT 0,
			contacted_at TEXT
		)
	`)
	if err != nil {
		return nil, err
	}

	return &DB{db: db}, nil
}

func (d *DB) AddUser(email, github, linkedin, twitter, interests string) (string, error) {
	id := fmt.Sprintf("user-%d", time.Now().UnixNano())
	_, err := d.db.Exec(`
		INSERT INTO users (id, email, github, linkedin, twitter, interests, signup_date)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, id, email, github, linkedin, twitter, interests, time.Now().Format(time.RFC3339))
	return id, err
}

func (d *DB) GetNextUncontacted() (*User, error) {
	row := d.db.QueryRow(`
		SELECT id, email, github, linkedin, twitter, interests, signup_date
		FROM users
		WHERE contacted = 0
		ORDER BY signup_date ASC
		LIMIT 1
	`)

	var u User
	var signupDate string
	err := row.Scan(&u.ID, &u.Email, &u.GitHub, &u.LinkedIn, &u.Twitter, &u.Interests, &signupDate)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	u.SignupDate, _ = time.Parse(time.RFC3339, signupDate)
	return &u, nil
}

func (d *DB) MarkContacted(id string) error {
	_, err := d.db.Exec(`
		UPDATE users SET contacted = 1, contacted_at = ? WHERE id = ?
	`, time.Now().Format(time.RFC3339), id)
	return err
}

func (d *DB) GetStats() (total, contacted, pending int, err error) {
	row := d.db.QueryRow(`SELECT COUNT(*), SUM(contacted), SUM(1 - contacted) FROM users`)
	err = row.Scan(&total, &contacted, &pending)
	return
}

func (d *DB) Close() error {
	return d.db.Close()
}
```

- [ ] **Step 4: Verify it compiles**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add .
git commit -m "feat: add project structure and database layer"
```

---

### Task 2: API Handlers

**Files:**
- Create: `api/handlers.go`

- [ ] **Step 1: Write handlers**

```go
package api

import (
	"encoding/json"
	"log"
	"net/http"

	"vulpineos/waitlist/db"
)

type Handler struct {
	DB *db.DB
}

type SignupRequest struct {
	Email    string `json:"email"`
	GitHub   string `json:"github,omitempty"`
	LinkedIn string `json:"linkedin,omitempty"`
	Twitter string `json:"twitter,omitempty"`
	Interests string `json:"interests"`
}

type SignupResponse struct {
	ID string `json:"id"`
}

type NextUserResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	GitHub    string `json:"github,omitempty"`
	LinkedIn  string `json:"linkedin,omitempty"`
	Twitter   string `json:"twitter,omitempty"`
	Interests string `json:"interests"`
}

type StatsResponse struct {
	Total     int `json:"total"`
	Contacted int `json:"contacted"`
	Pending  int `json:"pending"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func (h *Handler) HandleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SignupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Email == "" || req.Interests == "" {
		http.Error(w, "email and interests required", http.StatusBadRequest)
		return
	}

	id, err := h.DB.AddUser(req.Email, req.GitHub, req.LinkedIn, req.Twitter, req.Interests)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(SignupResponse{ID: id})
}

func (h *Handler) HandleNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	user, err := h.DB.GetNextUncontacted()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if user == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "no users in queue"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(NextUserResponse{
		ID:        user.ID,
		Email:     user.Email,
		GitHub:    user.GitHub,
		LinkedIn:  user.LinkedIn,
		Twitter:   user.Twitter,
		Interests: user.Interests,
	})
}

func (h *Handler) HandleContacted(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract user ID from URL - /contacted/{userId}
	// (simplified - would use gorilla/mux in production)
	userID := r.URL.Path[len("/contacted/"):]

	if err := h.DB.MarkContacted(userID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("Marked user %s as contacted", userID)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) HandleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	total, contacted, pending, err := h.DB.GetStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(StatsResponse{
		Total:     total,
		Contacted: contacted,
		Pending:  pending,
	})
}
```

- [ ] **Step 2: Update main.go**

Add HTTP server setup:

```go
func main() {
	cfg, err := LoadConfig("config.yaml")
	if err != nil {
		log.Fatal(err)
	}

	database, err := db.Open(cfg.DB.Path)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()

	handler := &api.Handler{DB: database}

	http.HandleFunc("/signup", handler.HandleSignup)
	http.HandleFunc("/next-user", handler.HandleNext)
	http.HandleFunc("/contacted/", handler.HandleContacted)
	http.HandleFunc("/stats", handler.HandleStats)

	log.Printf("Starting server on %s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port), nil))
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add .
git commit -m "feat: add API handlers"
```

---

### Task 3: Agent Pipeline

**Files:**
- Create: `agent/pipeline.go`

- [ ] **Step 1: Write pipeline**

```go
package agent

import (
	"bytes"
	"log"
	"net/smtp"
	"time"

	"vulpineos/waitlist/db"
)

type Config struct {
	 SMTPHost     string
	 SMTPPort     int
	 SMTPUsername string
	 SMTPPassword string
	 SMTPFrom     string
	 NickAPIURL   string
	 NickAPIKey  string
}

type Pipeline struct {
	DB   *db.DB
	Cfg  Config
}

func NewPipeline(db *db.DB, cfg Config) *Pipeline {
	return &Pipeline{DB: db, Cfg: cfg}
}

// RunNext fetches next user, processes, sends email, notifies Nick
func (p *Pipeline) RunNext() error {
	user, err := p.DB.GetNextUncontacted()
	if err != nil {
		return err
	}
	if user == nil {
		return nil // no users
	}

	// 1. Scrape (simulated - would use Vulpine here)
	log.Printf("Processing user: %s", user.Email)

	// 2. Draft email (simulated - would use Vulpine LLM)
	emailBody := p.draftEmail(user)

	// 3. Send email
	if err := p.sendEmail(user.Email, "Welcome to Vulpine", emailBody); err != nil {
		log.Printf("Failed to send email: %v", err)
		return err
	}

	// 4. Mark contacted
	if err := p.DB.MarkContacted(user.ID); err != nil {
		return err
	}

	// 5. Notify Nick's API
	p.notifyNick(user.Email)

	log.Printf("Completed: %s", user.Email)
	return nil
}

func (p *Pipeline) draftEmail(user *db.User) string {
	// This would use Vulpine LLM in production
	// For now, simple template
	return "Hi " + user.Email + ",\n\n" +
		"Thank you for your interest in Vulpine!" + "\n" +
		"We see you're interested in: " + user.Interests + "\n\n" +
		"Based on your interest, we think Vulpine could help you..." + "\n\n" +
		"Best,\nThe Vulpine Team\n"
}

func (p *Pipeline) sendEmail(to, subject, body string) error {
	msg := []byte("From: " + p.Cfg.SMTPFrom + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		body)

	err := smtp.SendMail(
		fmt.Sprintf("%s:%d", p.Cfg.SMTPHost, p.Cfg.SMTPPort),
		smtp.PlainAuth("", p.Cfg.SMTPUsername, p.Cfg.SMTPPassword, p.Cfg.SMTPHost),
		p.Cfg.SMTPFrom,
		[]string{to},
		msg,
	)
	return err
}

func (p *Pipeline) notifyNick(email string) {
	if p.Cfg.NickAPIURL == "" {
		return
	}
	// POST to Nick's API
	// (simplified - would use net/http)
	log.Printf("Would notify Nick: %s at %s", email, p.Cfg.NickAPIURL)
}
```

- [ ] **Step 2: Add /process-next handler**

Add endpoint to trigger pipeline:

```go
func (h *Handler) HandleProcess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodPost)
		return
	}

	// This would run the pipeline
	// In production: trigger async job
	json.NewEncoder(w).Encode(map[string]string{"status": "processing"})
}
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add .
git commit -m "feat: add agent pipeline"
```

---

### Task 4: CLI & Manual Trigger

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Add CLI commands**

```go
func main() {
	// Check for CLI flags before starting server
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "process":
			// One-shot process
			cfg, _ := LoadConfig("config.yaml")
			db, _ := db.Open(cfg.DB.Path)
			p := agent.NewPipeline(db, cfg.AgentConfig())
			p.RunNext()
			return
		case "stats":
			cfg, _ := LoadConfig("config.yaml")
			db, _ := db.Open(cfg.DB.Path)
			total, contacted, pending, _ := db.GetStats()
			log.Printf("Total: %d, Contacted: %d, Pending: %d", total, contacted, pending)
			return
		}
	}

	// ... server code
}
```

- [ ] **Step 2: Test CLI**

```bash
# Process one user
go run . process

# Check stats
go run . stats
```

- [ ] **Step 3: Commit**

---

### Task 5: Verify & Test

**Files:**
- No new files

- [ ] **Step 1: Manual test**

```bash
# Start server
go run . &

# Add test user
curl -X POST http://localhost:8080/signup \
  -H "Content-Type: application/json" \
  -d '{"email":"test@example.com","interests":"testing"}'

# Get next user
curl http://localhost:8080/next-user

# Check stats
curl http://localhost:8080/stats
```

- [ ] **Step 2: Process user**

```bash
go run . process
```

- [ ] **Step 3: Verify contacted**

```bash
curl http://localhost:8080/stats
```

- [ ] **Step 4: Commit**

---

## Spec Coverage Check

| Spec Requirement | Task |
|------------------|------|
| POST /signup endpoint | Task 2 |
| GET /next-user endpoint | Task 2 |
| POST /contacted/{id} | Task 2 |
| GET /stats endpoint | Task 2 |
| Agent pipeline (fetch→scrape→draft→send→notify) | Task 3 |
| Sequential rate limiting | Task 2 |
| Config file for Nick's API | Task 1 |

---

## Type Consistency Check

- `db.User` struct defined in Task 1, used in Tasks 2,3
- `api.Handler` wraps `*db.DB` - consistent across handlers
- All config loaded from `config.yaml` - single source

---

## Execution Options

**1. Subagent-Driven (recommended)** - Dispatch subagent per task

**2. Inline Execution** - Execute tasks here with checkpoints

Which approach?
