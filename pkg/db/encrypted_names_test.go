package db

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TestAKeyTheDatabaseResolvesToTheSealedColumnIsSealed covers every write path whose column is
// named by the caller, with a name GORM does not resolve and the database does.
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

// TestAnInsertKeyTheDatabaseResolvesToTheSealedColumnIsSealed covers Create over a map whose key
// reaches the sealed column in another letter case.
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

// TestARawColumnThisModuleCannotReadIsRefused covers the assignment whose column is raw text that
// is not an identifier.
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

type titledModel struct {
	ID    uint
	Title string `gorm:"column:Title"`
	Email string `gorm:"serializer:encrypted"`
}

type titlePatch struct {
	Title string `gorm:"column:EMAIL"`
}

// TestAnUpdateFromAnotherStructIsJudgedByTheColumnItWrites covers the struct update whose field
// reaches the sealed column under a name that differs from it only in letter case.
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

type spelledFirstModel struct {
	ID     uint
	Shadow string `gorm:"column:EMAIL"`
	Email  string `gorm:"serializer:encrypted"`
}

func (spelledFirstModel) TableName() string { return "spelled_models" }

type spelledLastModel struct {
	ID     uint
	Email  string `gorm:"serializer:encrypted"`
	Shadow string `gorm:"column:EMAIL"`
}

func (spelledLastModel) TableName() string { return "spelled_models" }

// createSpelledTable brings up the table both spelled models write into: AutoMigrate cannot build it, since SQLite refuses a table whose column names differ only in letter case.
func createSpelledTable(t *testing.T, product *database) {
	t.Helper()
	if err := product.DB().Exec("CREATE TABLE spelled_models (id INTEGER PRIMARY KEY, email TEXT)").Error; err != nil {
		t.Fatalf("creating the table the spelled models write into: %v", err)
	}
}

// storedEmail is what the email column of one spelled row holds, read around the model: "absent" when there is no such row.
func storedEmail(t *testing.T, product *database, id uint) string {
	t.Helper()
	var stored []sql.NullString
	if err := product.DB().Raw("SELECT email FROM spelled_models WHERE id = ?", id).Scan(&stored).Error; err != nil {
		t.Fatalf("reading the email column with native SQL: %v", err)
	}
	if len(stored) == 0 {
		return "absent"
	}
	return stored[0].String
}

// TestAWriteOfTheModelSpellingASealedColumnAnotherWayIsRefused covers a model that declares, beside
// a sealed field.
func TestAWriteOfTheModelSpellingASealedColumnAnotherWayIsRefused(t *testing.T) {
	const clear = "clear@example.com"
	cases := []struct {
		name  string
		write func(h *gorm.DB) error
	}{
		{"an insert of the model", func(h *gorm.DB) error {
			return h.Create(&spelledFirstModel{ID: 2, Shadow: clear, Email: "sealed@example.com"}).Error
		}},
		{"an insert leaving the unsealed field empty", func(h *gorm.DB) error {
			return h.Create(&spelledFirstModel{ID: 2, Email: "sealed@example.com"}).Error
		}},
		{"an insert of several rows", func(h *gorm.DB) error {
			return h.Create(&[]spelledFirstModel{{ID: 2, Shadow: clear}, {ID: 3, Shadow: clear}}).Error
		}},
		{"an insert of the other declaration order", func(h *gorm.DB) error {
			return h.Create(&spelledLastModel{ID: 2, Email: "sealed@example.com", Shadow: clear}).Error
		}},
		{"an upsert of the model", func(h *gorm.DB) error {
			return h.Clauses(clause.OnConflict{UpdateAll: true}).
				Create(&spelledFirstModel{ID: 1, Shadow: clear, Email: "sealed@example.com"}).Error
		}},
		{"an update from the model's struct", func(h *gorm.DB) error {
			return h.Model(&spelledFirstModel{ID: 1}).Updates(spelledFirstModel{Shadow: clear}).Error
		}},
		{"an update selecting the unsealed field", func(h *gorm.DB) error {
			return h.Model(&spelledFirstModel{ID: 1}).Select("Shadow").Updates(spelledFirstModel{}).Error
		}},
		{"UpdateColumns from the model's struct", func(h *gorm.DB) error {
			return h.Model(&spelledFirstModel{ID: 1}).UpdateColumns(spelledFirstModel{Shadow: clear}).Error
		}},
		{"a save of the other declaration order", func(h *gorm.DB) error {
			return h.Save(&spelledLastModel{ID: 1, Email: "sealed@example.com", Shadow: clear}).Error
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
			createSpelledTable(t, product)
			if err := product.DB().Omit("Shadow").Create(&spelledFirstModel{ID: 1, Email: "first@example.com"}).Error; err != nil {
				t.Fatalf("writing the row the case starts from: %v", err)
			}
			before := storedEmail(t, product, 1)

			err := c.write(product.DB())
			if err == nil {
				t.Fatalf("the write went through; the email column of row 1 holds %q and of row 2 %q",
					storedEmail(t, product, 1), storedEmail(t, product, 2))
			}
			if !strings.Contains(err.Error(), "Shadow") || !strings.Contains(err.Error(), "Email") {
				t.Errorf("the refusal does not name both fields: %v", err)
			}
			if after := storedEmail(t, product, 1); after != before {
				t.Errorf("the email column of row 1 went from %q to %q under a refused write", before, after)
			}
			if stored := storedEmail(t, product, 2); stored != "absent" {
				t.Errorf("a refused insert left a row 2 holding %q", stored)
			}
		})
	}
}

