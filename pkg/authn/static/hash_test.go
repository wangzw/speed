package static

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestAHashVerifiesThePasswordItWasMadeFromAndNoOther holds the round trip the
// whole table rests on.
func TestAHashVerifiesThePasswordItWasMadeFromAndNoOther(t *testing.T) {
	encoded, err := encode(2, []byte("0123456789abcdef"), "correct horse")
	if err != nil {
		t.Fatalf("deriving a hash: %v", err)
	}
	parsed, err := ParseHash(encoded)
	if err != nil {
		t.Fatalf("reading back the hash just written: %v", err)
	}

	if !parsed.matches("correct horse") {
		t.Error("the hash does not verify the password it was derived from")
	}
	for _, wrong := range []string{"", "correct hors", "correct horse ", "Correct horse"} {
		if parsed.matches(wrong) {
			t.Errorf("the hash verifies %q, which is not the password it was derived from", wrong)
		}
	}
}

// TestTwoHashesOfOnePasswordDiffer holds the salt.
//
// Without it, two accounts sharing a password would hold the same hash, and one
// table entry would confirm the other: anybody who could read the file would
// learn which accounts share a password without breaking any of them.
func TestTwoHashesOfOnePasswordDiffer(t *testing.T) {
	first, err := Hash("shared")
	if err != nil {
		t.Fatalf("deriving the first hash: %v", err)
	}
	second, err := Hash("shared")
	if err != nil {
		t.Fatalf("deriving the second hash: %v", err)
	}

	if first == second {
		t.Error("two hashes of one password are identical, so the salt is not being drawn fresh")
	}
	for _, encoded := range []string{first, second} {
		parsed, err := ParseHash(encoded)
		if err != nil {
			t.Fatalf("reading back a hash: %v", err)
		}
		if !parsed.matches("shared") {
			t.Error("a freshly drawn hash does not verify its own password")
		}
	}
}

// TestAHashCarriesTheCostItWasMadeAt holds why raising the default does not
// invalidate the tables already written: the count travels inside the hash, so
// an existing entry keeps verifying at the cost it was made with.
func TestAHashCarriesTheCostItWasMadeAt(t *testing.T) {
	encoded, err := encode(3, []byte("0123456789abcdef"), "secret")
	if err != nil {
		t.Fatalf("deriving a hash: %v", err)
	}
	parsed, err := ParseHash(encoded)
	if err != nil {
		t.Fatalf("reading back the hash: %v", err)
	}
	if parsed.iterations != 3 {
		t.Errorf("the hash reads back at %d iterations, want the 3 it was made at",
			parsed.iterations)
	}
	if !parsed.matches("secret") {
		t.Error("a hash made at a cost other than the current default does not verify")
	}
}

// TestWhatCannotBeAHashIsRefusedWhenItIsRead holds the shapes ParseHash turns
// away. Every table entry goes through it at startup, so each of these is a
// startup failure rather than a refusal only the holder of that account would
// ever run into.
func TestWhatCannotBeAHashIsRefusedWhenItIsRead(t *testing.T) {
	for _, c := range []struct {
		name    string
		encoded string
	}{
		{"a plain password somebody wrote in by hand", "correct horse"},
		{"too few fields", "pbkdf2-sha256$600000$c2FsdA"},
		{"too many fields", "pbkdf2-sha256$600000$c2FsdA$a2V5$extra"},
		{"a scheme this backend does not read", "scrypt$600000$c2FsdA$a2V5"},
		{"an iteration count that is not a number", "pbkdf2-sha256$many$c2FsdA$a2V5"},
		{"an iteration count of zero", "pbkdf2-sha256$0$c2FsdA$a2V5"},
		{"a negative iteration count", "pbkdf2-sha256$-1$c2FsdA$a2V5"},
		{"a salt that is not base64", "pbkdf2-sha256$600000$not base64$a2V5"},
		{"an empty salt", "pbkdf2-sha256$600000$$a2V5"},
		{"a key that is not base64", "pbkdf2-sha256$600000$c2FsdA$not base64"},
		{"an empty key", "pbkdf2-sha256$600000$c2FsdA$"},
		{"nothing at all", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseHash(c.encoded)
			if !errors.Is(err, ErrMalformedHash) {
				t.Fatalf("reading %q gave %v, want one wrapping %v",
					c.encoded, err, ErrMalformedHash)
			}
		})
	}
}

