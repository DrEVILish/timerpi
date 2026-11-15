package timerpi

import (
	"testing"
	"time"
)

// Operator reads must not queue behind an audience write's commit.
func TestReadsContinueDuringUncommittedWrite(t *testing.T) {
	d := openTestDB(t)
	sh := mustCreateShow(t, d, "Committed")
	tx, err := d.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE shows SET title = 'Pending' WHERE id = ?`, sh.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		s, err := d.GetShow(sh.ID)
		if err == nil && s.Title != "Committed" {
			t.Errorf("uncommitted title visible: %q", s.Title)
		}
		if err == nil {
			_, err = d.ListShows()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(500 * time.Millisecond):
		tx.Rollback()
		<-done
		t.Fatal("operator reads waited for the writer connection")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s, err := d.GetShow(sh.ID)
	if err != nil || s.Title != "Pending" {
		t.Fatalf("committed title not visible: %+v %v", s, err)
	}
}

func TestReaderPoolCannotWriteAndCloses(t *testing.T) {
	d := openTestDB(t)
	if d.readers == nil {
		t.Fatal("file-backed WAL database has no readers")
	}
	if _, err := d.readers.Exec(`INSERT INTO settings(key,value) VALUES ('read-only-test','bad')`); err == nil {
		t.Fatal("reader accepted a write")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := d.readers.Ping(); err == nil {
		t.Fatal("read pool left open")
	}
	if err := d.DB.Ping(); err == nil {
		t.Fatal("write pool left open")
	}
}

func TestTransactionViewReadsItsOwnWrite(t *testing.T) {
	d := openTestDB(t)
	sh := mustCreateShow(t, d, "Before")
	err := d.WriteTransaction(func(view *DB) error {
		if _, err := view.Exec(`UPDATE shows SET title = 'Inside' WHERE id = ?`, sh.ID); err != nil {
			return err
		}
		got, err := view.GetShow(sh.ID)
		if err == nil && got.Title != "Inside" {
			t.Errorf("transaction read committed pool instead of own write: %q", got.Title)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
