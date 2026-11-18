package timerpi

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ---------------------------------------------------------------------------
// Named display screens (F1: stable per-screen identity for the operator's
// screens panel — "Stage Left" keeps its theme/board across reconnects).
// Screens register on WS join (?screen= name, persisted to the browser's
// localStorage); unnamed displays stay anonymous (live peers only).
const MaxScreenNameLen = 40

// Screen is one named display + its operator-assigned overrides (empty =
// follow the show defaults).
type Screen struct {
	ShowID   int64  `db:"show_id"   json:"-"`
	Name     string `db:"name"      json:"name"`
	Theme    string `db:"theme"     json:"theme"`
	BoardID  int64  `db:"board_id"  json:"boardId"`
	Room     string `db:"room"      json:"room"`
	LastSeen int64  `db:"last_seen" json:"lastSeen"`
	// Kind is the display type: audience | walkin | presenter ("" = not
	// set yet). Rotation is 0/90/180/270 degrees (portrait poster screens).
	Kind     string `db:"kind"      json:"kind"`
	Rotation int    `db:"rotation"  json:"rotation"`
	// Template is a built-in layout the screen shows directly ("" = none):
	// built-ins are never edited; editing one makes an event layout
	// (STATUS U10/U11). A board_id > 0 wins over it.
	Template string `db:"template"  json:"template"`
}

// Display types (PRODUCT §3.2).
const (
	ScreenAudience  = "audience"
	ScreenWalkin    = "walkin"
	ScreenPresenter = "presenter"
)

// ValidScreenKind / ValidRotation guard the screen look settings.
func ValidScreenKind(k string) bool {
	return k == "" || k == ScreenAudience || k == ScreenWalkin || k == ScreenPresenter
}
func ValidRotation(r int) bool { return r == 0 || r == 90 || r == 180 || r == 270 }

// SetScreenLook sets a screen's display type and rotation (creating the
// registry row if needed).
func (d *DB) SetScreenLook(showID int64, name, kind string, rotation int) error {
	name = SanitizeScreenName(name)
	if name == "" {
		return fmt.Errorf("timerpi: empty screen name")
	}
	if !ValidScreenKind(kind) {
		return fmt.Errorf("timerpi: unknown display type %q", kind)
	}
	if !ValidRotation(rotation) {
		return fmt.Errorf("timerpi: rotation must be 0, 90, 180 or 270")
	}
	now := nowMS()
	_, err := d.Exec(`INSERT INTO screens (show_id, name, kind, rotation, last_seen) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET kind = ?, rotation = ?`, showID, name, kind, rotation, now, kind, rotation)
	return err
}

