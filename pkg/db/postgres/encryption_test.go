//go:build integration

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

type contact struct {
	ID    uint
	Email string `gorm:"serializer:encrypted"`
}

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

func storedEmail(t *testing.T, capability db.Database, id uint) sql.NullString {
	t.Helper()
	var stored sql.NullString
	if err := capability.DB().Raw("SELECT email FROM contacts WHERE id = ?", id).Row().Scan(&stored); err != nil {
		t.Fatalf("reading the column with native SQL: %v", err)
	}
	return stored
}

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

// TestEncryptedWritesSealOnThisEngine runs the write paths the root package's cases hold SQLite to
// against this engine.
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

func assertRowsRefused(t *testing.T, statement *gorm.DB) {
	t.Helper()
	rows, err := statement.Rows()
	if err != nil {
		return
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
	}
	t.Errorf("Rows over the model reading the sealed column went through (iteration ended with %v)", rows.Err())
}

// TestEncryptedReadsOnThisEngine holds the read side to the same rules: a read that would hand back
// the ciphertext is refused, whether its destination is on the statement or picked after it.
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
	if err := handle.Model(&contact{}).Select("email").Scan(&emails).Error; err == nil {
		t.Errorf("scanning the sealed column over the model went through and handed back %v", emails)
	}
	assertRowsRefused(t, handle.Model(&contact{}))

	rows, err := handle.Raw("SELECT * FROM contacts").Rows()
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

// TestNamesThisEngineResolvesToTheSealedColumn holds the sealing to the column PostgreSQL resolves
// a name to.
func TestNamesThisEngineResolvesToTheSealedColumn(t *testing.T) {
	capability := encryptedAssembly(t)
	handle := capability.DB()
	row := contact{Email: "first@example.com"}
	if err := handle.Create(&row).Error; err != nil {
		t.Fatalf("creating the row: %v", err)
	}

	if err := handle.Model(&row).Update(`"email"`, "quoted@example.com").Error; err != nil {
		t.Fatalf("updating the column named in double quotes: %v", err)
	}
	assertSealedOnThisEngine(t, capability, row.ID, "quoted@example.com")

	if err := handle.Model(&contact{}).Where("id = ?", row.ID).
		Clauses(clause.Set{{Column: clause.Column{Name: `"email"`, Raw: true}, Value: "raw@example.com"}}).
		Updates(map[string]any{}).Error; err != nil {
		t.Fatalf("updating through a raw column in double quotes: %v", err)
	}
	assertSealedOnThisEngine(t, capability, row.ID, "raw@example.com")

	inserted := map[string]any{"id": 99, `"email"`: "inserted@example.com"}
	if err := handle.Model(&contact{}).Create(inserted).Error; err != nil {
		t.Fatalf("inserting from a map keyed by the quoted column: %v", err)
	}
	assertSealedOnThisEngine(t, capability, 99, "inserted@example.com")

	escaped := `U&"\0065mail"`
	err := handle.Model(&contact{}).Where("id = ?", row.ID).
		Clauses(clause.Set{{Column: clause.Column{Name: escaped, Raw: true}, Value: "clear@example.com"}}).
		Updates(map[string]any{}).Error
	if err == nil {
		t.Error("a raw column written with a Unicode escape went through")
	}
	assertSealedOnThisEngine(t, capability, row.ID, "raw@example.com")
}

type caseSpelledContact struct {
	ID     uint
	Shadow string `gorm:"column:EMAIL"`
	Email  string `gorm:"serializer:encrypted"`
}

func caseSpelledColumns(t *testing.T, capability db.Database, id uint) (shadow, email string) {
	t.Helper()
	row := capability.DB().Raw(`SELECT "EMAIL", email FROM case_spelled_contacts WHERE id = ?`, id).Row()
	if err := row.Scan(&shadow, &email); err != nil {
		t.Fatalf("reading the two columns with native SQL: %v", err)
	}
	return shadow, email
}

func assertCaseSpelled(t *testing.T, capability db.Database, id uint, wantShadow, wantEmail string) {
	t.Helper()
	shadow, email := caseSpelledColumns(t, capability, id)
	if shadow != wantShadow {
		t.Errorf("the EMAIL column holds %q, want %q in clear", shadow, wantShadow)
	}
	if strings.Contains(email, "@example.com") {
		t.Errorf("the email column holds %q, which is in clear", email)
	}
	var read caseSpelledContact
	if err := capability.DB().First(&read, id).Error; err != nil {
		t.Fatalf("reading the row back through the model: %v", err)
	}
	if read.Shadow != wantShadow || read.Email != wantEmail {
		t.Errorf("the model reads back %+v, want Shadow %q and Email %q", read, wantShadow, wantEmail)
	}
}

