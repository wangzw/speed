package db

import (
	"database/sql"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// These cases cover a model on which two fields carry one column name exactly,
// the shape an embedded struct gives when it declares a field the model also
// declares. GORM binds such a column to one of the two — the field on the
// shorter path, or the first declared at equal length — and reads and writes
// the column through that field alone. What is judged is what GORM writes: the
// other field is not written, so it is not a second value for the column, and
// when the field GORM binds lacks the serializer the column is written and read
// in clear whatever the other field declares.

// contactDetails is a struct a model embeds, with an unsealed field of its own
// on the email column.
type contactDetails struct {
	Email string
}

// embeddingModel declares its sealed email beside an embedded struct that
// carries an unsealed field on the same column. The model's own Email is on the
// shorter path, so GORM binds the column to it and never writes Contact.Email.
type embeddingModel struct {
	ID      uint
	Email   string         `gorm:"serializer:encrypted"`
	Contact contactDetails `gorm:"embedded"`
}

// TableName is the one table the embedding models write into.
func (embeddingModel) TableName() string { return "embedding_models" }

// defaultedDetails is contactDetails with the column's default left to the
// database. GORM writes such a field whenever a row gives it a value, whichever
// field the column is bound to.
type defaultedDetails struct {
	Email string `gorm:"default:null"`
}

// defaultedEmbeddingModel embeds defaultedDetails beside its sealed email.
type defaultedEmbeddingModel struct {
	ID      uint
	Email   string           `gorm:"serializer:encrypted"`
	Contact defaultedDetails `gorm:"embedded"`
}

// TableName is the one table the embedding models write into.
func (defaultedEmbeddingModel) TableName() string { return "embedding_models" }

// createEmbeddingTable brings up the table the embedding models write into:
// one email column, which is all their two fields name.
func createEmbeddingTable(t *testing.T, product *database) {
	t.Helper()
	if err := product.DB().Exec("CREATE TABLE embedding_models (id INTEGER PRIMARY KEY, email TEXT, note TEXT, secret TEXT)").Error; err != nil {
		t.Fatalf("creating the table the embedding models write into: %v", err)
	}
}

// embeddedEmail is what the email column of one embedding row holds, read
// around the model: "absent" when there is no such row.
func embeddedEmail(t *testing.T, product *database, id uint) string {
	t.Helper()
	var stored []sql.NullString
	if err := product.DB().Raw("SELECT email FROM embedding_models WHERE id = ?", id).Scan(&stored).Error; err != nil {
		t.Fatalf("reading the email column with native SQL: %v", err)
	}
	switch {
	case len(stored) == 0:
		return "absent"
	case !stored[0].Valid:
		return "NULL"
	default:
		return stored[0].String
	}
}

// TestAWriteOfTheModelIsNotJudgedByAFieldGORMDoesNotWrite covers the writes
// through the model's own struct when an embedded field duplicates the sealed
// column. GORM writes the column once, through the sealed field, so the write
// goes through and the column holds the seal of the model's own Email, not the
// embedded field's value.
func TestAWriteOfTheModelIsNotJudgedByAFieldGORMDoesNotWrite(t *testing.T) {
	const embedded = "embedded@example.com"
	cases := []struct {
		name string
		// existing is whether the row the write reaches is there before it,
		// put there with native SQL so that no write of the model is needed.
		existing bool
		write    func(h *gorm.DB) error
	}{
		{"an insert of the model", false, func(h *gorm.DB) error {
			return h.Create(&embeddingModel{
				ID:      2,
				Email:   "written@example.com",
				Contact: contactDetails{Email: embedded},
			}).Error
		}},
		{"an insert of several rows", false, func(h *gorm.DB) error {
			return h.Create(&[]embeddingModel{
				{ID: 2, Email: "written@example.com", Contact: contactDetails{Email: embedded}},
				{ID: 3, Email: "third@example.com", Contact: contactDetails{Email: embedded}},
			}).Error
		}},
		{"an upsert of the model", true, func(h *gorm.DB) error {
			return h.Clauses(clause.OnConflict{UpdateAll: true}).Create(&embeddingModel{
				ID:      2,
				Email:   "written@example.com",
				Contact: contactDetails{Email: embedded},
			}).Error
		}},
		{"a save of the model", true, func(h *gorm.DB) error {
			return h.Save(&embeddingModel{
				ID:      2,
				Email:   "written@example.com",
				Contact: contactDetails{Email: embedded},
			}).Error
		}},
		{"an update from the model's struct", true, func(h *gorm.DB) error {
			return h.Model(&embeddingModel{ID: 2}).Updates(embeddingModel{
				Email:   "written@example.com",
				Contact: contactDetails{Email: embedded},
			}).Error
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
			createEmbeddingTable(t, product)
			if c.existing {
				if err := product.DB().Exec("INSERT INTO embedding_models (id) VALUES (2)").Error; err != nil {
					t.Fatalf("putting in the row the write reaches: %v", err)
				}
			}

			if err := c.write(product.DB()); err != nil {
				t.Fatalf("the write was refused: %v", err)
			}
			if stored := embeddedEmail(t, product, 2); strings.Contains(stored, "@example.com") {
				t.Errorf("the email column holds %q, which is in clear", stored)
			}
			var read embeddingModel
			if err := product.DB().First(&read, 2).Error; err != nil {
				t.Fatalf("reading the row back through the model: %v", err)
			}
			if read.Email != "written@example.com" {
				t.Errorf("the model reads the email back as %q, want written@example.com", read.Email)
			}
		})
	}
}

// TestAnEmbeddedFieldGORMWritesAsWellIsJudged keeps the refusal to the field
// GORM writes: an embedded field whose default is the database's is written as
// a second value for the column whenever the row gives it one, and that insert
// is refused, naming both fields by their place in the model; the same insert
// with the field left empty writes the column once and goes through.
func TestAnEmbeddedFieldGORMWritesAsWellIsJudged(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEmbeddingTable(t, product)
	h := product.DB()

	err := h.Create(&defaultedEmbeddingModel{
		ID:      1,
		Email:   "sealed@example.com",
		Contact: defaultedDetails{Email: "clear@example.com"},
	}).Error
	if err == nil {
		t.Fatalf("an insert writing the email column twice went through; it holds %q", embeddedEmail(t, product, 1))
	}
	for _, name := range []string{"defaultedEmbeddingModel.Email", "defaultedEmbeddingModel.Contact.Email"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal does not name %s: %v", name, err)
		}
	}
	if stored := embeddedEmail(t, product, 1); stored != "absent" {
		t.Errorf("a refused insert left a row holding %q", stored)
	}

	if err := h.Create(&defaultedEmbeddingModel{ID: 1, Email: "sealed@example.com"}).Error; err != nil {
		t.Fatalf("an insert leaving the embedded field empty was refused: %v", err)
	}
	var read defaultedEmbeddingModel
	if err := h.First(&read, 1).Error; err != nil || read.Email != "sealed@example.com" {
		t.Errorf("the model reads back %+v with %v, want the sealed email sealed@example.com", read, err)
	}
}

