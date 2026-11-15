package routes

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"timerpi/boards"
	"timerpi/timerpi"
)

type screenAssignmentMode int

const (
	configureScreen screenAssignmentMode = iota
	captureScreen
	selectScreenTemplate
	restoreScreenPreset
	copyScreenLook
)

// An assignment carries intent; callers do not coordinate storage setters.
// Nil fields retain the current value, including older preset omissions.
type screenAssignment struct {
	Mode        screenAssignmentMode
	Theme       *string
	BoardID     *int64
	Room        *string
	Template    *string
	Kind        *string
	Rotation    *int
	WaitingID   int64
	WaitingCode string
}

type screenAssignmentInputError string

func (e screenAssignmentInputError) Error() string { return string(e) }

var errScreenAlreadyCaptured = errors.New("someone else just set this screen up")

func screenAssignmentError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	var input screenAssignmentInputError
	if errors.As(err, &input) {
		status = http.StatusBadRequest
	} else if errors.Is(err, errScreenAlreadyCaptured) {
		status = http.StatusConflict
	}
	c.JSON(status, gin.H{"ok": false, "error": err.Error()})
}

// assignScreen resolves and persists one complete assignment. A capture claim
// commits with that assignment, so a polling display cannot hop to half a look.
// Publication remains with the caller and only follows a successful commit.
func (d *Deps) assignScreen(showID int64, name string, change screenAssignment) (timerpi.Screen, error) {
	name = timerpi.SanitizeScreenName(name)
	if name == "" {
		return timerpi.Screen{}, screenAssignmentInputError("bad screen name")
	}
	if change.Theme != nil && !screenThemeOK(*change.Theme) {
		return timerpi.Screen{}, screenAssignmentInputError("bad theme name")
	}
	if change.BoardID != nil && !d.boardKnown(showID, *change.BoardID) {
		return timerpi.Screen{}, screenAssignmentInputError("unknown board")
	}
	tx, err := d.Store.Beginx()
	if err != nil {
		return timerpi.Screen{}, err
	}
	defer tx.Rollback()
	if change.WaitingID != 0 {
		res, err := tx.Exec(`UPDATE waiting_screens SET assigned = ?, screen = ? WHERE id = ? AND (assigned = '' OR assigned = ?)`,
			change.WaitingCode, name, change.WaitingID, change.WaitingCode)
		if err != nil {
			return timerpi.Screen{}, err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return timerpi.Screen{}, err
		}
		if count == 0 {
			return timerpi.Screen{}, errScreenAlreadyCaptured
		}
	}
	current := timerpi.Screen{ShowID: showID, Name: name}
	err = tx.Get(&current, `SELECT show_id, name, theme, board_id, room, last_seen, kind, rotation, template FROM screens WHERE show_id = ? AND name = ?`, showID, name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return timerpi.Screen{}, err
	}
	next, err := resolveScreenAssignment(current, change)
	if err != nil {
		return timerpi.Screen{}, err
	}
	next.LastSeen = time.Now().UnixMilli()
	_, err = tx.Exec(`INSERT INTO screens (show_id, name, theme, board_id, room, last_seen, kind, rotation, template)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT (show_id, name) DO UPDATE SET
		theme = excluded.theme, board_id = excluded.board_id, room = excluded.room, last_seen = excluded.last_seen,
		kind = excluded.kind, rotation = excluded.rotation, template = excluded.template`,
		showID, name, next.Theme, next.BoardID, next.Room, next.LastSeen, next.Kind, next.Rotation, next.Template)
	if err != nil {
		return timerpi.Screen{}, err
	}
	if err := tx.Commit(); err != nil {
		return timerpi.Screen{}, err
	}
	return next, nil
}

func resolveScreenAssignment(current timerpi.Screen, change screenAssignment) (timerpi.Screen, error) {
	next := current
	if change.Theme != nil {
		next.Theme = *change.Theme
	}
	if change.Room != nil {
		next.Room = timerpi.SanitizeScreenName(*change.Room)
	}
	if change.BoardID != nil {
		next.BoardID = *change.BoardID
		if next.BoardID > 0 {
			next.Template = ""
		}
	}
	if change.Template != nil && !(next.BoardID > 0 && (change.Mode == restoreScreenPreset || change.Mode == copyScreenLook)) {
		key := ""
		if change.Mode == copyScreenLook {
			key = *change.Template // matching copies stored look, including legacy keys
		} else if strings.TrimSpace(*change.Template) != "" {
			var known bool
			key, known = templateKey(*change.Template)
			if !known {
				if change.Mode != restoreScreenPreset {
					return timerpi.Screen{}, screenAssignmentInputError("unknown template")
				}
				key = next.Template // old presets ignored unknown built-ins
			}
		}
		next.Template, next.BoardID = key, 0
	}
	look := change.Kind != nil || change.Rotation != nil
	if change.Mode == restoreScreenPreset {
		look = change.Kind != nil && change.Rotation != nil && timerpi.ValidScreenKind(*change.Kind) && timerpi.ValidRotation(*change.Rotation)
	}
	if look {
		if change.Kind != nil {
			next.Kind = *change.Kind
		}
		if change.Rotation != nil {
			next.Rotation = *change.Rotation
		}
		if !timerpi.ValidScreenKind(next.Kind) {
			return timerpi.Screen{}, screenAssignmentInputError("unknown display type " + strconv.Quote(next.Kind))
		}
		if !timerpi.ValidRotation(next.Rotation) {
			return timerpi.Screen{}, screenAssignmentInputError("rotation must be 0, 90, 180 or 270")
		}
	}
	switch change.Mode {
	case configureScreen:
		if look && next.BoardID == 0 && (next.Template != "" || next.Kind != "") {
			next.Template = boards.FitTemplate(next.Template, next.Kind)
		}
	case captureScreen:
		if change.Template == nil && next.BoardID == 0 && next.Kind != "" {
			next.Template = boards.FitTemplate("", next.Kind)
		}
	case selectScreenTemplate:
		if next.Kind == "" {
			for _, template := range boards.Templates() {
				if template.Key == next.Template {
					next.Kind = template.Kind
					break
				}
			}
		}
	}
	return next, nil
}

var screenThemeRe = regexp.MustCompile(`^[a-z0-9_-]{0,40}$`)

// screenThemeOK: "" (event default) or a theme slug — never ftl's
// core/tokens layers, which are not themes (REPORT #18).
func screenThemeOK(t string) bool {
	return screenThemeRe.MatchString(t) && t != "core" && t != "tokens"
}

// boardKnown reports whether bid names a board of this show (assignments
// must not strand a locked TV on a 404 ?board= page).
func (d *Deps) boardKnown(showID, bid int64) bool {
	if bid <= 0 {
		return bid == 0
	}
	_, err := boards.GetBoard(d.Store.DB, showID, bid)
	return err == nil
}

// templateKey normalises a built-in key; ok=false when unknown.
func templateKey(raw string) (string, bool) {
	k := strings.ToLower(strings.TrimSpace(raw))
	if cur, old := boards.LegacyTemplateKeys()[k]; old {
		k = cur // "room-portrait" and friends: one key holds both versions now
	}
	_, ok := boards.TemplateLayouts()[k]
	return k, ok
}
