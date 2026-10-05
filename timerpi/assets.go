package timerpi

import (
	"database/sql"
	"fmt"
)

// PLAN §11.2 phase 2: venue images (the `map` slot) ride a tiny blob
// table — a few hundred KiB each, read-heavy, and §11.9 wants them inside
// the show bundle later, so SQLite is the right home (no data-dir files to
// babysit in backup/restore).

// Asset is one uploaded image (venue map, sponsor board…).
type Asset struct {
	ID    int64  `db:"id"     json:"id"`
	Name  string `db:"name"   json:"name"`
	Mime  string `db:"mime"   json:"mime"`
	Bytes []byte `db:"bytes"  json:"-"`
	Ts    int64  `db:"ts"     json:"ts"`
}

// CreateAsset stores one image; the id is its public URL key.
func (d *DB) CreateAsset(name, mime string, data []byte) (Asset, error) {
	res, err := d.Exec(`INSERT INTO assets (name, mime, bytes, ts) VALUES (?, ?, ?, ?)`,
		ClipUTF8(name, 120), mime, data, nowMS())
	if err != nil {
		return Asset{}, fmt.Errorf("timerpi: create asset: %w", err)
	}
	id, _ := res.LastInsertId()
	return Asset{ID: id, Name: ClipUTF8(name, 120), Mime: mime, Bytes: data, Ts: nowMS()}, nil
}

// GetAsset fetches one asset by id (sql.ErrNoRows when missing).
func (d *DB) GetAsset(id int64) (Asset, error) {
	var a Asset
	err := d.Get(&a, `SELECT id, name, mime, bytes, ts FROM assets WHERE id = ?`, id)
	if err != nil {
		return Asset{}, err // ErrNoRows passes through for 404 mapping
	}
	return a, nil
}

// DeleteAsset removes one asset (sql.ErrNoRows when missing).
func (d *DB) DeleteAsset(id int64) error {
	res, err := d.Exec(`DELETE FROM assets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("timerpi: delete asset: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetZoneMap points a zone label at an asset id (empty assetId clears).
func (d *DB) SetZoneMap(zone string, assetID int64) error {
	zone = SanitizeScreenName(zone)
	if zone == "" {
		return fmt.Errorf("timerpi: zone map needs a zone")
	}
	key := "zone.map." + zone
	if assetID <= 0 {
		_, err := d.Exec(`DELETE FROM settings WHERE key = ?`, key)
		if err != nil {
			return fmt.Errorf("timerpi: zone map clear: %w", err)
		}
		return nil
	}
	return d.SetSetting(key, fmt.Sprintf("%d", assetID))
}

// ZoneMap returns the asset id mapped to a zone (0 = none).
func (d *DB) ZoneMap(zone string) int64 {
	zone = SanitizeScreenName(zone)
	if zone == "" {
		return 0
	}
	var v string
	if err := d.Get(&v, `SELECT value FROM settings WHERE key = ?`, "zone.map."+zone); err != nil {
		return 0
	}
	var id int64
	_, _ = fmt.Sscanf(v, "%d", &id)
	return id
}
