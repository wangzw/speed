package db

import (
	"context"
	"strings"
	"testing"
)

// These cases cover the reads that do not land a sealed column in a model field.
// GORM applies a field's serializer only when it scans a column into a field of
// a parsed struct, so a column read into anything else arrives as the text the
// database holds — the ciphertext — with no error. Where the statement tells
// this module what it is about to do, the read is refused instead of handing
// the ciphertext back as if it were the value.

// emailView is a struct a query can read into that is not the model, and
// whose field for the sealed column carries no serializer.
type emailView struct {
	ID    uint
	Email string
}

// sealedEmailView is the same struct with the field marked the way the model
// marks it.
type sealedEmailView struct {
	ID    uint
	Email string `gorm:"serializer:encrypted"`
}

// TestAReadThatWouldHandBackTheCiphertextIsRefused covers the finishers that
// run a query whose destination is known before it runs: each one would fill
// the destination with the stored text, and each is refused naming the field.
func TestAReadThatWouldHandBackTheCiphertextIsRefused(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	writtenRow(t, product)

	cases := []struct {
		name string
		read func() (any, error)
	}{
		{"Pluck into strings", func() (any, error) {
			var emails []string
			err := product.DB().Model(&encryptedModel{}).Pluck("email", &emails).Error
			return emails, err
		}},
		{"Pluck by field name", func() (any, error) {
			var emails []string
			err := product.DB().Model(&encryptedModel{}).Pluck("Email", &emails).Error
			return emails, err
		}},
		{"Select into strings", func() (any, error) {
			var emails []string
			err := product.DB().Model(&encryptedModel{}).Select("email").Find(&emails).Error
			return emails, err
		}},
		{"First into a string", func() (any, error) {
			var email string
			err := product.DB().Model(&encryptedModel{}).Select("email").Take(&email).Error
			return email, err
		}},
		{"Find into maps", func() (any, error) {
			var rows []map[string]any
			err := product.DB().Model(&encryptedModel{}).Find(&rows).Error
			return rows, err
		}},
		{"Take into a map", func() (any, error) {
			row := map[string]any{}
			err := product.DB().Model(&encryptedModel{}).Take(&row).Error
			return row, err
		}},
		{"Find into a struct without the serializer", func() (any, error) {
			var rows []emailView
			err := product.DB().Model(&encryptedModel{}).Find(&rows).Error
			return rows, err
		}},
		{"Find into maps selecting the column in an expression", func() (any, error) {
			var rows []map[string]any
			err := product.DB().Model(&encryptedModel{}).Select("id, email").Find(&rows).Error
			return rows, err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.read()
			if err == nil {
				t.Fatalf("the read went through and handed back %v, which is the stored text of a sealed "+
					"column rather than its value", got)
			}
			if !strings.Contains(err.Error(), "Email") {
				t.Errorf("the refusal does not name the field: %v", err)
			}
		})
	}
}

// TestAReadThatDoesNotReachTheSealedColumnGoesThrough keeps the refusal to the
// reads it is about. A read that selects around the sealed column, one into a
// struct that carries the serializer, and one whose destination cannot hold
// text all go through, and the tagged struct reads the plaintext.
func TestAReadThatDoesNotReachTheSealedColumnGoesThrough(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	row := writtenRow(t, product)

	var ids []uint
	if err := product.DB().Model(&encryptedModel{}).Pluck("id", &ids).Error; err != nil {
		t.Errorf("plucking a column that is not sealed was refused: %v", err)
	}
	var indexes []string
	if err := product.DB().Model(&encryptedModel{}).Pluck("email_index", &indexes).Error; err != nil {
		t.Errorf("plucking the blind index column beside the sealed one was refused: %v", err)
	}
	var narrow []map[string]any
	if err := product.DB().Model(&encryptedModel{}).Select("id", "email_index").Find(&narrow).Error; err != nil {
		t.Errorf("reading maps that select around the sealed column was refused: %v", err)
	}
	var omitted []map[string]any
	if err := product.DB().Model(&encryptedModel{}).Omit("email", "nickname").Find(&omitted).Error; err != nil {
		t.Errorf("reading maps that omit the sealed columns was refused: %v", err)
	}
	var count int64
	if err := product.DB().Model(&encryptedModel{}).Distinct("email").Count(&count).Error; err != nil {
		t.Errorf("counting over the sealed column was refused: %v", err)
	}

	var views []sealedEmailView
	if err := product.DB().Model(&encryptedModel{}).Find(&views).Error; err != nil {
		t.Fatalf("reading into a struct whose field carries the serializer: %v", err)
	}
	if len(views) != 1 || views[0].Email != row.Email {
		t.Errorf("the struct carrying the serializer read %v, want the plaintext %q", views, row.Email)
	}
}

// TestScanRowsOnTheDeliveredHandleOpensTheField covers streaming a result set
// the way GORM documents it: Rows on one statement, ScanRows on the handle. The
// scan runs no callback chain, so the key reaches it only if the handle itself
// carries it.
func TestScanRowsOnTheDeliveredHandleOpensTheField(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	row := writtenRow(t, product)

	rows, err := product.DB().Model(&encryptedModel{}).Rows()
	if err != nil {
		t.Fatalf("opening the result set: %v", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("closing the result set: %v", err)
		}
	}()
	var scanned []encryptedModel
	for rows.Next() {
		var one encryptedModel
		if err := product.DB().ScanRows(rows, &one); err != nil {
			t.Fatalf("scanning a row with the delivered handle: %v", err)
		}
		scanned = append(scanned, one)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating the result set: %v", err)
	}
	if len(scanned) != 1 || scanned[0].Email != row.Email {
		t.Errorf("scanning with the delivered handle read %v, want the plaintext %q", scanned, row.Email)
	}
}

// TestScanRowsOnAReplacedContextSaysWhatToDo covers the one shape of ScanRows
// the key cannot reach: a handle whose context the caller replaced, on a call
// that runs no callback. It still fails, and the failure says that, rather
// than claiming the handle was never this module's.
func TestScanRowsOnAReplacedContextSaysWhatToDo(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	writtenRow(t, product)

	rows, err := product.DB().Model(&encryptedModel{}).Rows()
	if err != nil {
		t.Fatalf("opening the result set: %v", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("closing the result set: %v", closeErr)
		}
	}()
	if !rows.Next() {
		t.Fatalf("the result set is empty: %v", rows.Err())
	}
	var one encryptedModel
	err = product.DB().WithContext(context.WithValue(t.Context(), contextMarker{}, "request")).
		ScanRows(rows, &one)
	if err == nil {
		t.Fatal("a scan whose context carries no key opened the field")
	}
	if strings.Contains(err.Error(), "not built by a handle this module opened") {
		t.Errorf("the failure claims the handle is not this module's, and it is: %v", err)
	}
	if !strings.Contains(err.Error(), "ScanRows") {
		t.Errorf("the failure does not name the call that lost the key: %v", err)
	}
}
