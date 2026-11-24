package mfa

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/mfa/internal/factorstore"
)

const (
	factorTable = factorstore.Table
	sweepBatch  = factorstore.SweepBatch
)

type register struct {
	handle *gorm.DB
}

func (g *register) store() *factorstore.Store { return factorstore.New(g.handle) }

func (g *register) list(ctx context.Context, account authn.AccountID, status int) ([]authn.FactorRecord, error) {
	return g.store().List(ctx, account, status)
}

func (g *register) find(ctx context.Context, account authn.AccountID, id string) (authn.FactorRecord, bool, error) {
	return g.store().Find(ctx, account, id)
}

func (g *register) insertPending(ctx context.Context, account authn.AccountID, kind authn.FactorKind, label string, limit int, besideActive bool, at time.Time) (authn.FactorRecord, error) {
	return g.store().InsertPending(ctx, account, kind, label, limit, besideActive, at)
}

func (g *register) activate(ctx context.Context, account authn.AccountID, id string, at time.Time) (bool, error) {
	return g.store().Activate(ctx, account, id, at)
}

func (g *register) activateReplacing(ctx context.Context, account authn.AccountID, id string, kind authn.FactorKind, at time.Time) (bool, error) {
	return g.store().ActivateReplacing(ctx, account, id, kind, at)
}

func (g *register) updateLabel(ctx context.Context, account authn.AccountID, id, label string) (bool, error) {
	return g.store().UpdateLabel(ctx, account, id, label)
}

func (g *register) touch(ctx context.Context, id string, at time.Time) error {
	return g.store().Touch(ctx, id, at)
}

func (g *register) remove(ctx context.Context, account authn.AccountID, id string) (bool, error) {
	return g.store().Remove(ctx, account, id)
}

func (g *register) removePending(ctx context.Context, id string) (bool, error) {
	return g.store().RemovePending(ctx, id)
}

func (g *register) disableAll(ctx context.Context, account authn.AccountID) (int, error) {
	return g.store().DisableAll(ctx, account)
}

func (g *register) disable(ctx context.Context, account authn.AccountID, id string) error {
	return g.store().Disable(ctx, account, id)
}

func (g *register) seasoned(ctx context.Context, account authn.AccountID, cutoff time.Time) (bool, error) {
	return g.store().Seasoned(ctx, account, cutoff)
}

func (g *register) activeKinds(ctx context.Context) ([]authn.FactorKind, error) {
	return g.store().ActiveKinds(ctx)
}

func (g *register) probe(ctx context.Context) error {
	return g.store().Probe(ctx)
}

func (g *register) expiredPending(ctx context.Context, cutoff time.Time) ([]authn.FactorRecord, error) {
	return g.store().ExpiredPending(ctx, cutoff)
}
