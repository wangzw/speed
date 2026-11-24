package password

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"time"

	"github.com/vislake/speed/pkg/authn/internal/acctcache"
)

// MaxCacheTTL is the longest a held check is remembered, the top of the
// lifetime setting's range.
const MaxCacheTTL = 5 * time.Minute

// Cache remembers that a password held against an entry, so a backend whose
// credential is presented on every request does not derive on every request.
// Only a held check is remembered, and the cache is not for an interactive login.
type Cache struct {
	secret [sha256.Size]byte
	store  *acctcache.Cache[time.Time]
	now    func() time.Time
}

// NewCache makes a cache of at most capacity checks. A capacity below one is one.
// now reads the time and nil reads the system clock.
func NewCache(capacity int, now func() time.Time) *Cache {
	if now == nil {
		now = time.Now
	}
	c := &Cache{store: acctcache.New[time.Time](capacity, MaxCacheTTL, now), now: now}
	if _, err := rand.Read(c.secret[:]); err != nil {
		// The system source of randomness failing is not a condition a
		// password check can carry on through: a secret that is not secret
		// would be a cache anybody could forge a hit in.
		panic("authn/password: the system source of randomness failed: " + err.Error())
	}
	return c
}

func (c *Cache) key(subject string, e Entry, password string) string {
	mac := hmac.New(sha256.New, c.secret[:])
	digest := e.Digest()
	// The variable-length segments are written with their lengths first, so
	// that a subject and a password whose concatenations are equal to another
	// pair's do not share a key. The digest is a fixed width and needs none.
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(subject)))
	mac.Write(n[:])
	mac.Write([]byte(subject))
	mac.Write(digest[:])
	binary.BigEndian.PutUint64(n[:], uint64(len(password)))
	mac.Write(n[:])
	mac.Write([]byte(password))
	return hex.EncodeToString(mac.Sum(nil))
}

// Hit reports whether this password held against this entry for this subject
// within ttl of the derivation that remembered it; the lifetime is absolute, and
// a ttl of zero or less is the cache switched off.
func (c *Cache) Hit(ttl time.Duration, subject string, e Entry, password string) bool {
	if ttl <= 0 {
		return false
	}
	stored, found := c.store.Get(c.key(subject, e, password))
	return found && c.now().Sub(stored) < ttl
}

// Remember records that the password held, at the time of the call, after a
// derivation and not after a hit; with a ttl of zero or less it records nothing.
func (c *Cache) Remember(ttl time.Duration, subject string, e Entry, password string) {
	if ttl <= 0 {
		return
	}
	c.store.Put(c.key(subject, e, password), c.now())
}