// SanitizeScreenName trims and bounds a screen name ("Stage Left",
// "Bühne 1", "舞台"): letters and digits of any script (with their
// combining marks), space, hyphen, underscore; at most MaxScreenNameLen
// runes. Everything else (controls, punctuation, symbols) is dropped.
func SanitizeScreenName(name string) string {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(name) {
		if n >= MaxScreenNameLen {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == ' ' || r == '-' || r == '_' {
			b.WriteRune(r)
			n++
		}
	}
	return strings.TrimSpace(b.String())
}

// UpsertScreen records a join (insert or last_seen refresh; operator
// overrides untouched).
func (d *DB) UpsertScreen(showID int64, name string) error {
	name = SanitizeScreenName(name)
	if name == "" {
		return fmt.Errorf("timerpi: empty screen name")
	}
	// Hardening: every JOIN upserts a registry row, and joins are
	// unauthenticated on screen/display roles — bound per show so a LAN
	// flood cannot grow the table before the 512-session cap bites.
	var n int64
	if err := d.Get(&n, `SELECT COUNT(*) FROM screens WHERE show_id = ?`, showID); err == nil && n >= maxScreenRows {
		var known int64
		if err := d.Get(&known, `SELECT COUNT(*) FROM screens WHERE show_id = ? AND name = ?`, showID, name); err == nil && known == 0 {
			return fmt.Errorf("timerpi: too many screens for show %d (cap %d)", showID, maxScreenRows)
		}
	}
	now := nowMS()
	if _, err := d.Exec(`INSERT INTO screens (show_id, name, last_seen) VALUES (?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET last_seen = ?`, showID, name, now, now); err != nil {
		return fmt.Errorf("timerpi: upsert screen: %w", err)
	}
	return nil
}

const maxScreenRows = 2000

// SetScreenConfig writes the operator's per-screen overrides (empty theme
// clears to the default; board 0 clears to the show board).
func (d *DB) SetScreenConfig(showID int64, name, theme string, boardID int64, room string) error {
	name = SanitizeScreenName(name)
	if name == "" {
		return fmt.Errorf("timerpi: empty screen name")
	}
	room = SanitizeScreenName(room)
	now := nowMS()
	if _, err := d.Exec(`INSERT INTO screens (show_id, name, theme, board_id, room, last_seen) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET theme = ?, board_id = ?, room = ?, last_seen = ?,
			template = CASE WHEN ? > 0 THEN '' ELSE template END`,
		showID, name, theme, boardID, room, now, theme, boardID, room, now, boardID); err != nil {
		return fmt.Errorf("timerpi: set screen config: %w", err)
	}
	return nil
}

// ListScreens returns the registered screens, most-recently-seen first.
func (d *DB) ListScreens(showID int64) ([]Screen, error) {
	var out []Screen
	err := d.Select(&out, `SELECT show_id, name, theme, board_id, room, last_seen, kind, rotation, template FROM screens
		WHERE show_id = ? ORDER BY last_seen DESC`, showID)
	if err != nil {
		return nil, fmt.Errorf("timerpi: list screens: %w", err)
	}
	if out == nil {
		out = []Screen{}
	}
	return out, nil
}

// GetScreenByName fetches one registered screen ("" name → no rows error).
func (d *DB) GetScreenByName(showID int64, name string) (Screen, error) {
	var s Screen
	err := d.Get(&s, `SELECT show_id, name, theme, board_id, room, last_seen, kind, rotation, template FROM screens
		WHERE show_id = ? AND name = ?`, showID, name)
	if err != nil {
		return Screen{}, fmt.Errorf("timerpi: get screen: %w", err)
	}
	return s, nil
}

// ErrScreenNameTaken: a rename target already names another screen.
var ErrScreenNameTaken = errors.New("timerpi: another screen already has that name")

// RenameScreen moves a registry row (operator rename from the panel).
func (d *DB) RenameScreen(showID int64, from, to string) error {
	from, to = SanitizeScreenName(from), SanitizeScreenName(to)
	if to == "" {
		return fmt.Errorf("timerpi: screen rename needs both names")
	}
	if from == "" {
		return sql.ErrNoRows
	}
	if from == to {
		return nil // identity rename must NOT delete the row it renames onto
	}
	tx, err := d.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Renaming onto another screen's name used to delete that screen's row
	// while its tab was still live (BUGLOG RW40): refuse instead.
	var taken int
	if err := tx.Get(&taken, `SELECT COUNT(*) FROM screens WHERE show_id = ? AND name = ?`, showID, to); err != nil {
		return err
	}
	if taken > 0 {
		return ErrScreenNameTaken
	}
	res, err := tx.Exec(`UPDATE screens SET name = ?, last_seen = ? WHERE show_id = ? AND name = ?`,
		to, nowMS(), showID, from)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows // no such screen
	}
	return tx.Commit()
}

// SetScreenTemplate makes the screen show a built-in layout directly
// ("" = none: back to the plain timer), clearing any layout assignment.
func (d *DB) SetScreenTemplate(showID int64, name, template string) error {
	name = SanitizeScreenName(name)
	if name == "" {
		return fmt.Errorf("timerpi: empty screen name")
	}
	now := nowMS()
	if _, err := d.Exec(`INSERT INTO screens (show_id, name, template, last_seen) VALUES (?, ?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET template = ?, board_id = 0, last_seen = ?`,
		showID, name, template, now, template, now); err != nil {
		return fmt.Errorf("timerpi: set screen template: %w", err)
	}
	return nil
}

