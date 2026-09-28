package db

import (
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// These cases hold every write path GORM offers for a field to the promise the
// design makes for it: a field marked serializer:encrypted is sealed before it
// reaches the database, whatever shape the write took, or the write is refused.
// A path that stored the plaintext would leave the column in clear and the row
// unreadable through the model, and neither would be reported where it
// happened.

// writtenRow creates one row through the model, which is the path that has
// always sealed, and returns it for the case to rewrite by another path.
func writtenRow(t *testing.T, product *database) encryptedModel {
	t.Helper()
	nickname := "first-nickname"
	row := encryptedModel{Email: "first@example.com", Nickname: &nickname}
	if err := product.DB().Create(&row).Error; err != nil {
		t.Fatalf("writing the row the case starts from: %v", err)
	}
	return row
}

// assertSealed holds one column of one row to the two things a sealed write
// leaves: the column does not carry the plaintext, and the model reads the
// plaintext back.
func assertSealed(t *testing.T, product *database, id uint, want string) {
	t.Helper()
	stored := storedColumn(t, product, id, "email")
	if !stored.Valid {
		t.Fatalf("the email column is NULL after writing %q into it", want)
	}
	if strings.Contains(stored.String, want) {
		t.Errorf("the email column holds %q, which carries the plaintext %q: the write stored it in clear",
			stored.String, want)
	}
	var read encryptedModel
	if err := product.DB().First(&read, id).Error; err != nil {
		t.Fatalf("reading the row back through the model: %v", err)
	}
	if read.Email != want {
		t.Errorf("the model reads the email back as %q, want %q", read.Email, want)
	}
}

// TestEveryMapShapedWriteSealsTheField covers the writes whose values arrive as
// a map rather than as the model: the shape a PATCH handler naturally builds.
// GORM puts a map value into the statement as it is, without the field's
// serializer, so each of these would store the plaintext unless this module
// seals it.
func TestEveryMapShapedWriteSealsTheField(t *testing.T) {
	cases := []struct {
		name  string
		write func(db *gorm.DB, row *encryptedModel, email string) error
	}{
		{"Update with a column name", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(row).Update("email", email).Error
		}},
		{"Update with a field name", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(row).Update("Email", email).Error
		}},
		{"Updates with a map", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(row).Updates(map[string]any{"email": email}).Error
		}},
		{"Updates with a pointer to a map", func(db *gorm.DB, row *encryptedModel, email string) error {
			values := map[string]any{"email": email}
			return db.Model(row).Updates(&values).Error
		}},
		{"UpdateColumn", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(&encryptedModel{}).Where("id = ?", row.ID).UpdateColumn("email", email).Error
		}},
		{"UpdateColumns with a map", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(&encryptedModel{}).Where("id = ?", row.ID).
				UpdateColumns(map[string]any{"email": email}).Error
		}},
		{"Updates with a byte slice", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(row).Updates(map[string]any{"email": []byte(email)}).Error
		}},
		{"Select narrowing a map", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(row).Select("email").
				Updates(map[string]any{"email": email, "email_index": "ignored"}).Error
		}},
		{"Omit leaving the field in", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(row).Omit("email_index").
				Updates(map[string]any{"email": email, "email_index": "ignored"}).Error
		}},
		{"Save of a struct", func(db *gorm.DB, row *encryptedModel, email string) error {
			row.Email = email
			return db.Save(row).Error
		}},
		{"Updates with a struct", func(db *gorm.DB, row *encryptedModel, email string) error {
			return db.Model(row).Updates(encryptedModel{Email: email}).Error
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
			createEncryptedTable(t, product)
			row := writtenRow(t, product)

			const email = "patched@example.com"
			if err := c.write(product.DB(), &row, email); err != nil {
				t.Fatalf("the write failed: %v", err)
			}
			assertSealed(t, product, row.ID, email)
		})
	}
}

