package dbsuite

import (
	"testing"

	"gorm.io/gorm"
)

// refusedCommitSchema is the pair of tables the refused-commit cases write into.
// The foreign key is checked at COMMIT, so a transaction over a missing owner
// runs every statement and is refused by its commit alone.
var refusedCommitSchema = []string{
	"CREATE TABLE commit_owners (id INTEGER PRIMARY KEY)",
	"CREATE TABLE commit_pets (id INTEGER PRIMARY KEY, " +
		"owner_id INTEGER REFERENCES commit_owners (id) DEFERRABLE INITIALLY DEFERRED)",
}

// Refusal has the database refuse one COMMIT on a handle and returns the error
// the transaction reported. A statement refused before the commit fails the
// test instead.
type Refusal func(t *testing.T, handle *gorm.DB) error

// RefusedCommit holds a handle an engine's module delivered to what the database
// capability states for a COMMIT the database refuses, over a deferred foreign
// key found violated at the COMMIT. The handle must enforce foreign keys.
func RefusedCommit(t *testing.T, handle, observer *gorm.DB) {
	t.Helper()
	RefusedCommitBy(t, handle, observer, deferredForeignKey)
}

func deferredForeignKey(t *testing.T, handle *gorm.DB) error {
	t.Helper()
	var early error
	err := handle.Transaction(func(tx *gorm.DB) error {
		for _, statement := range []string{
			"INSERT INTO commit_owners (id) VALUES (1)",
			"INSERT INTO commit_pets (id, owner_id) VALUES (1, 999)",
		} {
			if early = tx.Exec(statement).Error; early != nil {
				return early
			}
		}
		return nil
	})
	if early != nil {
		t.Fatalf("a statement of the transaction was refused before its commit: %v", early)
	}
	return err
}

// RefusedCommitBy holds a handle, held to one connection, to the contract over a refusal the caller brings about: the transaction took no effect and the connection returns outside any transaction. observer is another pool on the same database, through which a write after the refusal must be seen; it is nil for a database private to its one connection.
func RefusedCommitBy(t *testing.T, handle, observer *gorm.DB, refuse Refusal) {
	t.Helper()
	for _, statement := range refusedCommitSchema {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("creating the tables the case writes into: %v", err)
		}
	}

	if err := refuse(t, handle); err == nil {
		t.Fatal("the commit the database was to refuse went through")
	}

	var owners int64
	if err := handle.Raw("SELECT count(*) FROM commit_owners WHERE id = 1").Scan(&owners).Error; err != nil {
		t.Fatalf("reading after the refused commit: %v", err)
	}
	if owners != 0 {
		t.Errorf("the connection reads back the owner the refused transaction wrote: it is still " +
			"inside that transaction")
	}

	if err := handle.Exec("INSERT INTO commit_owners (id) VALUES (2)").Error; err != nil {
		t.Fatalf("writing after the refused commit: %v", err)
	}
	if observer != nil {
		var seen int64
		if err := observer.Raw("SELECT count(*) FROM commit_owners WHERE id = 2").Scan(&seen).Error; err != nil {
			t.Fatalf("reading through another pool: %v", err)
		}
		if seen != 1 {
			t.Errorf("another pool does not see a write that reported success after the refused " +
				"commit: the write went into the transaction the refusal left open")
		}
	}

	if err := handle.Transaction(func(tx *gorm.DB) error {
		return tx.Exec("INSERT INTO commit_pets (id, owner_id) VALUES (2, 2)").Error
	}); err != nil {
		t.Errorf("a transaction begun after the refused commit failed: %v", err)
	}
}