// TestAnEmbeddedFieldTheSelectionLeavesOutIsNotJudged keeps that refusal to
// what Select and Omit leave GORM writing: the embedded field whose default is
// the database's is written when Select names its column or nothing leaves it
// out, and not otherwise, whatever value the row gives it. An insert that
// leaves the column out goes through and writes nothing into it.
func TestAnEmbeddedFieldTheSelectionLeavesOutIsNotJudged(t *testing.T) {
	row := func() *defaultedEmbeddingModel {
		return &defaultedEmbeddingModel{
			ID:      1,
			Email:   "sealed@example.com",
			Contact: defaultedDetails{Email: "clear@example.com"},
		}
	}
	for _, c := range []struct {
		name    string
		insert  func(h *gorm.DB) error
		refused bool
	}{
		{"Omit leaving its column out", func(h *gorm.DB) error { return h.Omit("email").Create(row()).Error }, false},
		{"Select choosing only another column", func(h *gorm.DB) error {
			return h.Select("id").Create(row()).Error
		}, false},
		{"Select naming its column", func(h *gorm.DB) error {
			return h.Select("id", "email").Create(row()).Error
		}, true},
		{"Omit leaving another column out", func(h *gorm.DB) error { return h.Omit("note").Create(row()).Error }, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
			createEmbeddingTable(t, product)
			err := c.insert(product.DB())
			switch {
			case c.refused && err == nil:
				t.Fatalf("an insert writing the email column twice went through; it holds %q",
					embeddedEmail(t, product, 1))
			case c.refused:
				if !strings.Contains(err.Error(), "defaultedEmbeddingModel.Contact.Email") {
					t.Errorf("the refusal does not name the embedded field: %v", err)
				}
			case err != nil:
				t.Fatalf("an insert that leaves the email column out was refused: %v", err)
			default:
				if stored := embeddedEmail(t, product, 1); stored != "NULL" {
					t.Errorf("the email column of an insert that leaves it out holds %q, want NULL", stored)
				}
			}
		})
	}
}