// TestAKeyOfAnotherLengthIsRefusedWhenItIsRead holds the length of the derived
// key against the one verification derives.
//
// Verification derives keyLength bytes and compares them in constant time, and
// a constant-time comparison of two different lengths never matches. A
// well-formed pbkdf2-sha256 hash carrying a key of any other length — what
// another tool writes when it is set to a different length — therefore refuses
// the right password for ever, and the only person who would ever find out is
// the holder of that account. It is refused when the table is read instead, as
// every other entry that could never verify is.
func TestAKeyOfAnotherLengthIsRefusedWhenItIsRead(t *testing.T) {
	salt := []byte("0123456789abcdef")
	for _, length := range []int{16, keyLength - 1, keyLength + 1, 64} {
		key, err := pbkdf2.Key(sha256.New, "correct horse", salt, 2, length)
		if err != nil {
			t.Fatalf("deriving a %d-byte key: %v", length, err)
		}
		encoded := strings.Join([]string{
			hashScheme, "2", hashEncoding.EncodeToString(salt),
			hashEncoding.EncodeToString(key),
		}, hashSeparator)

		_, err = ParseHash(encoded)
		if !errors.Is(err, ErrMalformedHash) {
			t.Errorf("a hash carrying a %d-byte key read back with %v, want a failure wrapping "+
				"%v: verification derives %d bytes, so it could never verify",
				length, err, ErrMalformedHash, keyLength)
		}
	}
}

// fipsChild marks the copy of the test binary that
// TestASaltThisProcessCannotDeriveWithIsRefusedWhenItIsRead starts under FIPS
// 140-only mode.
const fipsChild = "SPEED_AUTHN_STATIC_FIPS_CHILD"

// TestASaltThisProcessCannotDeriveWithIsRefusedWhenItIsRead holds the same rule
// for what the derivation itself refuses. In FIPS 140-only mode the standard
// library will not derive from a salt shorter than 128 bits, so an entry
// carrying one reads as well formed and then refuses the right password for
// ever. Whether a derivation can run is the derivation's own answer, and it is
// asked when the table is read.
//
// The mode is fixed when a process starts, so the case runs in a copy of this
// test binary started under it.
func TestASaltThisProcessCannotDeriveWithIsRefusedWhenItIsRead(t *testing.T) {
	if os.Getenv(fipsChild) == "" {
		child := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
		child.Env = append(os.Environ(), fipsChild+"=1", "GODEBUG=fips140=only")
		if out, err := child.CombinedOutput(); err != nil {
			t.Fatalf("the case failed in FIPS 140-only mode: %v\n%s", err, out)
		}
		return
	}

	// Eight bytes of salt: enough for this backend's format, short of what
	// the derivation accepts in this mode. The key is any 32 bytes, since
	// the case is about reading the entry, not about deriving it.
	short := strings.Join([]string{
		hashScheme, "2", hashEncoding.EncodeToString([]byte("8 bytes!")),
		hashEncoding.EncodeToString(make([]byte, keyLength)),
	}, hashSeparator)
	if _, err := ParseHash(short); !errors.Is(err, ErrMalformedHash) {
		t.Errorf("a hash with an 8-byte salt read back with %v in FIPS 140-only mode, want a "+
			"failure wrapping %v: the derivation refuses that salt here, so it could never verify",
			err, ErrMalformedHash)
	}

	// A hash Hash wrote is one the derivation accepts in this mode too.
	written, err := Hash("correct horse")
	if err != nil {
		t.Fatalf("writing a hash in FIPS 140-only mode: %v", err)
	}
	parsed, err := ParseHash(written)
	if err != nil {
		t.Fatalf("reading back a hash Hash wrote in FIPS 140-only mode: %v", err)
	}
	if !parsed.matches("correct horse") {
		t.Error("a hash Hash wrote does not verify its own password in FIPS 140-only mode")
	}
}

