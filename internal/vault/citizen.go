package vault

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreateCitizen creates a new long-lived identity.
func (db *DB) CreateCitizen(label, fingerprint, proxyConfig, locale, timezone string) (*Citizen, error) {
	id := uuid.New().String()
	now := time.Now().Unix()

	_, err := db.conn.Exec(
		`INSERT INTO citizens (id, label, fingerprint, proxy_config, locale, timezone, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, label, fingerprint, proxyConfig, locale, timezone, now,
	)
	if err != nil {
		return nil, fmt.Errorf("create citizen: %w", err)
	}

	return &Citizen{
		ID:          id,
		Label:       label,
		Fingerprint: fingerprint,
		ProxyConfig: proxyConfig,
		Locale:      locale,
		Timezone:    timezone,
		CreatedAt:   time.Unix(now, 0),
	}, nil
}

// GetCitizen retrieves a citizen by ID.
func (db *DB) GetCitizen(id string) (*Citizen, error) {
	row := db.conn.QueryRow(
		`SELECT id, label, fingerprint, proxy_config, locale, timezone,
		        created_at, last_used_at, total_sessions, detection_events
		 FROM citizens WHERE id = ?`, id,
	)

	var c Citizen
	var createdAt, lastUsedAt int64
	err := row.Scan(&c.ID, &c.Label, &c.Fingerprint, &c.ProxyConfig,
		&c.Locale, &c.Timezone, &createdAt, &lastUsedAt,
		&c.TotalSessions, &c.DetectionEvents)
	if err != nil {
		return nil, fmt.Errorf("get citizen: %w", err)
	}
	c.CreatedAt = time.Unix(createdAt, 0)
	c.LastUsedAt = time.Unix(lastUsedAt, 0)
	return &c, nil
}

// ListCitizens returns all citizens ordered by last used.
func (db *DB) ListCitizens() ([]Citizen, error) {
	rows, err := db.conn.Query(
		`SELECT id, label, fingerprint, proxy_config, locale, timezone,
		        created_at, last_used_at, total_sessions, detection_events
		 FROM citizens ORDER BY last_used_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list citizens: %w", err)
	}
	defer rows.Close()

	var citizens []Citizen
	for rows.Next() {
		var c Citizen
		var createdAt, lastUsedAt int64
		if err := rows.Scan(&c.ID, &c.Label, &c.Fingerprint, &c.ProxyConfig,
			&c.Locale, &c.Timezone, &createdAt, &lastUsedAt,
			&c.TotalSessions, &c.DetectionEvents); err != nil {
			return nil, fmt.Errorf("scan citizen: %w", err)
		}
		c.CreatedAt = time.Unix(createdAt, 0)
		c.LastUsedAt = time.Unix(lastUsedAt, 0)
		citizens = append(citizens, c)
	}
	return citizens, nil
}

// UpdateCitizenUsage increments session count and updates last-used timestamp.
func (db *DB) UpdateCitizenUsage(id string) error {
	_, err := db.conn.Exec(
		`UPDATE citizens SET total_sessions = total_sessions + 1, last_used_at = ? WHERE id = ?`,
		time.Now().Unix(), id,
	)
	return err
}

// IncrementDetectionEvents increments the detection event counter.
func (db *DB) IncrementDetectionEvents(id string) error {
	_, err := db.conn.Exec(
		`UPDATE citizens SET detection_events = detection_events + 1 WHERE id = ?`, id,
	)
	return err
}

// DeleteCitizen removes a citizen and all associated data (cascade).
func (db *DB) DeleteCitizen(id string) error {
	_, err := db.conn.Exec(`DELETE FROM citizens WHERE id = ?`, id)
	return err
}

// SaveCookies stores cookies for a citizen-domain pair.
func (db *DB) SaveCookies(citizenID, domain, cookiesJSON string) error {
	_, err := db.conn.Exec(
		`INSERT OR REPLACE INTO citizen_cookies (citizen_id, domain, cookies, updated_at)
		 VALUES (?, ?, ?, ?)`,
		citizenID, domain, cookiesJSON, time.Now().Unix(),
	)
	return err
}

// GetCookies retrieves all cookies for a citizen.
func (db *DB) GetCookies(citizenID string) ([]CitizenCookies, error) {
	rows, err := db.conn.Query(
		`SELECT citizen_id, domain, cookies, updated_at FROM citizen_cookies WHERE citizen_id = ?`,
		citizenID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []CitizenCookies
	for rows.Next() {
		var cc CitizenCookies
		var updatedAt int64
		if err := rows.Scan(&cc.CitizenID, &cc.Domain, &cc.Cookies, &updatedAt); err != nil {
			return nil, err
		}
		cc.UpdatedAt = time.Unix(updatedAt, 0)
		result = append(result, cc)
	}
	return result, nil
}

// SaveStorage stores a localStorage snapshot for a citizen-origin pair.
func (db *DB) SaveStorage(citizenID, origin, dataJSON string) error {
	_, err := db.conn.Exec(
		`INSERT OR REPLACE INTO citizen_storage (citizen_id, origin, data, updated_at)
		 VALUES (?, ?, ?, ?)`,
		citizenID, origin, dataJSON, time.Now().Unix(),
	)
	return err
}

// GetStorage retrieves all localStorage snapshots for a citizen.
func (db *DB) GetStorage(citizenID string) ([]CitizenStorage, error) {
	rows, err := db.conn.Query(
		`SELECT citizen_id, origin, data, updated_at FROM citizen_storage WHERE citizen_id = ?`,
		citizenID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []CitizenStorage
	for rows.Next() {
		var cs CitizenStorage
		var updatedAt int64
		if err := rows.Scan(&cs.CitizenID, &cs.Origin, &cs.Data, &updatedAt); err != nil {
			return nil, err
		}
		cs.UpdatedAt = time.Unix(updatedAt, 0)
		result = append(result, cs)
	}
	return result, nil
}
