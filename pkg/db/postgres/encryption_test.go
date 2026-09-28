package postgres_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/db"
	"github.com/vislake/speed/pkg/internal/pgpool/pgtest"
)

// contact is a model with one sealed column, the shape a host's PII takes.
type contact struct {
	ID    uint
	Email string `gorm:"serializer:encrypted"`
}

// encryptedAssembly starts a host on this engine with a root key configured
// and the contacts table in place.
func encryptedAssembly(t *testing.T) db.Database {
	t.Helper()
	capability, err := runAssemblyWith(t, context.Background(), map[string]any{
		"dsn":            pgtest.Acquire(t),
		"encryption-key": "the-root-key-of-the-postgres-cases",
	})
	if err != nil {
		t.Fatalf("starting a host with a root key: %v", err)
	}
	if err := capability.DB().AutoMigrate(&contact{}); err != nil {
		t.Fatalf("creating the contacts table: %v", err)
	}
	return capability
}

// storedEmail reads the email column of one row with native SQL, which is the
// only way to see what this engine holds rather than what the model makes of it.
func storedEmail(t *testing.T, capability db.Database, id uint) sql.NullString {
	t.Helper()
	var stored sql.NullString
	if err := capability.DB().Raw("SELECT email FROM contacts WHERE id = ?", id).Row().Scan(&stored); err != nil {
		t.Fatalf("reading the column with native SQL: %v", err)
	}
	return stored
}

// assertSealedOnThisEngine holds one row to a sealed column that reads back as
// the plaintext through the model.
func assertSealedOnThisEngine(t *testing.T, capability db.Database, id uint, want string) {
	t.Helper()
	if stored := storedEmail(t, capability, id); !stored.Valid || strings.Contains(stored.String, want) {
		t.Errorf("the email column holds %v, want a seal over %q", stored, want)
	}
	var read contact
	if err := capability.DB().First(&read, id).Error; err != nil {
		t.Fatalf("reading the row back through the model: %v", err)
	}
	if read.Email != want {
		t.Errorf("the model reads %q, want %q", read.Email, want)
	}
}

// TestEncryptedWritesSealOnThisEngine runs the write paths the root package's
// cases hold SQLite to against this engine, whose insert and upsert take a
// RETURNING clause and a different conflict syntax: the two engines deliver one
// capability and have to seal the same writes.
func TestEncryptedWritesSealOnThisEngine(t *testing.T) {
	capability := encryptedAssembly(t)
	handle := capability.DB()

	row := contact{Email: "first@example.com"}
	if err := handle.Create(&row).Error; err != nil {
		t.Fatalf("creating the row: %v", err)
	}
	if err := handle.Model(&row).Updates(map[string]any{"email": "patched@example.com"}).Error; err != nil {
		t.Fatalf("updating through a map: %v", err)
	}
	assertSealedOnThisEngine(t, capability, row.ID, "patched@example.com")
	if err := handle.Model(&row).Update("email", "updated@example.com").Error; err != nil {
		t.Fatalf("updating one column: %v", err)
	}
	assertSealedOnThisEngine(t, capability, row.ID, "updated@example.com")

	inserted := map[string]any{"email": "inserted@example.com"}
	if err := handle.Model(&contact{}).Create(inserted).Error; err != nil {
		t.Fatalf("inserting from a map: %v", err)
	}
	id, ok := inserted["id"]
	if !ok {
		t.Fatalf("the map carries no id after the insert: %#v", inserted)
	}
	if inserted["email"] != "inserted@example.com" {
		t.Errorf("the caller's map holds %#v after the insert, want the plaintext", inserted["email"])
	}
	var insertedID uint
	switch v := id.(type) {
	case int64:
		insertedID = uint(v)
	case uint:
		insertedID = v
	default:
		t.Fatalf("the id came back as %T", id)
	}
	assertSealedOnThisEngine(t, capability, insertedID, "inserted@example.com")

	if err := handle.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{"email": "upserted@example.com"}),
	}).Create(&contact{ID: row.ID, Email: "ignored@example.com"}).Error; err != nil {
		t.Fatalf("upserting with an assignment by value: %v", err)
	}
	assertSealedOnThisEngine(t, capability, row.ID, "upserted@example.com")

	if err := handle.Model(&row).Update("email", gorm.Expr("'clear@example.com'")).Error; err == nil {
		t.Error("an update writing an expression into the sealed column went through")
	}
	assertSealedOnThisEngine(t, capability, row.ID, "upserted@example.com")
}

// TestEncryptedReadsOnThisEngine holds the read side to the same rules: a
// read that would hand back the ciphertext is refused, and streaming rows with
// ScanRows on the delivered handle opens the field.
func TestEncryptedReadsOnThisEngine(t *testing.T) {
	capability := encryptedAssembly(t)
	handle := capability.DB()
	row := contact{Email: "first@example.com"}
	if err := handle.Create(&row).Error; err != nil {
		t.Fatalf("creating the row: %v", err)
	}

	var emails []string
	if err := handle.Model(&contact{}).Pluck("email", &emails).Error; err == nil {
		t.Errorf("plucking the sealed column went through and handed back %v", emails)
	}

	rows, err := handle.Model(&contact{}).Rows()
	if err != nil {
		t.Fatalf("opening the result set: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var scanned []contact
	for rows.Next() {
		var one contact
		if err := handle.ScanRows(rows, &one); err != nil {
			t.Fatalf("scanning with the delivered handle: %v", err)
		}
		scanned = append(scanned, one)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating the result set: %v", err)
	}
	if len(scanned) != 1 || scanned[0].Email != row.Email {
		t.Errorf("scanning with the delivered handle read %v, want the plaintext", scanned)
	}
}