// TestAWriteOfTheModelThatLeavesTheOtherSpellingOutGoesThrough keeps that refusal to the writes it
// is about: one whose selection leaves the unsealed field out.
func TestAWriteOfTheModelThatLeavesTheOtherSpellingOutGoesThrough(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createSpelledTable(t, product)
	h := product.DB()

	if err := h.Omit("Shadow").Create(&spelledFirstModel{ID: 1, Shadow: "left out", Email: "one@example.com"}).Error; err != nil {
		t.Fatalf("an insert leaving the unsealed field out was refused: %v", err)
	}
	var read spelledFirstModel
	if err := h.First(&read, 1).Error; err != nil || read.Email != "one@example.com" {
		t.Errorf("the model reads back %+v with %v, want the sealed email one@example.com", read, err)
	}
	if err := h.Model(&spelledFirstModel{ID: 1}).Updates(spelledFirstModel{Email: "two@example.com"}).Error; err != nil {
		t.Fatalf("an update leaving the unsealed field empty was refused: %v", err)
	}
	if err := h.Model(&spelledFirstModel{ID: 1}).Updates(map[string]any{"Shadow": "three@example.com"}).Error; err != nil {
		t.Fatalf("an update naming the unsealed field by key was refused: %v", err)
	}
	stored := storedEmail(t, product, 1)
	if strings.Contains(stored, "@example.com") {
		t.Errorf("the email column holds %q, which is in clear", stored)
	}
	read = spelledFirstModel{}
	if err := h.First(&read, 1).Error; err != nil || read.Email != "three@example.com" {
		t.Errorf("the model reads back %+v with %v, want the sealed email three@example.com", read, err)
	}
}

type accentedModel struct {
	ID     uint
	Shadow string `gorm:"column:ÉMAIL"`
	Email  string `gorm:"column:émail;serializer:encrypted"`
}

// TestNamesThatDifferOutsideASCIIAreTwoColumnsOnSQLite holds the judgement to SQLite's own rule: a
// write of the model writes each of the two columns once and goes through.
func TestNamesThatDifferOutsideASCIIAreTwoColumnsOnSQLite(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	h := product.DB()
	if err := h.AutoMigrate(&accentedModel{}); err != nil {
		t.Fatalf("creating the model's table: %v", err)
	}

	if err := h.Create(&accentedModel{ID: 1, Shadow: "shadow-1", Email: "sealed@example.com"}).Error; err != nil {
		t.Fatalf("an insert of the model was refused: %v", err)
	}
	if err := h.Model(&accentedModel{ID: 1}).Updates(map[string]any{"Shadow": "shadow-2"}).Error; err != nil {
		t.Fatalf("an update naming the unsealed field by key failed: %v", err)
	}
	var stored struct{ Shadow, Email string }
	if err := h.Raw("SELECT `ÉMAIL` AS shadow, `émail` AS email FROM accented_models WHERE id = 1").
		Scan(&stored).Error; err != nil {
		t.Fatalf("reading the two columns with native SQL: %v", err)
	}
	if stored.Shadow != "shadow-2" || strings.Contains(stored.Email, "@example.com") {
		t.Errorf("the columns hold %+v, want ÉMAIL in clear as shadow-2 and émail sealed", stored)
	}
	var read accentedModel
	if err := h.First(&read, 1).Error; err != nil || read.Shadow != "shadow-2" || read.Email != "sealed@example.com" {
		t.Errorf("the model reads back %+v with %v, want shadow-2 and sealed@example.com", read, err)
	}
	var shadows []map[string]any
	if err := h.Model(&accentedModel{}).Select("Shadow").Find(&shadows).Error; err != nil {
		t.Errorf("reading the unsealed column into a map was refused: %v", err)
	}
}

type titleView struct {
	ID    uint
	Title string
}

type lowerView struct {
	ID    uint
	Lower string
}

type lengthView struct {
	ID          uint
	EmailLength int
	Missing     bool
}

// TestAReadUnderAnotherNameIsRefused covers the reads that rename the sealed column, or compute
// from it, into a text field without the serializer — the model's own unmarked field among them.
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

// TestAReadUnderAnotherNameThatCannotCarryTheTextGoesThrough keeps that refusal to the fields it is
// about: a number or a truth value computed from the sealed column is not its text.
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