// TestReadingAHashNeverEchoesIt holds the rule an error about a credential
// table has to keep: the thing it is complaining about is the secret.
func TestReadingAHashNeverEchoesIt(t *testing.T) {
	const entry = "pbkdf2-sha256$600000$c2VjcmV0c2FsdA$c2VjcmV0a2V5bWF0ZXJpYWw"

	// Each case fails on one field, so that its text could quote the others:
	// a key that is not base64, and the entry as written, whose key is
	// readable and of a length verification never derives.
	for _, broken := range []string{
		strings.Replace(entry, "c2VjcmV0a2V5bWF0ZXJpYWw", "not base64!", 1),
		entry,
	} {
		_, err := ParseHash(broken)
		if err == nil {
			t.Fatalf("the hash %q parsed without complaint", broken)
		}
		for _, secret := range []string{"c2VjcmV0c2FsdA", "c2VjcmV0a2V5bWF0ZXJpYWw", "not base64!"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the failure reads %q and carries %q out of the stored hash", err, secret)
			}
		}
	}
}

// TestTheDefaultCostIsTheOneThatGetsWritten holds that Hash does not quietly
// write a cheaper hash than the constant says.
func TestTheDefaultCostIsTheOneThatGetsWritten(t *testing.T) {
	encoded, err := Hash("secret")
	if err != nil {
		t.Fatalf("deriving a hash: %v", err)
	}
	parsed, err := ParseHash(encoded)
	if err != nil {
		t.Fatalf("reading back the hash: %v", err)
	}
	if parsed.iterations != hashIterations {
		t.Errorf("Hash wrote %d iterations, want the %d the constant states",
			parsed.iterations, hashIterations)
	}
	if len(parsed.salt) != saltLength {
		t.Errorf("Hash wrote a salt of %d byte(s), want %d", len(parsed.salt), saltLength)
	}
	if len(parsed.key) != keyLength {
		t.Errorf("Hash wrote a key of %d byte(s), want %d", len(parsed.key), keyLength)
	}
}

// TestAHashAtAGivenCountIsDerivedAtThatCount holds what HashAt is for: a host
// or a test that chooses the cost gets an entry at that cost, and one that
// verifies the password it was made from and no other.
//
// Reading the count back and verifying are both needed. A writer that ignored
// the count would read back at the default; one that wrote the count asked for
// and derived at another would read back right and never verify, since
// verification derives at the count the entry carries.
func TestAHashAtAGivenCountIsDerivedAtThatCount(t *testing.T) {
	for _, iterations := range []int{1, 3, 1_000} {
		encoded, err := HashAt("correct horse", iterations)
		if err != nil {
			t.Fatalf("deriving a hash at %d iteration(s): %v", iterations, err)
		}
		parsed, err := ParseHash(encoded)
		if err != nil {
			t.Fatalf("reading back the hash HashAt wrote at %d iteration(s): %v", iterations, err)
		}
		if parsed.iterations != iterations {
			t.Errorf("HashAt was asked for %d iteration(s) and wrote %d", iterations,
				parsed.iterations)
		}
		if len(parsed.salt) != saltLength {
			t.Errorf("HashAt wrote a salt of %d byte(s), want %d", len(parsed.salt), saltLength)
		}
		if !parsed.matches("correct horse") {
			t.Errorf("the hash HashAt wrote at %d iteration(s) does not verify the password it "+
				"was derived from", iterations)
		}
		if parsed.matches("correct hors") {
			t.Errorf("the hash HashAt wrote at %d iteration(s) verifies a password it was not "+
				"derived from", iterations)
		}
	}
}

