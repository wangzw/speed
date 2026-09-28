package db

import (
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// These cases hold the sealing and the read guard to the column a name reaches
// in the database rather than to the name as GORM resolves it. GORM writes a
// name it cannot resolve as it stands, and the database then decides which
// column it is: SQLite matches identifiers without regard to letter case, and
// either engine takes a name that arrives already quoted as its quoted content.
// A key GORM leaves unresolved can therefore still reach a sealed column, and a
// write that sealed only what GORM resolved would store that value in clear.

// TestAKeyTheDatabaseResolvesToTheSealedColumnIsSealed covers every write path
// whose column is named by the caller, with a name GORM does not resolve and
// the database does: the email column under another letter case, or quoted.
func TestAKeyTheDatabaseResolvesToTheSealedColumnIsSealed(t *testing.T) {
	cases := []struct {
		name  string
		write func(db *gorm.DB, row *encryptedModel, email string) error
	}{
		{"Updates with a key in another letter case", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(row).Updates(map[string]any{"eMaIl": email}).Error
		}},
		{"Update with a column in another letter case", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(row).Update("eMaIl", email).Error
		}},
		{"Update with a column already quoted", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(row).Update("`email`", email).Error
		}},
		{"UpdateColumns with a key in another letter case", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(&encryptedModel{}).Where("id = ?", row.ID).
				UpdateColumns(map[string]any{"EmAiL": email}).Error
		}},
		{"a SET clause naming a raw column", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(&encryptedModel{}).Where("id = ?", row.ID).
				Clauses(clause.Set{{Column: clause.Column{Name: " eMaIl", Raw: true}, Value: email}}).
				Updates(map[string]any{}).Error
		}},
		{"a SET clause naming a quoted column", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(&encryptedModel{}).Where("id = ?", row.ID).
				Clauses(clause.Set{{Column: clause.Column{Name: "`EMAIL`"}, Value: email}}).
				Updates(map[string]any{}).Error
		}},
		{"an upsert assignment in another letter case", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				DoUpdates: clause.Assignments(map[string]any{"eMaIl": email}),
			}).Create(&encryptedModel{ID: row.ID, Email: "inserted@example.com"}).Error
		}},
		{"an upsert copying the inserted value in another letter case", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				DoUpdates: clause.AssignmentColumns([]string{"eMaIl"}),
			}).Create(&encryptedModel{ID: row.ID, Email: email}).Error
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
			createEncryptedTable(t, product)
			row := writtenRow(t, product)

			const email = "resolved@example.com"
			if err := c.write(product.DB(), &row, email); err != nil {
				t.Fatalf("the write failed: %v", err)
			}
			assertSealed(t, product, row.ID, email)
		})
	}
}

// TestAnInsertKeyTheDatabaseResolvesToTheSealedColumnIsSealed covers Create
// over a map whose key reaches the sealed column in another letter case.
func TestAnInsertKeyTheDatabaseResolvesToTheSealedColumnIsSealed(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)

	const email = "inserted@example.com"
	values := map[string]any{"id": 7, "EmAiL": email}
	if err := product.DB().Model(&encryptedModel{}).Create(values).Error; err != nil {
		t.Fatalf("inserting from the map: %v", err)
	}
	assertSealed(t, product, 7, email)
	if values["EmAiL"] != email {
		t.Errorf("the caller's map holds %#v after the insert, want the plaintext it put there", values["EmAiL"])
	}
}

// TestARawColumnThisModuleCannotReadIsRefused covers the assignment whose
// column is raw text that is not an identifier. Which column it reaches is the
// database's to decide and nothing this module reads, so on a model with sealed
// fields the write is refused, and the column keeps what it held; a raw column
// that is a plain identifier still goes through.
func TestARawColumnThisModuleCannotReadIsRefused(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	row := writtenRow(t, product)
	before := storedColumn(t, product, row.ID, "email")

	for _, column := range []string{"lower(email)", `U&"\0065mail"`, "email[1]"} {
		err := product.DB().Model(&encryptedModel{}).Where("id = ?", row.ID).
			Clauses(clause.Set{{Column: clause.Column{Name: column, Raw: true}, Value: "clear@example.com"}}).
			Updates(map[string]any{}).Error
		if err == nil || !strings.Contains(err.Error(), strconv.Quote(column)) {
			t.Errorf("a SET clause on the raw column %q gave %v, want a refusal naming it", column, err)
		}
	}
	if after := storedColumn(t, product, row.ID, "email"); after != before {
		t.Errorf("the column changed from %v to %v under a refused write", before, after)
	}

	if err := product.DB().Model(&encryptedModel{}).Where("id = ?", row.ID).
		Clauses(clause.Set{{Column: clause.Column{Name: "email_index", Raw: true}, Value: "digest"}}).
		Updates(map[string]any{}).Error; err != nil {
		t.Errorf("a SET clause on a raw column that is not sealed was refused: %v", err)
	}
}

// titledModel has a column whose name is also a field name, which is what lets
// GORM resolve another struct's field onto it by that field's name.
type titledModel struct {
	ID    uint
	Title string `gorm:"column:Title"`
	Email string `gorm:"serializer:encrypted"`
}

// titlePatch writes its Title field into the column EMAIL. GORM finds the field
// by the model's column name Title and writes the field's own column, and
// SQLite resolves EMAIL to the model's sealed email column.
type titlePatch struct {
	Title string `gorm:"column:EMAIL"`
}

