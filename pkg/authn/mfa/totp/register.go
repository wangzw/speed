package totp

// The seed statements live in pkg/authn/mfa/internal/totpseed, so that the
// module's test-support package mfatest can write and read the same rows through
// them: the encrypted model is the one thing a fixture must not copy, since a
// copy that dropped the serializer would store a seed in plaintext. The type
// below is this module's view of those statements.
//
// seedTable and the seed type stay here because the module's tests name both.

import (
	"context"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn/mfa/internal/totpseed"
)

// seedTable is the table of the TOTP seeds, this module's own.
const seedTable = totpseed.Table

// seed is the data of one TOTP factor as the factor reads and writes it. The
// secret is the plaintext; it never leaves this module.
type seed = totpseed.Seed

// register is the statements on authn_totp_secrets and nothing else. It decides
// nothing; the factor does.
type register struct {
	handle *gorm.DB
}

func (g *register) store() *totpseed.Store { return totpseed.New(g.handle) }

// insert writes the seed of a factor. It goes through the model so that the
// serializer seals the secret.
func (g *register) insert(ctx context.Context, s seed) error {
	return g.store().Insert(ctx, s)
}

// load reads the seed of a factor. found is false for a factor that has no row,
// which is a factor nothing registered here.
func (g *register) load(ctx context.Context, factorID string) (seed, bool, error) {
	return g.store().Load(ctx, factorID)
}

// advance raises last_counter to counter, and only when it is still below it.
// The statement is what decides a replay: two attempts that present the code of
// one counter both read a lower stored value, the conditional write lets exactly
// one through, and the other is told rows were affected zero times. The result
// says whether this call was the one.
func (g *register) advance(ctx context.Context, factorID string, counter int64) (bool, error) {
	return g.store().Advance(ctx, factorID, counter)
}

// remove deletes the seed of a factor. It is idempotent: a row that is not there
// is not an error.
func (g *register) remove(ctx context.Context, factorID string) error {
	return g.store().Remove(ctx, factorID)
}

// probe reports whether the table can be read. An error that is not ErrNotMigrated
// says the table is there and the read failed.
func (g *register) probe(ctx context.Context) error {
	return g.store().Probe(ctx)
}

// roundTrip writes a probe row and reads it back through the model, in a
// transaction that is rolled back, so that a handle whose encrypted column
// cannot be sealed or opened is found at startup. Every failure is the same one:
// the column could not be written and read, whatever the underlying cause.
func (g *register) roundTrip(ctx context.Context) error {
	return g.store().RoundTrip(ctx, algorithmSHA1, digitsOfSix, periodSeconds)
}