// sealedDetails is a struct a model embeds whose field seals the email column.
type sealedDetails struct {
	Email string `gorm:"serializer:encrypted"`
}

// shadowingModel embeds sealedDetails and declares an unsealed Email of its
// own, on the shorter path. GORM binds the email column to that field, so the
// embedded field's serializer is never run: the model declares the column
// sealed, and GORM writes and reads it in clear. Secret is a sealed column
// GORM binds to its sealed field.
type shadowingModel struct {
	ID      uint
	Email   string
	Note    string
	Secret  string        `gorm:"serializer:encrypted"`
	Contact sealedDetails `gorm:"embedded"`
}

// TableName is the one table the embedding models write into.
func (shadowingModel) TableName() string { return "embedding_models" }

// TestAStatementOverASealedColumnGORMBindsToAnUnsealedFieldIsRefused covers
// that model. Every write that reaches the email column would store its value
// in clear, or, sealed here, leave a seal the model then reads back as the
// value through its unsealed field; every read of the column would hand back
// whatever the column holds, a seal included. Each is refused, naming both
// fields, and the column keeps what it held. A statement that stays off the
// column goes through.
func TestAStatementOverASealedColumnGORMBindsToAnUnsealedFieldIsRefused(t *testing.T) {
	const clear = "clear@example.com"
	var rows []shadowingModel
	cases := []struct {
		name      string
		statement func(h *gorm.DB) error
	}{
		{"an insert of the model", func(h *gorm.DB) error {
			return h.Create(&shadowingModel{ID: 2, Email: clear}).Error
		}},
		{"an insert from a map", func(h *gorm.DB) error {
			return h.Model(&shadowingModel{}).Create(map[string]any{"id": 2, "email": clear}).Error
		}},
		{"a save of the model", func(h *gorm.DB) error {
			return h.Save(&shadowingModel{ID: 1, Email: clear}).Error
		}},
		{"an update from the model's struct", func(h *gorm.DB) error {
			return h.Model(&shadowingModel{ID: 1}).Updates(shadowingModel{Email: clear}).Error
		}},
		{"an update from another struct that seals the column", func(h *gorm.DB) error {
			return h.Model(&shadowingModel{ID: 1}).Updates(sealedEmailPatch{Email: clear}).Error
		}},
		{"an update from a map", func(h *gorm.DB) error {
			return h.Model(&shadowingModel{ID: 1}).Updates(map[string]any{"email": clear}).Error
		}},
		{"an update of one column", func(h *gorm.DB) error {
			return h.Model(&shadowingModel{ID: 1}).Update("Email", clear).Error
		}},
		{"an update from a map in another letter case", func(h *gorm.DB) error {
			return h.Model(&shadowingModel{ID: 1}).Updates(map[string]any{"EMAIL": clear}).Error
		}},
		{"a SET clause the caller wrote", func(h *gorm.DB) error {
			return h.Model(&shadowingModel{}).Where("id = ?", 1).
				Clauses(clause.Set{{Column: clause.Column{Name: "email"}, Value: clear}}).
				Updates(map[string]any{}).Error
		}},
		{"an upsert assignment", func(h *gorm.DB) error {
			return h.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				DoUpdates: clause.Assignments(map[string]any{"email": clear}),
			}).Omit("Email").Create(&shadowingModel{ID: 1}).Error
		}},
		{"an assignment copying the column into another sealed one", func(h *gorm.DB) error {
			return h.Model(&shadowingModel{}).Where("id = ?", 1).
				Clauses(clause.Set{{Column: clause.Column{Name: "secret"}, Value: clause.Column{Name: "email"}}}).
				Updates(map[string]any{}).Error
		}},
		{"a read into the model", func(h *gorm.DB) error {
			return h.Find(&rows).Error
		}},
		{"a read of one row into the model", func(h *gorm.DB) error {
			var row shadowingModel
			return h.First(&row, 1).Error
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
			createEmbeddingTable(t, product)
			if err := product.DB().Exec("INSERT INTO embedding_models (id, email) VALUES (1, 'held')").Error; err != nil {
				t.Fatalf("putting in the row the statement reaches: %v", err)
			}

			err := c.statement(product.DB())
			if err == nil {
				t.Fatalf("the statement went through; the email column of row 1 holds %q and of row 2 %q",
					embeddedEmail(t, product, 1), embeddedEmail(t, product, 2))
			}
			for _, name := range []string{"shadowingModel.Contact.Email", "shadowingModel.Email"} {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("the refusal does not name %s: %v", name, err)
				}
			}
			if !strings.Contains(err.Error(), `binds its column "email" to field shadowingModel.Email`) {
				t.Errorf("the refusal does not say which field GORM binds the column to: %v", err)
			}
			if stored := embeddedEmail(t, product, 1); stored != "held" {
				t.Errorf("the email column of row 1 went from %q to %q under a refused statement", "held", stored)
			}
			if stored := embeddedEmail(t, product, 2); stored != "absent" {
				t.Errorf("a refused insert left a row 2 holding %q", stored)
			}
		})
	}
}

