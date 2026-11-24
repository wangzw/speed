package password

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Decoy is an entry no password derives, written with the current parameters,
// which a login that found no account derives against so an unknown name costs
// what a known one does. It is remade whenever the parameters change.
type Decoy struct {
	// text draws the password a decoy is made from; nil draws from the
	// system's source of randomness, and a test replaces it to see the draws.
	text func() string

	mu    sync.Mutex
	made  Params
	entry Entry
	ready bool
}

// For is the decoy entry for the parameters, made if there is none for them yet.
// The password it is made from is drawn fresh and thrown away, so no password
// matches it.
func (d *Decoy) For(p Params) (Entry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ready && d.made == p {
		return d.entry, nil
	}
	draw := d.text
	if draw == nil {
		draw = randomText
	}
	encoded, err := p.Hash(draw())
	if err != nil {
		return Entry{}, err
	}
	e, err := Parse(encoded)
	if err != nil {
		return Entry{}, err
	}
	d.made, d.entry, d.ready = p, e, true
	return e, nil
}

func randomText() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("authn/password: the system source of randomness failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
