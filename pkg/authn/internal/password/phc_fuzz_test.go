package password

import (
	"errors"
	"testing"
)

// FuzzParse holds what ADR-authn-password-hash names as the guard of its second
// failure condition: whatever Parse is handed it neither panics nor returns an
// error that is not ErrMalformedHash, and an entry it accepts is within every
// bound, is one the writer would write, and converts to PHC and back.
func FuzzParse(f *testing.F) {
	salt, key := variedBytes(16), variedBytes(32)
	for _, s := range []string{
		"pbkdf2-sha256$5$" + rawB64(salt) + "$" + rawB64(key),
		"$pbkdf2-sha256$i=5,l=32$" + rawB64(salt) + "$" + rawB64(key),
		argonLiteral(64, 1, 1, salt, key),
		"$argon2id$v=19$m=4194304,t=10,p=16$" + rawB64(salt) + "$" + rawB64(key),
		"", "$", "$$$$$$", "pbkdf2-sha256$$$", "$argon2id$v=19$m=1,t=1,p=1$$",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		e, err := Parse(s)
		if err != nil {
			if !errors.Is(err, ErrMalformedHash) {
				t.Fatalf("Parse(%q) failed with %v, which is not ErrMalformedHash", s, err)
			}
			return
		}
		p := e.Params()
		switch p.Algorithm {
		case Argon2id:
			if p.MemoryKiB > MaxMemoryKiB || p.Iterations > MaxIterations || p.Parallelism > MaxParallelism ||
				p.MemoryKiB < 8*uint32(p.Parallelism) || p.Iterations < 1 || p.Parallelism < 1 {
				t.Fatalf("Parse(%q) accepted out-of-bound argon2id params %+v", s, p)
			}
		case PBKDF2SHA256:
			if p.PBKDF2Iterations < 1 || p.PBKDF2Iterations > MaxPBKDF2Iterations {
				t.Fatalf("Parse(%q) accepted out-of-bound pbkdf2 iterations %d", s, p.PBKDF2Iterations)
			}
		default:
			t.Fatalf("Parse(%q) accepted algorithm %q", s, p.Algorithm)
		}
		if err := p.Check(); err != nil {
			t.Fatalf("Parse(%q) accepted %+v, which the writer's Check refuses: %v", s, p, err)
		}
		if phc, perr := e.PHC(); perr == nil {
			if !e.Legacy() && phc != s {
				t.Fatalf("a PHC entry does not convert to itself: %q -> %q", s, phc)
			}
			if again, err := Parse(phc); err != nil || again.Params() != p {
				t.Fatalf("the PHC form of %q does not read back: %v", s, err)
			}
		}
	})
}