// TestAStatementOverTheShadowingModelThatStaysOffTheColumnGoesThrough keeps
// that refusal to the column it is about.
func TestAStatementOverTheShadowingModelThatStaysOffTheColumnGoesThrough(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEmbeddingTable(t, product)
	h := product.DB()

	if err := h.Omit("Email").Create(&shadowingModel{ID: 1, Note: "a note"}).Error; err != nil {
		t.Fatalf("an insert leaving the email column out was refused: %v", err)
	}
	if err := h.Model(&shadowingModel{ID: 1}).Updates(map[string]any{"note": "another note"}).Error; err != nil {
		t.Fatalf("an update of another column was refused: %v", err)
	}
	var read []shadowingModel
	if err := h.Select("id", "note").Find(&read).Error; err != nil {
		t.Fatalf("a read selecting around the email column was refused: %v", err)
	}
	if len(read) != 1 || read[0].Note != "another note" {
		t.Errorf("the read gave %+v, want the one row with its note", read)
	}
	read = nil
	if err := h.Omit("Email").Find(&read).Error; err != nil {
		t.Fatalf("a read omitting the email column was refused: %v", err)
	}
	if len(read) != 1 || read[0].Note != "another note" {
		t.Errorf("the read gave %+v, want the one row with its note", read)
	}
}
