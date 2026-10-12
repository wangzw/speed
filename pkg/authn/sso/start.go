package sso

import (
	"context"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/core"
)

// start runs in Start: the tables must be there, the root key must be usable for
// the encrypted column, and the sweep begins. New touched no table, so on a
// first start the tables are not there until the migration has run.
func (p *product) start(ctx context.Context, reg *core.Registry) error {
	if err := p.register.probe(ctx); err != nil {
		return err
	}
	if err := p.register.roundTrip(ctx); err != nil {
		return fmt.Errorf("%w: %s holds the client secret in an encrypted column, and this database "+
			"has no usable root key: %w", authn.ErrNoEncryptionKey, ModuleName, err)
	}
	if !reg.Participating(admissionModule) {
		p.logger.Warn(diagnosticPrefix + admissionModule + " is not taking part, so a tenant's requirement " +
			"of single sign-on is enforced by nobody")
	}
	p.startSweep(ctx)
	return nil
}

// stop ends the module's background work: the sweep stops within its own bound.
func (p *product) stop() error {
	return p.stopSweep()
}
