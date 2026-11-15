package timerpi

import "database/sql"

// WriteTransaction gives fn a private database view. Its writes and cache
// invalidations become visible together, only after fn succeeds and commits.
// The view must not escape fn or be used by concurrent goroutines.
func (d *DB) WriteTransaction(fn func(*DB) error) error {
	tx, err := d.DB.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	view := &DB{DB: d.DB, tx: tx}
	if err := fn(view); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for id := range view.cueG.gen {
		d.cuesChanged(id)
	}
	for id := range view.air.gen {
		d.airDirty(id)
	}
	return nil
}

func (d *DB) Exec(query string, args ...any) (sql.Result, error) {
	if d.tx != nil {
		return d.tx.Exec(query, args...)
	}
	return d.DB.Exec(query, args...)
}

func (d *DB) Get(dest any, query string, args ...any) error {
	if d.tx != nil {
		return d.tx.Get(dest, query, args...)
	}
	if d.readers != nil {
		return d.readers.Get(dest, query, args...)
	}
	return d.DB.Get(dest, query, args...)
}

func (d *DB) Select(dest any, query string, args ...any) error {
	if d.tx != nil {
		return d.tx.Select(dest, query, args...)
	}
	if d.readers != nil {
		return d.readers.Select(dest, query, args...)
	}
	return d.DB.Select(dest, query, args...)
}
