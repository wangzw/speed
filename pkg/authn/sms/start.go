package sms

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

// start runs in Start: the table must be there, the root key must be usable,
// and the sending pool and the sweep begin. New touched no table, so on a first
// start the table is not there until the migration has run, and the pool must
// not be started against a table that does not exist yet.
func (p *product) start(ctx context.Context) error {
	if err := p.register.probe(ctx); err != nil {
		return err
	}
	if err := p.checkEncryptionKey(); err != nil {
		return err
	}
	p.startPool(ctx)
	p.startSweep(ctx)
	return nil
}

// stop ends the module's background work: the sending pool drains and the sweep
// stops, each within its own bound.
func (p *product) stop() error {
	return errors.Join(p.stopPool(), p.stopSweep())
}

// checkEncryptionKey proves the database has a root key, without which the blind
// index panics rather than computing a digest under no key. The self-check is a
// recover around one digest: a missing key ends the startup with
// ErrNoEncryptionKey rather than falling back to an unkeyed digest, which would
// store the code and the number in a form anyone could reverse.
func (p *product) checkEncryptionKey() error {
	var missing bool
	func() {
		defer func() {
			if recover() != nil {
				missing = true
			}
		}()
		p.database.BlindIndex(ModuleName)
	}()
	if missing {
		return fmt.Errorf("%w: %s computes the challenge digests under the database root key, and this "+
			"database has none. Configure a root key, or leave this module out of the command",
			authn.ErrNoEncryptionKey, ModuleName)
	}
	return nil
}