// ScreenKey returns the screen's key, creating the screen row and a fresh
// random key when either is missing (BUGLOG RW9). The key rides the
// screen's URL (?key=) from capture or the Screens page "screen link"; only
// a keyed screen receives operator content (stage messages, notes, the
// Presenter item). Forgetting the screen (DeleteScreen) or deleting its
// room or event drops the key: the screen is released.
func (d *DB) ScreenKey(showID int64, name string) (string, error) {
	name = SanitizeScreenName(name)
	if name == "" {
		return "", fmt.Errorf("timerpi: screen key needs a name")
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	fresh := hex.EncodeToString(b[:])
	if _, err := d.Exec(`INSERT INTO screens (show_id, name, last_seen, key) VALUES (?, ?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET key = CASE WHEN key = '' THEN excluded.key ELSE key END`,
		showID, name, nowMS(), fresh); err != nil {
		return "", fmt.Errorf("timerpi: screen key: %w", err)
	}
	var key string
	if err := d.Get(&key, `SELECT key FROM screens WHERE show_id = ? AND name = ?`, showID, name); err != nil {
		return "", fmt.Errorf("timerpi: screen key: %w", err)
	}
	return key, nil
}

// ScreenKeyValid reports whether key is the named screen's key.
func (d *DB) ScreenKeyValid(showID int64, name, key string) bool {
	name = SanitizeScreenName(name)
	if name == "" || key == "" {
		return false
	}
	var want string
	if err := d.Get(&want, `SELECT key FROM screens WHERE show_id = ? AND name = ?`, showID, name); err != nil || want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(key)) == 1
}

// ClearScreenBoard drops the board assignment from every screen pointing
// at bid (called when a board is deleted — a stale id would navigate locked
// TVs to a 404 on every join push).
//
// Layouts are event-wide, so every screen of every room using it falls
// back (board ids are unique box-wide).
func (d *DB) ClearScreenBoard(bid int64) error {
	_, err := d.Exec(`UPDATE screens SET board_id = 0 WHERE board_id = ?`, bid)
	if err != nil {
		return fmt.Errorf("timerpi: clear screen board: %w", err)
	}
	return nil
}

