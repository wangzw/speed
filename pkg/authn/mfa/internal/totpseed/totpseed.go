// Package totpseed holds the statements on authn_totp_secrets, the TOTP factor's seed table. The secret column uses the encrypted serializer, so a write seals it and a read opens it.
package totpseed

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/event"
)

// Table is the table of the TOTP seeds.
const Table = "authn_totp_secrets"

// The parameters a seed is generated with: HMAC-SHA1, six digits and a thirty-second step, the convention every authenticator app follows. They are fixed, not settings.
const (
	// PeriodSeconds is the step a generated code is valid for.
	PeriodSeconds = 30
	// DigitsOfSix is how many digits a generated code carries.
	DigitsOfSix = 6
	// AlgorithmSHA1 is the digest a generated code is computed under.
	AlgorithmSHA1 = "SHA1"
)

const probePlaintext = "speed/authn/totp/encrypted-column-probe"

type row struct {
	FactorID    string `gorm:"column:factor_id;primaryKey"`
	Secret      string `gorm:"column:secret;serializer:encrypted"`
	Algorithm   string `gorm:"column:algorithm"`
	Digits      int    `gorm:"column:digits"`
	Period      int    `gorm:"column:period"`
	LastCounter int64  `gorm:"column:last_counter"`
}

func (row) TableName() string { return Table }

// Seed is the data of one TOTP factor as the factor reads and writes it. The
// secret is the plaintext; it never leaves this module.
type Seed struct {
	// FactorID is the factor row the seed belongs to.
	FactorID string
	// Secret is the plaintext base32 seed.
	Secret string
	// Algorithm is the HMAC digest name stored with the seed.
	Algorithm string
	// Digits is the number of digits a generated code carries.
	Digits int
	// Period is the step in seconds a generated code is valid for.
	Period int
	// LastCounter is the highest counter already used, which replay is judged against.
	LastCounter int64
}

func (s Seed) row() row { return row(s) }

func seedOf(r row) Seed { return Seed(r) }

// Store is the statements on authn_totp_secrets and nothing else. It decides
// nothing; the factor does.
type Store struct {
	handle *gorm.DB
}

// New returns the seed store over one database handle.
func New(handle *gorm.DB) *Store { return &Store{handle: handle} }

func (g *Store) session(ctx context.Context) *gorm.DB { return g.handle.WithContext(ctx) }

// Insert writes the seed of a factor. It goes through the model so that the
// serializer seals the secret.
func (g *Store) Insert(ctx context.Context, s Seed) error {
	r := s.row()
	if err := g.session(ctx).Create(&r).Error; err != nil {
		return fmt.Errorf("writing a TOTP seed: %w", err)
	}
	return nil
}

// Load reads the seed of a factor. found is false for a factor that has no row,
// which is a factor nothing registered here.
func (g *Store) Load(ctx context.Context, factorID string) (Seed, bool, error) {
	var r row
	err := g.session(ctx).Where("factor_id = ?", factorID).Take(&r).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return Seed{}, false, nil
	case err != nil:
		return Seed{}, false, fmt.Errorf("reading a TOTP seed: %w", err)
	}
	return seedOf(r), true, nil
}

// Advance raises last_counter to counter only while it is still below it; the affected-row count decides which of two attempts proving the same counter wins.
func (g *Store) Advance(ctx context.Context, factorID string, counter int64) (bool, error) {
	res := g.session(ctx).Exec("UPDATE "+Table+" SET last_counter = ? WHERE factor_id = ? AND last_counter < ?",
		counter, factorID, counter)
	if res.Error != nil {
		return false, fmt.Errorf("advancing a TOTP counter: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// Remove deletes the seed of a factor. It is idempotent: a row that is not there
// is not an error.
func (g *Store) Remove(ctx context.Context, factorID string) error {
	if err := g.session(ctx).Exec("DELETE FROM "+Table+" WHERE factor_id = ?", factorID).Error; err != nil {
		return fmt.Errorf("deleting a TOTP seed: %w", err)
	}
	return nil
}

// Probe reports whether the table can be read. An error that is not
// ErrNotMigrated says the table is there and the read failed.
func (g *Store) Probe(ctx context.Context) error {
	err := g.session(ctx).Exec("SELECT 1 FROM " + Table + " WHERE 1 = 0").Error
	if err == nil {
		return nil
	}
	if !g.session(ctx).Migrator().HasTable(Table) {
		return fmt.Errorf("%w: %s is not there: %w", authn.ErrNotMigrated, Table, err)
	}
	return fmt.Errorf("reading %s: %w", Table, err)
}

// RoundTrip writes a probe row and reads it back through the model in a rolled-back transaction, so a handle whose encrypted column cannot be sealed or opened fails at startup.
func (g *Store) RoundTrip(ctx context.Context, algorithm string, digits, period int) error {
	tx := g.session(ctx).Begin()
	if tx.Error != nil {
		return fmt.Errorf("opening a transaction for the encrypted-column check: %w", tx.Error)
	}
	defer func() { _ = tx.Rollback() }()

	probe := row{
		FactorID:  "probe:" + event.NewID(),
		Secret:    probePlaintext,
		Algorithm: algorithm,
		Digits:    digits,
		Period:    period,
	}
	if err := tx.Create(&probe).Error; err != nil {
		return fmt.Errorf("writing the encrypted-column check row: %w", err)
	}
	var back row
	if err := tx.Where("factor_id = ?", probe.FactorID).Take(&back).Error; err != nil {
		return fmt.Errorf("reading the encrypted-column check row back: %w", err)
	}
	if back.Secret != probePlaintext {
		return errors.New("the encrypted column did not read back what was written into it")
	}
	return nil
}
