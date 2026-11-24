package db

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/core/coretest"
)

type emailView struct {
	ID    uint
	Email string
}

type sealedEmailView struct {
	ID    uint
	Email string `gorm:"serializer:encrypted"`
}

// TestAReadThatWouldHandBackTheCiphertextIsRefused covers the finishers that run a query whose
// destination is known before it runs: each one would fill the destination with the stored text.
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

// TestAReadThatDoesNotReachTheSealedColumnGoesThrough keeps the refusal to the reads it is about.
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

// TestARowReadOverTheModelThatReachesTheSealedColumnIsRefused covers the finishers that hand the
// result set to a destination chosen after the last callback: Scan, Rows and Row.
func TestARowReadOverTheModelThatReachesTheSealedColumnIsRefused(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	writtenRow(t, product)

	cases := []struct {
		name string
		read func() (any, error)
	}{
		{"Scan into strings", func() (any, error) {
			var emails []string
			err := product.DB().Model(&encryptedModel{}).Select("email").Scan(&emails).Error
			return emails, err
		}},
		{"Scan into a string", func() (any, error) {
			var email string
			err := product.DB().Model(&encryptedModel{}).Select("email").Scan(&email).Error
			return email, err
		}},
		{"Scan into maps", func() (any, error) {
			var rows []map[string]any
			err := product.DB().Model(&encryptedModel{}).Scan(&rows).Error
			return rows, err
		}},
		{"Scan into a struct without the serializer", func() (any, error) {
			var rows []emailView
			err := product.DB().Model(&encryptedModel{}).Scan(&rows).Error
			return rows, err
		}},
		{"Scan into the model", func() (any, error) {
			var rows []encryptedModel
			err := product.DB().Model(&encryptedModel{}).Scan(&rows).Error
			return rows, err
		}},
		{"Rows over every column", func() (any, error) {
			rows, err := product.DB().Model(&encryptedModel{}).Rows()
			return closedRows(t, rows), err
		}},
		{"Rows selecting the column", func() (any, error) {
			rows, err := product.DB().Model(&encryptedModel{}).Select("id, email").Rows()
			return closedRows(t, rows), err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.read()
			if err == nil {
				t.Fatalf("the read went through and handed %v to a destination this module never "+
					"sees, which gets the stored text of a sealed column rather than its value", got)
			}
			if !strings.Contains(err.Error(), "Email") {
				t.Errorf("the refusal does not name the field: %v", err)
			}
		})
	}
}

// TestARowOverTheModelThatReachesTheSealedColumnReportsTheRefusal covers Row, which has no error of
// its own to return: what it hands back is the row.
func TestARowOverTheModelThatReachesTheSealedColumnReportsTheRefusal(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	writtenRow(t, product)

	for _, c := range []struct {
		name string
		row  func() *sql.Row
	}{
		{"on the handle", func() *sql.Row {
			return product.DB().Model(&encryptedModel{}).Select("email").Row()
		}},
		{"over every column", func() *sql.Row {
			return product.DB().Model(&encryptedModel{}).Where("id = ?", 1).Row()
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			row := func() (row *sql.Row) {
				defer func() {
					if recovered := recover(); recovered != nil {
						t.Fatalf("Row panicked with %v, want the refusal reported through the row", recovered)
					}
				}()
				return c.row()
			}()
			if row == nil {
				t.Fatal("Row handed back no row, and scanning it would panic")
			}
			email := "untouched"
			err := row.Scan(&email)
			if err == nil || !strings.Contains(err.Error(), "Email") {
				t.Fatalf("Scan on the refused row gave %v, want the refusal naming the field", err)
			}
			if email != "untouched" {
				t.Errorf("the refused row scanned %q into the destination", email)
			}
			if rowErr := row.Err(); rowErr == nil || rowErr.Error() != err.Error() {
				t.Errorf("Err on the refused row gave %v, want the refusal Scan gave", rowErr)
			}
		})
	}
}