// TestAMapWriteLeavesTheCallersValuesInPlainText pins what the sealing must not
// do to the values the caller handed in. The map is the caller's, and so is the
// model a Model(&row) update writes its assignments back into: a seal that
// stayed in either would give the caller ciphertext where it put plaintext.
func TestAMapWriteLeavesTheCallersValuesInPlainText(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	row := writtenRow(t, product)

	values := map[string]any{"email": "patched@example.com"}
	if err := product.DB().Model(&row).Updates(values).Error; err != nil {
		t.Fatalf("updating through a map: %v", err)
	}
	if got, ok := values["email"].(string); !ok || got != "patched@example.com" {
		t.Errorf("the caller's map holds %#v after the update, want the plaintext it put there", values["email"])
	}
	if row.Email != "patched@example.com" {
		t.Errorf("the model the update was written through holds %q, want the plaintext", row.Email)
	}

	created := map[string]any{"email": "created@example.com"}
	if err := product.DB().Model(&encryptedModel{}).Create(created).Error; err != nil {
		t.Fatalf("creating through a map: %v", err)
	}
	if got, ok := created["email"].(string); !ok || got != "created@example.com" {
		t.Errorf("the caller's map holds %#v after the insert, want the plaintext it put there", created["email"])
	}
	if _, ok := created["id"]; !ok {
		t.Errorf("the caller's map carries no id after the insert, and GORM writes the key it generated "+
			"back into the map it inserted from: %#v", created)
	}
}

// TestANullableFieldWrittenThroughAMap covers the pointer field: a value is
// sealed, and nil stays the NULL it means rather than a seal over nothing.
func TestANullableFieldWrittenThroughAMap(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	row := writtenRow(t, product)

	if err := product.DB().Model(&row).Updates(map[string]any{"nickname": "second-nickname"}).Error; err != nil {
		t.Fatalf("setting the nullable field through a map: %v", err)
	}
	stored := storedColumn(t, product, row.ID, "nickname")
	if !stored.Valid || strings.Contains(stored.String, "second-nickname") {
		t.Errorf("the nickname column holds %v after a map write, want a sealed value", stored)
	}
	var read encryptedModel
	if err := product.DB().First(&read, row.ID).Error; err != nil {
		t.Fatalf("reading the row back: %v", err)
	}
	if read.Nickname == nil || *read.Nickname != "second-nickname" {
		t.Errorf("the nickname reads back as %v, want the plaintext written", read.Nickname)
	}

	if err := product.DB().Model(&row).Updates(map[string]any{"nickname": nil}).Error; err != nil {
		t.Fatalf("clearing the nullable field through a map: %v", err)
	}
	if stored := storedColumn(t, product, row.ID, "nickname"); stored.Valid {
		t.Errorf("the nickname column holds %q after being set to nil, want NULL", stored.String)
	}
}

// TestAnInsertFromAMapSealsTheField covers Create over a map and over a slice of
// maps, which GORM binds as they are, like the updates above.
func TestAnInsertFromAMapSealsTheField(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)

	one := map[string]any{"email": "one@example.com"}
	if err := product.DB().Model(&encryptedModel{}).Create(one).Error; err != nil {
		t.Fatalf("inserting from a map: %v", err)
	}
	batch := []map[string]any{{"email": "two@example.com"}, {"email": "three@example.com"}}
	if err := product.DB().Model(&encryptedModel{}).Create(&batch).Error; err != nil {
		t.Fatalf("inserting from a slice of maps: %v", err)
	}

	var rows []encryptedModel
	if err := product.DB().Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("reading the inserted rows back: %v", err)
	}
	want := []string{"one@example.com", "two@example.com", "three@example.com"}
	if len(rows) != len(want) {
		t.Fatalf("%d rows came back, want %d", len(rows), len(want))
	}
	for i, row := range rows {
		assertSealed(t, product, row.ID, want[i])
	}
}

// TestAnUpsertAssignmentSealsTheField covers ON CONFLICT DO UPDATE with the
// assignments given as values, and keeps the form that copies the inserted
// value working beside it.
func TestAnUpsertAssignmentSealsTheField(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	row := writtenRow(t, product)

	byValue := encryptedModel{ID: row.ID, Email: "inserted@example.com"}
	if err := product.DB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]any{"email": "upserted@example.com"}),
	}).Create(&byValue).Error; err != nil {
		t.Fatalf("upserting with an assignment by value: %v", err)
	}
	assertSealed(t, product, row.ID, "upserted@example.com")

	byColumn := encryptedModel{ID: row.ID, Email: "excluded@example.com"}
	if err := product.DB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"email"}),
	}).Create(&byColumn).Error; err != nil {
		t.Fatalf("upserting with the inserted value: %v", err)
	}
	assertSealed(t, product, row.ID, "excluded@example.com")

	updateAll := encryptedModel{ID: row.ID, Email: "all@example.com"}
	if err := product.DB().Clauses(clause.OnConflict{UpdateAll: true}).Create(&updateAll).Error; err != nil {
		t.Fatalf("upserting every column: %v", err)
	}
	assertSealed(t, product, row.ID, "all@example.com")
}

