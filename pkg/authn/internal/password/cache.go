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

// MaxCacheTTL is the longest a check that held may be remembered, which is the
// top of the range the lifetime setting takes. The store behind the cache keeps
// an entry that long and the lifetime in force is read against the time it was
// stored, so a lifetime that is lowered at run time takes effect on the entries
// already there.
const MaxCacheTTL = 5 * time.Minute

// Cache remembers that a password held against an entry, so that a backend whose
// credential is presented on every request does not derive on every request.
//
// A key is the HMAC-SHA-256, under a secret drawn when the Cache is made and
// never written anywhere, of the subject, the digest of the entry and the
// password. The password and nothing reversible from it is kept, and the
// secret's loss with the process is what makes a stolen copy of the cache worth
// nothing. Only a check that held is remembered, so a wrong password costs a
// derivation every time and the cache gives nobody a faster way to be refused.
// An entry that is written again has a new digest, so the checks against the
// old one are never found.
//
// The cache is for the backend whose credential is presented on every request,
// and not for an interactive login: a login has no repeated derivation to save,
// and the difference in time between a hit and a miss would tell a caller that
// this password had held lately.
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
// within ttl of the derivation that remembered it. The lifetime is absolute: it
// runs from that derivation, and a hit does not extend it, so a password
// presented more often than ttl is still derived once per ttl. A ttl of zero or
// less is the cache switched off.
func (c *Cache) Hit(ttl time.Duration, subject string, e Entry, password string) bool {
	if ttl <= 0 {
		return false
	}
	stored, found := c.store.Get(c.key(subject, e, password))
	return found && c.now().Sub(stored) < ttl
}

// Remember records that the password held, at the time of the call. The caller
// remembers a password after a derivation that vouched for it and not after a
// hit, which is what keeps the lifetime absolute. With a ttl of zero or less it
// records nothing.
func (c *Cache) Remember(ttl time.Duration, subject string, e Entry, password string) {
	if ttl <= 0 {
		return
	}
	c.store.Put(c.key(subject, e, password), c.now())
}
