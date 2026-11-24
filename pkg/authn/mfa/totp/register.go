package totp

import (
	"context"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn/mfa/internal/totpseed"
)

const seedTable = totpseed.Table

type seed = totpseed.Seed

type register struct {
	handle *gorm.DB
}

func (g *register) store() *totpseed.Store { return totpseed.New(g.handle) }

func (g *register) insert(ctx context.Context, s seed) error {
	return g.store().Insert(ctx, s)
}

func (g *register) load(ctx context.Context, factorID string) (seed, bool, error) {
	return g.store().Load(ctx, factorID)
}

func (g *register) advance(ctx context.Context, factorID string, counter int64) (bool, error) {
	return g.store().Advance(ctx, factorID, counter)
}

func (g *register) remove(ctx context.Context, factorID string) error {
	return g.store().Remove(ctx, factorID)
}

func (g *register) probe(ctx context.Context) error {
	return g.store().Probe(ctx)
}

func (g *register) roundTrip(ctx context.Context) error {
	return g.store().RoundTrip(ctx, algorithmSHA1, digitsOfSix, periodSeconds)
}
