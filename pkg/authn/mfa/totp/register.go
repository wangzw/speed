package totp

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// seedTable is the table of the TOTP seeds, this module's own.
const seedTable = "authn_totp_secrets"

// probePlaintext is the value the startup round trip writes into the encrypted
// column and reads back. It is not a secret of any deployment: it exists only so
// that a write and a read that cannot be encrypted fail the startup instead of
// the first enrollment.
const probePlaintext = "speed/authn/totp/encrypted-column-probe"

// seedRow is a row of the table as the model path sees it. The secret carries
// the serializer, so a write seals it and a read opens it; a read that reached
// the column through anything but this model would come back as ciphertext, and
// the database module refuses that rather than hand it over.
type seedRow struct {
	FactorID    string `gorm:"column:factor_id;primaryKey"`
	Secret      string `gorm:"column:secret;serializer:encrypted"`
	Algorithm   string `gorm:"column:algorithm"`
	Digits      int    `gorm:"column:digits"`
	Period      int    `gorm:"column:period"`
	LastCounter int64  `gorm:"column:last_counter"`
}

// TableName is the table this model maps to, which is the one the migration
// creates.
func (seedRow) TableName() string { return seedTable }

// seed is the data of one TOTP factor as the factor reads and writes it. The
// secret is the plaintext; it never leaves this module.
type seed struct {
	FactorID    string
	Secret      string
	Algorithm   string
	Digits      int
	Period      int
	LastCounter int64
}

// row is the model form of the seed. The two types hold the same fields in the
// same order, so one converts to the other.
func (s seed) row() seedRow { return seedRow(s) }

func rowSeed(r seedRow) seed { return seed(r) }

// register is the statements on authn_totp_secrets and nothing else. It decides
// nothing; the factor does.
type register struct {
	handle *gorm.DB
}

func (g *register) session(ctx context.Context) *gorm.DB { return g.handle.WithContext(ctx) }

// insert writes the seed of a factor. It goes through the model so that the
// serializer seals the secret.
func (g *register) insert(ctx context.Context, s seed) error {
	row := s.row()
	if err := g.session(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("writing a TOTP seed: %w", err)
	}
	return nil
}

// load reads the seed of a factor. found is false for a factor that has no row,
// which is a factor nothing registered here.
func (g *register) load(ctx context.Context, factorID string) (seed, bool, error) {
	var row seedRow
	err := g.session(ctx).Where("factor_id = ?", factorID).Take(&row).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return seed{}, false, nil
	case err != nil:
		return seed{}, false, fmt.Errorf("reading a TOTP seed: %w", err)
	}
	return rowSeed(row), true, nil
}

// advance raises last_counter to counter, and only when it is still below it.
// The statement is what decides a replay: two attempts that present the code of
// one counter both read a lower stored value, the conditional write lets exactly
// one through, and the other is told rows were affected zero times. The result
// says whether this call was the one.
func (g *register) advance(ctx context.Context, factorID string, counter int64) (bool, error) {
	res := g.session(ctx).Exec("UPDATE "+seedTable+" SET last_counter = ? WHERE factor_id = ? AND last_counter < ?",
		counter, factorID, counter)
	if res.Error != nil {
		return false, fmt.Errorf("advancing a TOTP counter: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// remove deletes the seed of a factor. It is idempotent: a row that is not there
// is not an error.
func (g *register) remove(ctx context.Context, factorID string) error {
	if err := g.session(ctx).Exec("DELETE FROM "+seedTable+" WHERE factor_id = ?", factorID).Error; err != nil {
		return fmt.Errorf("deleting a TOTP seed: %w", err)
	}
	return nil
}

// probe reports whether the table can be read. An error that is not ErrNotMigrated
// says the table is there and the read failed.
func (g *register) probe(ctx context.Context) error {
	err := g.session(ctx).Exec("SELECT 1 FROM " + seedTable + " WHERE 1 = 0").Error
	if err == nil {
		return nil
	}
	if !g.session(ctx).Migrator().HasTable(seedTable) {
		return fmt.Errorf("%w: %s is not there: %w", authn.ErrNotMigrated, seedTable, err)
	}
	return fmt.Errorf("reading %s: %w", seedTable, err)
}

// roundTrip writes a probe row and reads it back through the model, in a
// transaction that is rolled back, so that a handle whose encrypted column
// cannot be sealed or opened is found at startup. Every failure is the same one:
// the column could not be written and read, whatever the underlying cause.
func (g *register) roundTrip(ctx context.Context) error {
	tx := g.session(ctx).Begin()
	if tx.Error != nil {
		return fmt.Errorf("opening a transaction for the encrypted-column check: %w", tx.Error)
	}
	defer func() { _ = tx.Rollback() }()

	probe := seedRow{
		FactorID:  "probe:" + event.NewID(),
		Secret:    probePlaintext,
		Algorithm: algorithmSHA1,
		Digits:    digitsOfSix,
		Period:    periodSeconds,
	}
	if err := tx.Create(&probe).Error; err != nil {
		return fmt.Errorf("writing the encrypted-column check row: %w", err)
	}
	var back seedRow
	if err := tx.Where("factor_id = ?", probe.FactorID).Take(&back).Error; err != nil {
		return fmt.Errorf("reading the encrypted-column check row back: %w", err)
	}
	if back.Secret != probePlaintext {
		return errors.New("the encrypted column did not read back what was written into it")
	}
	return nil
}
