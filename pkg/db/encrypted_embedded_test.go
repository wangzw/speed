package db

import (
	"database/sql"
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type contactDetails struct {
	Email string
}

type embeddingModel struct {
	ID      uint
	Email   string         `gorm:"serializer:encrypted"`
	Contact contactDetails `gorm:"embedded"`
}

func (embeddingModel) TableName() string { return "embedding_models" }

type defaultedDetails struct {
	Email string `gorm:"default:null"`
}

type defaultedEmbeddingModel struct {
	ID      uint
	Email   string           `gorm:"serializer:encrypted"`
	Contact defaultedDetails `gorm:"embedded"`
}

func (defaultedEmbeddingModel) TableName() string { return "embedding_models" }

func createEmbeddingTable(t *testing.T, product *database) {
	t.Helper()
	if err := product.DB().Exec("CREATE TABLE embedding_models (id INTEGER PRIMARY KEY, email TEXT, note TEXT, secret TEXT)").Error; err != nil {
		t.Fatalf("creating the table the embedding models write into: %v", err)
	}
}

// embeddedEmail is what the email column of one embedding row holds, read around the model: "absent" when there is no such row.
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

// TestAWriteOfTheModelIsNotJudgedByAFieldGORMDoesNotWrite covers the writes through the model's own
// struct when an embedded field duplicates the sealed column.
func TestAWriteOfTheModelIsNotJudgedByAFieldGORMDoesNotWrite(t *testing.T) {
	const embedded = "embedded@example.com"
	cases := []struct {
		name     string
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

// TestAnEmbeddedFieldGORMWritesAsWellIsJudged keeps the refusal to the field GORM writes.
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

// TestAnEmbeddedFieldTheSelectionLeavesOutIsNotJudged keeps that refusal to what Select and Omit
// leave GORM writing.
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

type sealedDetails struct {
	Email string `gorm:"serializer:encrypted"`
}

type shadowingModel struct {
	ID      uint
	Email   string
	Note    string
	Secret  string        `gorm:"serializer:encrypted"`
	Contact sealedDetails `gorm:"embedded"`
}

func (shadowingModel) TableName() string { return "embedding_models" }

// TestAStatementOverASealedColumnGORMBindsToAnUnsealedFieldIsRefused covers that model.
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

// TestAStatementOverTheShadowingModelThatStaysOffTheColumnGoesThrough keeps that refusal to the
// column it is about.
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
