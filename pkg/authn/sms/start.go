package sms

import (
	"context"
	"errors"
	"fmt"

	"github.com/vislake/speed/pkg/authn"
)

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

func (p *product) stop() error {
	return errors.Join(p.stopPool(), p.stopSweep())
}

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
