package dbsuite

import (
	"testing"

	"gorm.io/gorm"
)

// concurrentWriters is how many transactions ReadThenWriteCommits runs at once:
// enough that each of them meets another holding the database's write lock on
// an engine that has only one.
const concurrentWriters = 16

// ReadThenWriteCommits holds the handle an engine's module delivers to
// committing every one of concurrent transactions that each read the table
// before they write to it — a check followed by a write, the most ordinary
// transaction there is. Each waits for the others within the engine's own lock
// wait; none is refused for having read first while another one writes.
func ReadThenWriteCommits(t *testing.T, handle *gorm.DB) {
	t.Helper()
	if err := handle.Exec("CREATE TABLE read_write_rows (id INTEGER PRIMARY KEY, seen INTEGER)").Error; err != nil {
		t.Fatalf("creating the table the case writes into: %v", err)
	}
	start := make(chan struct{})
	answers := make(chan error, concurrentWriters)
	for i := range concurrentWriters {
		go func() {
			<-start
			answers <- handle.Transaction(func(tx *gorm.DB) error {
				var seen int64
				if err := tx.Raw("SELECT count(*) FROM read_write_rows").Scan(&seen).Error; err != nil {
					return err
				}
				return tx.Exec("INSERT INTO read_write_rows (id, seen) VALUES (?, ?)", i+1, seen).Error
			})
		}()
	}
	close(start)
	var failed []error
	for range concurrentWriters {
		if err := <-answers; err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		t.Errorf("%d of %d concurrent transactions reading before they write failed, the first with: %v",
			len(failed), concurrentWriters, failed[0])
	}
	var rows int64
	if err := handle.Raw("SELECT count(*) FROM read_write_rows").Scan(&rows).Error; err != nil {
		t.Fatalf("reading the table back: %v", err)
	}
	if rows != concurrentWriters {
		t.Errorf("the table holds %d rows, want the %d the transactions wrote", rows, concurrentWriters)
	}
}
