package vault

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// StoredProxy represents a proxy entry in the vault database.
type StoredProxy struct {
	ID      string    `json:"id"`
	Config  string    `json:"config"`  // JSON ProxyConfig
	Geo     string    `json:"geo"`     // JSON GeoInfo
	Label   string    `json:"label"`
	AddedAt time.Time `json:"added_at"`
}

// AddProxy inserts a new proxy record and returns it.
func (db *DB) AddProxy(config, geo, label string) (*StoredProxy, error) {
	id := uuid.New().String()
	now := time.Now()
	_, err := db.conn.Exec(
		`INSERT INTO proxies (id, config, geo, label, added_at) VALUES (?, ?, ?, ?, ?)`,
		id, config, geo, label, now.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("insert proxy: %w", err)
	}
	return &StoredProxy{
		ID:      id,
		Config:  config,
		Geo:     geo,
		Label:   label,
		AddedAt: now,
	}, nil
}

// ListProxies returns all stored proxies.
func (db *DB) ListProxies() ([]StoredProxy, error) {
	rows, err := db.conn.Query(`SELECT id, config, geo, label, added_at FROM proxies ORDER BY added_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list proxies: %w", err)
	}
	defer rows.Close()

	var proxies []StoredProxy
	for rows.Next() {
		var p StoredProxy
		var addedAt int64
		if err := rows.Scan(&p.ID, &p.Config, &p.Geo, &p.Label, &addedAt); err != nil {
			return nil, fmt.Errorf("scan proxy: %w", err)
		}
		p.AddedAt = time.Unix(addedAt, 0)
		proxies = append(proxies, p)
	}
	return proxies, rows.Err()
}

// GetProxy retrieves a single proxy by ID.
func (db *DB) GetProxy(id string) (*StoredProxy, error) {
	var p StoredProxy
	var addedAt int64
	err := db.conn.QueryRow(
		`SELECT id, config, geo, label, added_at FROM proxies WHERE id = ?`, id,
	).Scan(&p.ID, &p.Config, &p.Geo, &p.Label, &addedAt)
	if err != nil {
		return nil, fmt.Errorf("get proxy %s: %w", id, err)
	}
	p.AddedAt = time.Unix(addedAt, 0)
	return &p, nil
}

// UpdateProxyGeo updates the geo JSON for an existing proxy.
func (db *DB) UpdateProxyGeo(id, geoJSON string) error {
	res, err := db.conn.Exec(`UPDATE proxies SET geo = ? WHERE id = ?`, geoJSON, id)
	if err != nil {
		return fmt.Errorf("update proxy geo: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("proxy %s not found", id)
	}
	return nil
}

// DeleteProxy removes a proxy by ID.
func (db *DB) DeleteProxy(id string) error {
	res, err := db.conn.Exec(`DELETE FROM proxies WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete proxy: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("proxy %s not found", id)
	}
	return nil
}
