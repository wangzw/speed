package dbsuite

import (
	"testing"

	"gorm.io/gorm"
)

// refusedCommitSchema is the pair of tables the refused-commit case writes
// into. A pet names its owner under a foreign key checked at COMMIT rather than
// at the statement, so a transaction over a missing owner runs every statement
// and is refused by its commit alone.
var refusedCommitSchema = []string{
	"CREATE TABLE commit_owners (id INTEGER PRIMARY KEY)",
	"CREATE TABLE commit_pets (id INTEGER PRIMARY KEY, " +
		"owner_id INTEGER REFERENCES commit_owners (id) DEFERRABLE INITIALLY DEFERRED)",
}

// RefusedCommit holds a handle an engine's module delivered to what the
// database capability states for a COMMIT the database refuses: the
// transaction took no effect, and the connection it ran on goes back to the
// pool outside any transaction. SQLite keeps the transaction open when it
// refuses a COMMIT while database/sql hands the connection back regardless,
// and PostgreSQL ends the transaction itself; the contract is the same on both,
// so every engine runs this case.
//
// The handle must be held to one connection, so that every statement after the
// refusal lands on the connection the refused transaction ran on, and it must
// enforce foreign keys; SQLite does so only on a connection that turned them
// on.
//
// observer is another pool on the same database, through which a write made
// after the refusal must be seen: that is what tells a committed write from one
// taken into a transaction left open, which the connection itself reads back
// either way. It is nil for a database that belongs to the one connection
// opening it, which no other pool reaches; the other two observations still
// hold that connection to the contract.
func RefusedCommit(t *testing.T, handle, observer *gorm.DB) {
	t.Helper()
	for _, statement := range refusedCommitSchema {
		if err := handle.Exec(statement).Error; err != nil {
			t.Fatalf("creating the tables the case writes into: %v", err)
		}
	}

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
	if err == nil {
		t.Fatal("the commit over a missing owner went through")
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