// TestAnUpdateFromAnotherStructIsJudgedByTheColumnItWrites covers the struct
// update whose field reaches the sealed column under a name that differs from
// it only in letter case: the value would be stored in clear, and the update is
// refused as the exact name is.
func TestAnUpdateFromAnotherStructIsJudgedByTheColumnItWrites(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	if err := product.DB().AutoMigrate(&titledModel{}); err != nil {
		t.Fatalf("creating the model's table: %v", err)
	}
	row := titledModel{Title: "a title", Email: "first@example.com"}
	if err := product.DB().Create(&row).Error; err != nil {
		t.Fatalf("writing the row: %v", err)
	}

	err := product.DB().Model(&titledModel{}).Where("id = ?", row.ID).
		Updates(titlePatch{Title: "clear@example.com"}).Error
	if err == nil || !strings.Contains(err.Error(), "Email") {
		t.Errorf("an update writing EMAIL from an unmarked field gave %v, want a refusal naming the field", err)
	}
	var read titledModel
	if err := product.DB().First(&read, row.ID).Error; err != nil {
		t.Fatalf("reading the row back through the model: %v", err)
	}
	if read.Email != "first@example.com" {
		t.Errorf("the model reads the email back as %q after a refused update", read.Email)
	}
}

// titleView is a struct whose text field takes whatever a query names title.
type titleView struct {
	ID    uint
	Title string
}

// lowerView takes the result column PostgreSQL names after an unaliased call.
type lowerView struct {
	ID    uint
	Lower string
}

// lengthView takes a number computed from the sealed column.
type lengthView struct {
	ID          uint
	EmailLength int
	Missing     bool
}

// TestAReadUnderAnotherNameIsRefused covers the reads that rename the sealed
// column, or compute from it, into a text field without the serializer — the
// model's own unmarked field among them. GORM scans a result column into the
// field its name resolves to, so each of these would hand back the ciphertext
// as that field's value.
func TestAReadUnderAnotherNameIsRefused(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	writtenRow(t, product)

	cases := []struct {
		name string
		read func() (any, error)
	}{
		{"into the model's unmarked field", func() (any, error) {
			var rows []encryptedModel
			err := product.DB().Model(&encryptedModel{}).Select("id, email AS email_index").Find(&rows).Error
			return rows, err
		}},
		{"into another struct", func() (any, error) {
			var rows []titleView
			err := product.DB().Model(&encryptedModel{}).Select("id", "email AS title").Find(&rows).Error
			return rows, err
		}},
		{"with the alias written without AS", func() (any, error) {
			var rows []titleView
			err := product.DB().Model(&encryptedModel{}).Select("id, email title").Find(&rows).Error
			return rows, err
		}},
		{"with a quoted alias", func() (any, error) {
			var rows []titleView
			err := product.DB().Model(&encryptedModel{}).Select("id, email AS \"Title\"").Find(&rows).Error
			return rows, err
		}},
		{"with the alias set apart on the column", func() (any, error) {
			var rows []titleView
			err := product.DB().Model(&encryptedModel{}).Clauses(clause.Select{Columns: []clause.Column{
				{Name: "id"}, {Name: "email", Alias: "title"},
			}}).Find(&rows).Error
			return rows, err
		}},
		{"computed from the column", func() (any, error) {
			var rows []titleView
			err := product.DB().Model(&encryptedModel{}).Select("id, lower(email) AS title").Find(&rows).Error
			return rows, err
		}},
		{"an unaliased call PostgreSQL names after the function", func() (any, error) {
			var rows []lowerView
			err := product.DB().Model(&encryptedModel{}).Select("id, lower(email)").Find(&rows).Error
			return rows, err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.read()
			if err == nil {
				t.Fatalf("the read went through and handed back %+v, which carries the stored text of a "+
					"sealed column rather than its value", got)
			}
			if !strings.Contains(err.Error(), "Email") {
				t.Errorf("the refusal does not name the field: %v", err)
			}
		})
	}
}

// TestAReadUnderAnotherNameThatCannotCarryTheTextGoesThrough keeps that
// refusal to the fields it is about: a number or a truth value computed from
// the sealed column is not its text, the model reads the column under its own
// name, and a sealed column renamed onto another sealed field is opened by it.
func TestAReadUnderAnotherNameThatCannotCarryTheTextGoesThrough(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	row := writtenRow(t, product)

	var lengths []lengthView
	if err := product.DB().Model(&encryptedModel{}).
		Select("id, length(email) AS email_length, email IS NULL AS missing").Find(&lengths).Error; err != nil {
		t.Errorf("reading numbers computed from the sealed column was refused: %v", err)
	}
	var own []encryptedModel
	if err := product.DB().Model(&encryptedModel{}).Select("id, email").Find(&own).Error; err != nil {
		t.Errorf("reading the sealed column under its own name into the model was refused: %v", err)
	}
	var swapped []encryptedModel
	if err := product.DB().Model(&encryptedModel{}).Select("id, nickname AS email").Find(&swapped).Error; err != nil {
		t.Fatalf("reading one sealed column into another sealed field was refused: %v", err)
	}
	if len(swapped) != 1 || swapped[0].Email != *row.Nickname {
		t.Errorf("the sealed field reads %+v, want the nickname it was selected from opened", swapped)
	}
}
