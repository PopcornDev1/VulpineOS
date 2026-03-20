package vault

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS citizens (
	id              TEXT PRIMARY KEY,
	label           TEXT NOT NULL,
	fingerprint     TEXT NOT NULL,
	proxy_config    TEXT DEFAULT '',
	locale          TEXT DEFAULT '',
	timezone        TEXT DEFAULT '',
	created_at      INTEGER NOT NULL,
	last_used_at    INTEGER DEFAULT 0,
	total_sessions  INTEGER DEFAULT 0,
	detection_events INTEGER DEFAULT 0
);

CREATE TABLE IF NOT EXISTS citizen_cookies (
	citizen_id TEXT NOT NULL REFERENCES citizens(id) ON DELETE CASCADE,
	domain     TEXT NOT NULL,
	cookies    TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (citizen_id, domain)
);

CREATE TABLE IF NOT EXISTS citizen_storage (
	citizen_id TEXT NOT NULL REFERENCES citizens(id) ON DELETE CASCADE,
	origin     TEXT NOT NULL,
	data       TEXT NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (citizen_id, origin)
);

CREATE TABLE IF NOT EXISTS templates (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL UNIQUE,
	description      TEXT DEFAULT '',
	sop              TEXT NOT NULL,
	interaction_mode TEXT NOT NULL DEFAULT 'full',
	allowed_domains  TEXT DEFAULT '[]',
	constraints      TEXT DEFAULT '{}',
	created_at       INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS nomad_sessions (
	id           TEXT PRIMARY KEY,
	template_id  TEXT REFERENCES templates(id),
	fingerprint  TEXT NOT NULL,
	started_at   INTEGER NOT NULL,
	completed_at INTEGER DEFAULT 0,
	status       TEXT NOT NULL DEFAULT 'active',
	result       TEXT DEFAULT ''
);
`

// DB wraps the SQLite vault database.
type DB struct {
	conn *sql.DB
}

// Open opens (or creates) the vault database at the default path.
func Open() (*DB, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".vulpineos")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create vault dir: %w", err)
	}
	return OpenPath(filepath.Join(dir, "vault.db"))
}

// OpenPath opens the vault database at the given path.
func OpenPath(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("open vault db: %w", err)
	}

	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate vault: %w", err)
	}

	return &DB{conn: conn}, nil
}

// Close closes the database.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Conn returns the underlying sql.DB for advanced queries.
func (db *DB) Conn() *sql.DB {
	return db.conn
}