// DeleteScreen forgets a registered screen (config gone; a still-live tab
// re-registers on its next join — that's the honest semantic).
func (d *DB) DeleteScreen(showID int64, name string) error {
	if _, err := d.Exec(`DELETE FROM screens WHERE show_id = ? AND name = ?`, showID, name); err != nil {
		return fmt.Errorf("timerpi: delete screen: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Waiting room (displays whose show vanished). A display whose join is
// refused registers itself here; any operator browser lists entries and can
// CAPTURE one into a live show (sets `assigned`), which the display's poll
// consumes and navigates to. Stale rows (>10 min silence) are pruned on
// every touch — a waiting display re-registers with each poll anyway.

// WaitingScreen is one registry row.
type WaitingScreen struct {
	ID       int64  `db:"id"         json:"id"`
	Name     string `db:"name"       json:"name"`
	Host     string `db:"host"       json:"host"`
	LastSeen int64  `db:"last_seen"  json:"lastSeen"`
	Assigned string `db:"assigned"   json:"-"`
	Screen   string `db:"screen"     json:"-"`
	Handheld bool   `db:"handheld"   json:"handheld"`
	// IP is the client address the screen registered from: on the cloud
	// an Event Technician only sees screens behind their own address.
	IP string `db:"ip" json:"-"`
}

const waitingStaleAfterMS = 10 * 60 * 1000

// maxWaitingRows caps unassigned waiting screens (BUGLOG RW16): a loop of
// random names can't flood every operator's "Waiting" list.
const maxWaitingRows = 200

// SetWaitingHandheld records whether a waiting screen is a phone/tablet.
func (d *DB) SetWaitingHandheld(name, host string, handheld bool) error {
	name, host = SanitizeScreenName(name), ClipUTF8(strings.TrimSpace(host), 80)
	_, err := d.Exec(`UPDATE waiting_screens SET handheld = ? WHERE name = ? AND host = ?`, handheld, name, host)
	return err
}

// SpendNonce marks a single-use nonce spent until expires (unix seconds);
// fresh=false when it was already spent. Expired nonces are dropped first.
// Persisted so a restart doesn't make a used sign-in code work again.
func (d *DB) SpendNonce(nonce string, expires, now int64) (bool, error) {
	if _, err := d.Exec(`DELETE FROM spent_nonces WHERE expires < ?`, now); err != nil {
		return false, err
	}
	res, err := d.Exec(`INSERT OR IGNORE INTO spent_nonces (nonce, expires) VALUES (?, ?)`, nonce, expires)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// SetWaitingIP records the client address a waiting screen polls from.
func (d *DB) SetWaitingIP(name, host, ip string) error {
	name, host = SanitizeScreenName(name), ClipUTF8(strings.TrimSpace(host), 80)
	_, err := d.Exec(`UPDATE waiting_screens SET ip = ? WHERE name = ? AND host = ?`, ClipUTF8(ip, 64), name, host)
	return err
}

// ErrWaitingFull: the waiting list is at maxWaitingRows.
var ErrWaitingFull = errors.New("timerpi: too many screens waiting to be set up")

// RegisterWaiting upserts a waiting display (name sanitized; host free text,
// bounded). Empty name after sanitizing is refused.
func (d *DB) RegisterWaiting(name, host string) error {
	return d.RegisterWaitingToken(name, host, "")
}

// RegisterWaitingToken is RegisterWaiting with the tab's waiting token:
// the first token a row sees is the only one that may later claim its
// capture (and the screen key that comes with it; BUGLOG RW9).
func (d *DB) RegisterWaitingToken(name, host, token string) error {
	token = ClipUTF8(strings.TrimSpace(token), 64)
	name = SanitizeScreenName(name)
	if name == "" {
		return fmt.Errorf("timerpi: waiting register needs a name")
	}
	host = ClipUTF8(strings.TrimSpace(host), 80)
	now := nowMS()
	var fresh, known int64
	if err := d.Get(&fresh, `SELECT COUNT(*) FROM waiting_screens WHERE assigned = '' AND last_seen >= ?`, now-waitingStaleAfterMS); err != nil {
		return fmt.Errorf("timerpi: register waiting: %w", err)
	}
	if fresh >= maxWaitingRows {
		if err := d.Get(&known, `SELECT COUNT(*) FROM waiting_screens WHERE name = ? AND host = ?`, name, host); err != nil {
			return fmt.Errorf("timerpi: register waiting: %w", err)
		}
		if known == 0 {
			return ErrWaitingFull
		}
	}
	_, err := d.Exec(`INSERT INTO waiting_screens (name, host, last_seen, token) VALUES (?, ?, ?, ?)
		ON CONFLICT (name, host) DO UPDATE SET last_seen = ?,
			token = CASE WHEN token = '' THEN excluded.token ELSE token END`, name, host, now, token, now)
	if err != nil {
		return fmt.Errorf("timerpi: register waiting: %w", err)
	}
	return nil
}

// PruneWaiting removes rows silent past the stale window.
func (d *DB) PruneWaiting() {
	_, _ = d.Exec(`DELETE FROM waiting_screens WHERE last_seen < ?`, nowMS()-waitingStaleAfterMS)
}

// WaitingLiveMS: a waiting screen polls every 2 s, so one silent for
// longer than this is closed (or lost its network) and is listed as offline.
const WaitingLiveMS = 15 * 1000

// ListWaiting returns fresh rows: live screens first, newest first, so the
// TV just opened is the top card and a closed tab never is (review I8).
func (d *DB) ListWaiting() ([]WaitingScreen, error) {
	d.PruneWaiting()
	var out []WaitingScreen
	err := d.Select(&out, `SELECT id, name, host, last_seen, assigned, screen, handheld, ip FROM waiting_screens
		WHERE assigned = '' ORDER BY (last_seen >= ?) DESC, id DESC`, nowMS()-WaitingLiveMS)
	if err != nil {
		return nil, fmt.Errorf("timerpi: list waiting: %w", err)
	}
	if out == nil {
		out = []WaitingScreen{}
	}
	return out, nil
}

// ClaimWaiting consumes an assignment for (name, host): returns the
// captured show code + the screen name to adopt, ONCE. The row is removed
// on claim — the display leaves the waiting room for good (PLAN §11.2).
func (d *DB) ClaimWaiting(name, host string) (string, string, error) {
	return d.ClaimWaitingToken(name, host, "")
}

// ClaimWaitingToken is ClaimWaiting for a tab holding a waiting token: a
// row registered with a token is only claimed by that same token, so a
// stranger polling with the screen's name can't steal its capture.
func (d *DB) ClaimWaitingToken(name, host, token string) (string, string, error) {
	code, screen, _, err := d.ClaimWaitingBox(name, host, token)
	return code, screen, err
}

// RegisterBoxWaiting is a box's registration: a waiting row that also
// carries its current 6-digit pairing code (VENUE-CLOUD §4).
func (d *DB) RegisterBoxWaiting(name, host, token, code string) error {
	if err := d.RegisterWaitingToken(name, host, token); err != nil {
		return err
	}
	if !ValidPairCode(code) {
		return fmt.Errorf("timerpi: bad pairing code")
	}
	_, err := d.Exec(`UPDATE waiting_screens SET pair_code = ? WHERE name = ? AND host = ? AND token = ?`,
		code, SanitizeScreenName(name), ClipUTF8(strings.TrimSpace(host), 80), ClipUTF8(strings.TrimSpace(token), 64))
	return err
}

// ValidPairCode: six digits.
func ValidPairCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// pairFreshMS: a pairing code only matches a box that registered it within
// the last minute (boxes re-register every few seconds).
const pairFreshMS = 60 * 1000

// WaitingByPairCode finds the unassigned box showing code.
func (d *DB) WaitingByPairCode(code string) (WaitingScreen, error) {
	var w WaitingScreen
	if !ValidPairCode(code) {
		return w, sql.ErrNoRows
	}
	err := d.Get(&w, `SELECT id, name, host, last_seen, assigned, screen, handheld, ip FROM waiting_screens
		WHERE pair_code = ? AND last_seen >= ? ORDER BY last_seen DESC LIMIT 1`, code, nowMS()-pairFreshMS)
	return w, err
}

// ClaimWaitingBox is ClaimWaitingToken that also reports whether the row
// was a box (registered with a pairing code): only a box receives the
// event's mesh key with its capture.
func (d *DB) ClaimWaitingBox(name, host, token string) (string, string, bool, error) {
	name = SanitizeScreenName(name)
	host = ClipUTF8(strings.TrimSpace(host), 80)
	token = ClipUTF8(strings.TrimSpace(token), 64)
	if err := d.RegisterWaitingToken(name, host, token); err != nil && !errors.Is(err, ErrWaitingFull) {
		return "", "", false, nil
	}
	var row struct {
		Assigned string `db:"assigned"`
		Screen   string `db:"screen"`
		Token    string `db:"token"`
		PairCode string `db:"pair_code"`
	}
	err := d.Get(&row, `SELECT assigned, screen, token, pair_code FROM waiting_screens WHERE name = ? AND host = ?`, name, host)
	if err != nil || row.Assigned == "" {
		return "", "", false, nil
	}
	if row.Token != "" && subtle.ConstantTimeCompare([]byte(row.Token), []byte(token)) != 1 {
		return "", "", false, nil // not the tab that registered this screen
	}
	// Atomic win: the conditional UPDATE claims the row or loses it to a
	// racing claimant (hardening round — the read-then-delete window let
	// two displays both hop to the same capture).
	res, err := d.Exec(`UPDATE waiting_screens SET assigned = '' WHERE name = ? AND host = ? AND assigned = ?`, name, host, row.Assigned)
	if err != nil {
		return "", "", false, fmt.Errorf("timerpi: claim waiting: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", "", false, nil // lost the race — the other poller hops
	}
	if _, err := d.Exec(`DELETE FROM waiting_screens WHERE name = ? AND host = ?`, name, host); err != nil {
		return "", "", false, fmt.Errorf("timerpi: claim waiting: %w", err)
	}
	return row.Assigned, row.Screen, row.PairCode != "" && row.Token != "", nil
}

// GetWaiting fetches one waiting row (sql.ErrNoRows when missing).
func (d *DB) GetWaiting(id int64) (WaitingScreen, error) {
	var w WaitingScreen
	err := d.Get(&w, `SELECT id, name, host, last_seen, assigned, screen, handheld, ip FROM waiting_screens WHERE id = ?`, id)
	if err != nil {
		return WaitingScreen{}, err
	}
	return w, nil
}

// AssignWaiting marks a waiting row captured into a show code, carrying
// the screen name the display adopts on its hop (PLAN §11.2 capture modal).
// Only an unassigned row, or one already assigned to the same room (the
// operator correcting a capture before the screen hops), is taken (BUGLOG
// RW38): operators of two rooms capturing the same screen can't both win;
// the loser gets sql.ErrNoRows.
func (d *DB) AssignWaiting(id int64, code, screen string) error {
	res, err := d.Exec(`UPDATE waiting_screens SET assigned = ?, screen = ? WHERE id = ? AND (assigned = '' OR assigned = ?)`,
		code, SanitizeScreenName(screen), id, code)
	if err != nil {
		return fmt.Errorf("timerpi: assign waiting: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteWaiting dismisses a waiting row.
func (d *DB) DeleteWaiting(id int64) error {
	res, err := d.Exec(`DELETE FROM waiting_screens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("timerpi: delete waiting: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ---------------------------------------------------------------------------
// Display presets (F2: named screen-configuration bundles — theme +
// per-screen assignments — surviving restarts, exportable as JSON).
type DisplayPreset struct {
	ID        int64  `db:"id"         json:"id"`
	ShowID    int64  `db:"show_id"    json:"-"`
	Name      string `db:"name"       json:"name"`
	Data      string `db:"data"       json:"data"`
	UpdatedAt int64  `db:"updated_at" json:"updatedAt"`
}

// SavePreset creates or replaces the show's preset under name.
func (d *DB) SavePreset(showID int64, name, data string) (DisplayPreset, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(data) > 65536 {
		return DisplayPreset{}, fmt.Errorf("timerpi: bad preset (name required, data ≤64KiB)")
	}
	now := nowMS()
	if _, err := d.Exec(`INSERT INTO display_presets (show_id, name, data, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (show_id, name) DO UPDATE SET data = ?, updated_at = ?`,
		showID, name, data, now, data, now); err != nil {
		return DisplayPreset{}, fmt.Errorf("timerpi: save preset: %w", err)
	}
	var p DisplayPreset
	if err := d.Get(&p, `SELECT id, show_id, name, data, updated_at FROM display_presets WHERE show_id = ? AND name = ?`, showID, name); err != nil {
		return DisplayPreset{}, fmt.Errorf("timerpi: read preset: %w", err)
	}
	return p, nil
}

// ListPresets returns the show's presets by name.
func (d *DB) ListPresets(showID int64) ([]DisplayPreset, error) {
	var out []DisplayPreset
	err := d.Select(&out, `SELECT id, show_id, name, data, updated_at FROM display_presets WHERE show_id = ? ORDER BY name`, showID)
	if err != nil {
		return nil, fmt.Errorf("timerpi: list presets: %w", err)
	}
	if out == nil {
		out = []DisplayPreset{}
	}
	return out, nil
}

// GetPreset fetches one preset by id (scoped to the show).
func (d *DB) GetPreset(showID, id int64) (DisplayPreset, error) {
	var p DisplayPreset
	err := d.Get(&p, `SELECT id, show_id, name, data, updated_at FROM display_presets WHERE show_id = ? AND id = ?`, showID, id)
	if err != nil {
		return DisplayPreset{}, fmt.Errorf("timerpi: get preset: %w", err)
	}
	return p, nil
}

// DeletePreset removes one preset (scoped to the show).
func (d *DB) DeletePreset(showID, id int64) error {
	if _, err := d.Exec(`DELETE FROM display_presets WHERE show_id = ? AND id = ?`, showID, id); err != nil {
		return fmt.Errorf("timerpi: delete preset: %w", err)
	}
	return nil
}