// TestAWriteThisModuleCannotSealIsRefused covers the values that cannot be
// sealed because they are not values: an SQL expression or a subquery is
// evaluated by the database, after anything here could have encrypted it. The
// write is refused, naming the field, and the column keeps what it held.
func TestAWriteThisModuleCannotSealIsRefused(t *testing.T) {
	cases := []struct {
		name  string
		write func(db *gorm.DB, row *encryptedModel) error
	}{
		{"Update with an expression", func(db *gorm.DB, row *encryptedModel) error {
			return db.Model(row).Update("email", gorm.Expr("'clear@example.com'")).Error
		}},
		{"Updates with a subquery", func(db *gorm.DB, row *encryptedModel) error {
			return db.Model(row).Updates(map[string]any{
				"email": db.Model(&encryptedModel{}).Select("email_index").Where("id = ?", row.ID),
			}).Error
		}},
		{"Update copying a column that is not sealed", func(db *gorm.DB, row *encryptedModel) error {
			return db.Model(row).Update("email", clause.Column{Name: "email_index"}).Error
		}},
		{"an upsert assigning an expression", func(db *gorm.DB, row *encryptedModel) error {
			return db.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "id"}},
				DoUpdates: clause.Assignments(map[string]any{"email": gorm.Expr("'clear@example.com'")}),
			}).Create(&encryptedModel{ID: row.ID, Email: "inserted@example.com"}).Error
		}},
		{"an insert from a map carrying an expression", func(db *gorm.DB, _ *encryptedModel) error {
			return db.Model(&encryptedModel{}).
				Create(map[string]any{"email": gorm.Expr("'clear@example.com'")}).Error
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
			createEncryptedTable(t, product)
			row := writtenRow(t, product)
			before := storedColumn(t, product, row.ID, "email")

			err := c.write(product.DB(), &row)
			if err == nil {
				t.Fatal("a write this module cannot seal went through")
			}
			if !strings.Contains(err.Error(), "Email") {
				t.Errorf("the refusal does not name the field: %v", err)
			}
			if after := storedColumn(t, product, row.ID, "email"); after != before {
				t.Errorf("the column changed from %v to %v under a refused write", before, after)
			}
			var count int64
			if err := product.DB().Model(&encryptedModel{}).Count(&count).Error; err != nil {
				t.Fatalf("counting the rows: %v", err)
			}
			if count != 1 {
				t.Errorf("%d rows after a refused write, want the one the case wrote", count)
			}
		})
	}
}

// emailPatch is a struct an update can be written from that is not the model.
// GORM reads its fields through its own schema, where the email carries no
// serializer.
type emailPatch struct {
	Email string
}

// sealedEmailPatch is the same struct with the field marked the way the model
// marks it.
type sealedEmailPatch struct {
	Email string `gorm:"serializer:encrypted"`
}

// indexPatch is a struct an update can be written from that reaches none of the
// model's sealed columns.
type indexPatch struct {
	EmailIndex string
}

// TestAnUpdateFromAnotherStructMustCarryTheSerializer covers Updates with a
// struct that is not the model. The values come through that struct's own
// fields, so a field without the tag would put the plaintext in the model's
// sealed column; that is refused, and the tagged form is sealed.
func TestAnUpdateFromAnotherStructMustCarryTheSerializer(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	row := writtenRow(t, product)
	before := storedColumn(t, product, row.ID, "email")

	err := product.DB().Model(&encryptedModel{}).Where("id = ?", row.ID).
		Updates(emailPatch{Email: "clear@example.com"}).Error
	if err == nil {
		t.Fatal("an update from a struct whose field carries no serializer went through")
	}
	if !strings.Contains(err.Error(), "Email") {
		t.Errorf("the refusal does not name the field: %v", err)
	}
	if after := storedColumn(t, product, row.ID, "email"); after != before {
		t.Errorf("the column changed from %v to %v under a refused update", before, after)
	}

	if err := product.DB().Model(&encryptedModel{}).Where("id = ?", row.ID).
		Updates(sealedEmailPatch{Email: "sealed@example.com"}).Error; err != nil {
		t.Fatalf("an update from a struct whose field carries the serializer: %v", err)
	}
	assertSealed(t, product, row.ID, "sealed@example.com")

	// A struct that leaves the field at its zero value does not write it, so
	// there is nothing to refuse.
	if err := product.DB().Model(&encryptedModel{}).Where("id = ?", row.ID).
		Updates(indexPatch{EmailIndex: "digest"}).Error; err != nil {
		t.Errorf("an update from a struct that does not reach the sealed column was refused: %v", err)
	}
}