// TestColumnsThatDifferInLetterCaseAreTwoColumnsOnThisEngine covers a model whose unsealed column
// differs from its sealed one only in letter case.
func TestColumnsThatDifferInLetterCaseAreTwoColumnsOnThisEngine(t *testing.T) {
	capability := encryptedAssembly(t)
	handle := capability.DB()
	if err := handle.AutoMigrate(&caseSpelledContact{}); err != nil {
		t.Fatalf("creating the table: %v", err)
	}

	row := caseSpelledContact{Shadow: "shadow-1", Email: "sealed-1@example.com"}
	if err := handle.Create(&row).Error; err != nil {
		t.Fatalf("an insert of the model was refused: %v", err)
	}
	assertCaseSpelled(t, capability, row.ID, "shadow-1", "sealed-1@example.com")

	if err := handle.Save(&caseSpelledContact{ID: row.ID, Shadow: "shadow-2", Email: "sealed-2@example.com"}).Error; err != nil {
		t.Fatalf("a save of the model was refused: %v", err)
	}
	assertCaseSpelled(t, capability, row.ID, "shadow-2", "sealed-2@example.com")

	if err := handle.Model(&row).Updates(caseSpelledContact{Shadow: "shadow-3", Email: "sealed-3@example.com"}).Error; err != nil {
		t.Fatalf("an update from the model's struct was refused: %v", err)
	}
	assertCaseSpelled(t, capability, row.ID, "shadow-3", "sealed-3@example.com")

	if err := handle.Model(&row).UpdateColumns(caseSpelledContact{Shadow: "shadow-4", Email: "sealed-4@example.com"}).Error; err != nil {
		t.Fatalf("UpdateColumns from the model's struct was refused: %v", err)
	}
	assertCaseSpelled(t, capability, row.ID, "shadow-4", "sealed-4@example.com")

	if err := handle.Model(&row).Updates(map[string]any{"Shadow": "shadow-5"}).Error; err != nil {
		t.Fatalf("an update naming the unsealed field by key failed: %v", err)
	}
	assertCaseSpelled(t, capability, row.ID, "shadow-5", "sealed-4@example.com")

	if err := handle.Model(&row).Updates(map[string]any{"EMAIL": "shadow-6"}).Error; err != nil {
		t.Fatalf("an update naming the unsealed column by key failed: %v", err)
	}
	assertCaseSpelled(t, capability, row.ID, "shadow-6", "sealed-4@example.com")

	if err := handle.Model(&caseSpelledContact{}).Where("id = ?", row.ID).
		Clauses(clause.Set{{Column: clause.Column{Name: `"EMAIL"`, Raw: true}, Value: "shadow-7"}}).
		Updates(map[string]any{}).Error; err != nil {
		t.Fatalf("a SET clause on the quoted unsealed column failed: %v", err)
	}
	assertCaseSpelled(t, capability, row.ID, "shadow-7", "sealed-4@example.com")

	if err := handle.Model(&caseSpelledContact{}).Where("id = ?", row.ID).
		Clauses(clause.Set{{Column: clause.Column{Name: "EMAIL", Raw: true}, Value: "folded@example.com"}}).
		Updates(map[string]any{}).Error; err != nil {
		t.Fatalf("a SET clause on a raw column this engine folds to the sealed one failed: %v", err)
	}
	assertCaseSpelled(t, capability, row.ID, "shadow-7", "folded@example.com")

	var shadows []string
	if err := handle.Model(&caseSpelledContact{}).Pluck("Shadow", &shadows).Error; err != nil {
		t.Errorf("plucking the unsealed field was refused: %v", err)
	} else if len(shadows) != 1 || shadows[0] != "shadow-7" {
		t.Errorf("plucking the unsealed field gave %v, want [shadow-7]", shadows)
	}
	var columns []map[string]any
	if err := handle.Model(&caseSpelledContact{}).Select("EMAIL").Find(&columns).Error; err != nil {
		t.Errorf("reading the unsealed column into a map was refused: %v", err)
	} else if len(columns) != 1 || columns[0]["EMAIL"] != "shadow-7" {
		t.Errorf("reading the unsealed column into a map gave %v, want its plaintext", columns)
	}
	var folded []map[string]any
	if err := handle.Model(&caseSpelledContact{}).Select("id, EMAIL").Find(&folded).Error; err == nil {
		t.Errorf("reading a raw column this engine folds to the sealed one into a map went through and "+
			"handed back %v", folded)
	}
}