// TestTwoHashesAtOneCountDiffer holds the salt for HashAt as
// TestTwoHashesOfOnePasswordDiffer holds it for Hash: a writer that takes the
// cost from its caller still draws the salt itself, fresh on every call.
func TestTwoHashesAtOneCountDiffer(t *testing.T) {
	first, err := HashAt("shared", 2)
	if err != nil {
		t.Fatalf("deriving the first hash: %v", err)
	}
	second, err := HashAt("shared", 2)
	if err != nil {
		t.Fatalf("deriving the second hash: %v", err)
	}
	if first == second {
		t.Error("two hashes of one password at one count are identical, so the salt is not " +
			"being drawn fresh")
	}
}

// TestACountNoEntryCanCarryIsRefusedRatherThanWritten holds HashAt to what
// ParseHash reads. The standard library's derivation takes a count below one
// as one and does not complain, so without this refusal HashAt would hand back
// an entry that every startup reading the table then turns away.
func TestACountNoEntryCanCarryIsRefusedRatherThanWritten(t *testing.T) {
	for _, iterations := range []int{0, -1, math.MinInt} {
		encoded, err := HashAt("correct horse", iterations)
		if !errors.Is(err, ErrMalformedHash) {
			t.Errorf("HashAt at %d iteration(s) gave %v, want a failure wrapping %v",
				iterations, err, ErrMalformedHash)
		}
		if encoded != "" {
			t.Errorf("HashAt at %d iteration(s) handed back an entry alongside its failure",
				iterations)
		}
	}
}

// TestTheStoredFormatIsTheOneTheDesignStates holds the format from both sides,
// written out here in literals rather than through this package's constants.
// A table in a configuration file outlives the binary that reads it, and an
// entry may come from a tool other than this package, so the format is a
// contract: renaming the scheme, changing the separator or the base64 variant,
// or deriving another key length breaks every table already written, and this
// case is what goes red when one of them changes.
func TestTheStoredFormatIsTheOneTheDesignStates(t *testing.T) {
	const password = "correct horse"

	t.Run("an entry written to the format elsewhere is read and verifies", func(t *testing.T) {
		salt := []byte("sixteen byte slt")
		key, err := pbkdf2.Key(sha256.New, password, salt, 7, 32)
		if err != nil {
			t.Fatalf("deriving the key: %v", err)
		}
		entry := "pbkdf2-sha256$7$" + base64.RawStdEncoding.EncodeToString(salt) + "$" +
			base64.RawStdEncoding.EncodeToString(key)

		parsed, err := ParseHash(entry)
		if err != nil {
			t.Fatalf("an entry in the stated format read back with %v", err)
		}
		if !parsed.matches(password) {
			t.Error("an entry in the stated format does not verify the password it was " +
				"derived from")
		}
	})

	t.Run("what HashAt writes is the format", func(t *testing.T) {
		entry, err := HashAt(password, 7)
		if err != nil {
			t.Fatalf("deriving a hash: %v", err)
		}
		fields := strings.Split(entry, "$")
		if len(fields) != 4 {
			t.Fatalf("HashAt wrote %d '$'-separated field(s), want 4", len(fields))
		}
		if fields[0] != "pbkdf2-sha256" || fields[1] != "7" {
			t.Errorf("HashAt wrote the scheme %q and the count %q, want %q and %q",
				fields[0], fields[1], "pbkdf2-sha256", "7")
		}
		salt, err := base64.RawStdEncoding.DecodeString(fields[2])
		if err != nil || len(salt) != 16 {
			t.Fatalf("the salt HashAt wrote is not unpadded base64 of 16 bytes: %d byte(s), %v",
				len(salt), err)
		}
		key, err := base64.RawStdEncoding.DecodeString(fields[3])
		if err != nil {
			t.Fatalf("the key HashAt wrote is not unpadded base64: %v", err)
		}
		want, err := pbkdf2.Key(sha256.New, password, salt, 7, 32)
		if err != nil {
			t.Fatalf("deriving the key independently: %v", err)
		}
		if !bytes.Equal(key, want) {
			t.Error("the key HashAt wrote is not PBKDF2-HMAC-SHA-256 of the password over the " +
				"salt it wrote, at the count it wrote, 32 bytes long")
		}
	})
}