// TestARefusedRowTakesNoConnection holds the refused row to reaching no database: it is answered
// before any connection is taken.
func TestARefusedRowTakesNoConnection(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem,
		`"max-open-conns":1`, `"max-idle-conns":1`)
	createEncryptedTable(t, product)
	writtenRow(t, product)

	err := product.DB().Transaction(func(*gorm.DB) error {
		// The deadline only bounds the wait for a wrong implementation: one
		// that takes a connection before refusing waits for the one this
		// transaction holds and answers the pool's deadline error instead of
		// the refusal naming the field.
		waiting, cancel := context.WithTimeout(t.Context(), coretest.Scale(10*time.Second))
		defer cancel()
		var email string
		err := product.DB().WithContext(waiting).Model(&encryptedModel{}).Select("email").Row().Scan(&email)
		if err == nil || !strings.Contains(err.Error(), "Email") {
			t.Errorf("the refused Row gave %v, want the refusal naming the field", err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the transaction the Row was issued beside failed: %v", err)
	}
}

// TestARowReadThatDoesNotReachTheSealedColumnGoesThrough keeps that refusal to the statements it is
// about: one over the model that selects or omits around the sealed columns.
func TestARowReadThatDoesNotReachTheSealedColumnGoesThrough(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	row := writtenRow(t, product)
	if err := product.DB().AutoMigrate(&encryptedModel{}); err != nil {
		t.Errorf("bringing the existing table of the model up again failed: %v", err)
	}

	var ids []uint
	if err := product.DB().Model(&encryptedModel{}).Select("id").Scan(&ids).Error; err != nil {
		t.Errorf("scanning a column that is not sealed was refused: %v", err)
	}
	var count int64
	if err := product.DB().Model(&encryptedModel{}).Select("count(*)").Row().Scan(&count); err != nil {
		t.Errorf("a Row counting the rows failed: %v", err)
	}
	omitted, err := product.DB().Model(&encryptedModel{}).Omit("email", "nickname").Rows()
	if err != nil {
		t.Errorf("Rows omitting the sealed columns was refused: %v", err)
	}
	if columns := closedRows(t, omitted); slices.Contains(columns, "email") {
		t.Errorf("Rows omitting the sealed columns read %v", columns)
	}

	var stored []string
	if err = product.DB().Raw("SELECT email FROM encrypted_models").Scan(&stored).Error; err != nil {
		t.Errorf("native SQL over the sealed column was refused: %v", err)
	}
	if len(stored) != 1 || stored[0] == row.Email {
		t.Errorf("native SQL read %v, want the one stored seal", stored)
	}
	tabled, err := product.DB().Table("encrypted_models").Select("email").Rows()
	if err != nil {
		t.Errorf("Rows over a table name alone was refused: %v", err)
	}
	closedRows(t, tabled)
}

func closedRows(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	if rows == nil {
		return nil
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("closing the result set: %v", err)
		}
	}()
	columns, err := rows.Columns()
	if err != nil {
		t.Errorf("reading the result set's columns: %v", err)
	}
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		t.Errorf("iterating the result set: %v", err)
	}
	return columns
}

// TestScanRowsOnTheDeliveredHandleOpensTheField covers streaming a result set the way GORM
// documents it: Rows on one statement, ScanRows on the handle.
func TestScanRowsOnTheDeliveredHandleOpensTheField(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	row := writtenRow(t, product)

	sources := []struct {
		name string
		open func() (*sql.Rows, error)
	}{
		{"native SQL", func() (*sql.Rows, error) {
			return product.DB().Raw("SELECT * FROM encrypted_models").Rows()
		}},
		{"a table name", func() (*sql.Rows, error) {
			return product.DB().Table("encrypted_models").Rows()
		}},
	}
	for _, source := range sources {
		t.Run(source.name, func(t *testing.T) {
			rows, err := source.open()
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
		})
	}
}

// TestScanRowsOnAReplacedContextSaysWhatToDo covers the one shape of ScanRows the key cannot reach:
// a handle whose context the caller replaced, on a call that runs no callback.
func TestScanRowsOnAReplacedContextSaysWhatToDo(t *testing.T) {
	product := assembleEncrypted(t, encryptedDSN(t), encryptedRootKeyItem)
	createEncryptedTable(t, product)
	writtenRow(t, product)

	rows, err := product.DB().Raw("SELECT * FROM encrypted_models").Rows()
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
