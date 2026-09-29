package db

import (
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
// other field is not written, so it is not a second value for the column.

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
	if err := product.DB().Exec("CREATE TABLE embedding_models (id INTEGER PRIMARY KEY, email TEXT)").Error; err != nil {
		t.Fatalf("creating the table the embedding models write into: %v", err)
	}
}

// embeddedEmail is what the email column of one embedding row holds, read
// around the model: "absent" when there is no such row.
func embeddedEmail(t *testing.T, product *database, id uint) string {
	t.Helper()
	var stored []*string
	if err := product.DB().Raw("SELECT email FROM embedding_models WHERE id = ?", id).Scan(&stored).Error; err != nil {
		t.Fatalf("reading the email column with native SQL: %v", err)
	}
	switch {
	case len(stored) == 0:
		return "absent"
	case stored[0] == nil:
		return "NULL"
	default:
		return *stored[0]
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
