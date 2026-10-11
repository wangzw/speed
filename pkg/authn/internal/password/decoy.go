package password

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
)

// Decoy is an entry no password derives, written with the current parameters,
// which a login that found no account derives against so that an unknown name
// costs what a known one does.
//
// It is made when it is first asked for and again whenever the parameters it
// was made with are not the ones asked for. It is not made once at start: a
// parameter changed while the process runs would leave the entries of known
// accounts derived at the new cost and the decoy at the old one, and the
// difference in time would tell a caller which names exist.
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

// randomText is text no one chose: 32 bytes from the system's source, as hex.
func randomText() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("authn/password: the system source of randomness failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}