// longColumn is a column name longer than the 63 bytes this engine keeps of an
// identifier; longSpelling is another name with the same first 63 bytes.
const (
	longColumn   = "a_sealed_column_whose_name_runs_on_past_what_this_engine_keeps_of_it"
	longSpelling = "a_sealed_column_whose_name_runs_on_past_what_this_engine_keeps_another_way"
)

type longNamedContact struct {
	ID     uint
	Secret string `gorm:"column:a_sealed_column_whose_name_runs_on_past_what_this_engine_keeps_of_it;serializer:encrypted"`
}

// longNamedView declares the sealed column under the name this engine keeps of
// it, which is the name this engine gives the column in a result: GORM cannot
// scan the column back into longNamedContact, whose field carries the whole
// name, so a case reads the row through this struct.
type longNamedView struct {
	ID     uint
	Secret string `gorm:"column:a_sealed_column_whose_name_runs_on_past_what_this_engine_keeps_;serializer:encrypted"`
}

// TestANameThisEngineCutsShortToTheSealedColumnIsSealed covers a name that reaches the sealed
// column only because this engine keeps the first 63 bytes of an identifier.
func TestANameThisEngineCutsShortToTheSealedColumnIsSealed(t *testing.T) {
	if len(longColumn) <= 63 || len(longSpelling) <= 63 || longColumn[:63] != longSpelling[:63] {
		t.Fatalf("the two names have to share their first 63 bytes and run past them")
	}
	capability := encryptedAssembly(t)
	handle := capability.DB()
	if err := handle.AutoMigrate(&longNamedContact{}); err != nil {
		t.Fatalf("creating the table: %v", err)
	}
	row := longNamedContact{Secret: "first@example.com"}
	if err := handle.Create(&row).Error; err != nil {
		t.Fatalf("creating the row: %v", err)
	}

	if err := handle.Model(&row).Updates(map[string]any{longSpelling: "second@example.com"}).Error; err != nil {
		t.Fatalf("updating through the other spelling failed: %v", err)
	}
	var stored string
	if err := handle.Raw(`SELECT "`+longColumn[:63]+`" FROM long_named_contacts WHERE id = ?`, row.ID).
		Row().Scan(&stored); err != nil {
		t.Fatalf("reading the column with native SQL: %v", err)
	}
	if strings.Contains(stored, "@example.com") {
		t.Errorf("the sealed column holds %q, which is in clear", stored)
	}
	var read longNamedView
	if err := handle.Table("long_named_contacts").First(&read, row.ID).Error; err != nil ||
		read.Secret != "second@example.com" {
		t.Errorf("the row reads back %+v with %v, want second@example.com", read, err)
	}

	var columns []map[string]any
	if err := handle.Model(&longNamedContact{}).Select(longSpelling).Find(&columns).Error; err == nil {
		t.Errorf("reading the sealed column under the other spelling into a map went through and handed "+
			"back %v", columns)
	}
}

type lowerView struct {
	ID    uint
	Lower string
}

// TestAReadUnderAnotherNameOnThisEngine covers the renamed reads where this engine's naming decides
// the receiving field: an unaliased call is named after its function.
func TestAReadUnderAnotherNameOnThisEngine(t *testing.T) {
	capability := encryptedAssembly(t)
	handle := capability.DB()
	if err := handle.Create(&contact{Email: "first@example.com"}).Error; err != nil {
		t.Fatalf("creating the row: %v", err)
	}

	var lowered []lowerView
	if err := handle.Model(&contact{}).Select("id, lower(email)").Find(&lowered).Error; err == nil {
		t.Errorf("reading lower(email) into a field named after the function went through and handed "+
			"back %+v", lowered)
	}
	var renamed []lowerView
	if err := handle.Model(&contact{}).Select("id, email AS lower").Find(&renamed).Error; err == nil {
		t.Errorf("reading the sealed column under an alias went through and handed back %+v", renamed)
	}
}
