package password

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func variedBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + 1)
	}
	return b
}

func rawB64(b []byte) string { return base64.RawStdEncoding.EncodeToString(b) }

func argonLiteral(m, t uint32, p uint8, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", m, t, p, rawB64(salt), rawB64(key))
}

// TestAnEntryOfAnyAcceptedSaltAndHashLengthVerifiesItsPassword: Verify at every salt and hash length an entry may carry.
func TestAnEntryOfAnyAcceptedSaltAndHashLengthVerifiesItsPassword(t *testing.T) {
	const plaintext = "correct horse"
	for _, saltLen := range []int{8, 16, 17, 64} {
		for _, keyLen := range []int{16, 32, 64} {
			salt := variedBytes(saltLen)
			t.Run(fmt.Sprintf("salt%d/key%d", saltLen, keyLen), func(t *testing.T) {
				pk, err := pbkdf2.Key(sha256.New, plaintext, salt, 3, keyLen)
				if err != nil {
					t.Fatal(err)
				}
				ak := argon2.IDKey([]byte(plaintext), salt, 1, 64, 1, uint32(keyLen))
				for name, lit := range map[string]string{
					"pbkdf2":   "$pbkdf2-sha256$i=3,l=" + strconv.Itoa(keyLen) + "$" + rawB64(salt) + "$" + rawB64(pk),
					"argon2id": argonLiteral(64, 1, 1, salt, ak),
				} {
					e, err := Parse(lit)
					if err != nil {
						t.Fatalf("%s: the entry is refused: %v", name, err)
					}
					if ok, err := e.Verify(plaintext); err != nil || !ok {
						t.Errorf("%s: the password the entry was derived from gives %v, %v", name, ok, err)
					}
					if ok, _ := e.Verify(plaintext + "x"); ok {
						t.Errorf("%s: another password verifies", name)
					}
				}
			})
		}
	}
}

// TestEveryByteOfTheStoredHashTakesPartInTheComparison: every byte of the stored hash takes part in the comparison.
func TestEveryByteOfTheStoredHashTakesPartInTheComparison(t *testing.T) {
	const plaintext = "correct horse"
	salt := variedBytes(16)
	pk, err := pbkdf2.Key(sha256.New, plaintext, salt, 3, 32)
	if err != nil {
		t.Fatal(err)
	}
	ak := argon2.IDKey([]byte(plaintext), salt, 1, 64, 1, 32)
	forms := map[string]func(key []byte) string{
		"pbkdf2":   func(k []byte) string { return "$pbkdf2-sha256$i=3,l=32$" + rawB64(salt) + "$" + rawB64(k) },
		"argon2id": func(k []byte) string { return argonLiteral(64, 1, 1, salt, k) },
	}
	for name, key := range map[string][]byte{"pbkdf2": pk, "argon2id": ak} {
		build := forms[name]
		e, err := Parse(build(key))
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := e.Verify(plaintext); err != nil || !ok {
			t.Fatalf("%s: the untouched entry does not verify: %v, %v", name, ok, err)
		}
		for pos := range key {
			tampered := bytes.Clone(key)
			tampered[pos] ^= 0x01
			e, err := Parse(build(tampered))
			if err != nil {
				t.Fatal(err)
			}
			if ok, _ := e.Verify(plaintext); ok {
				t.Errorf("%s: an entry whose hash differs from the derived one at byte %d verifies", name, pos)
			}
		}
	}
}

// flipLowBit changes the last base64 character to one that differs only in the
// bits after the last full byte, which a strict decoder refuses and a lenient
// one drops.
func flipLowBit(s string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	last := strings.IndexByte(alphabet, s[len(s)-1])
	return s[:len(s)-1] + string(alphabet[last^1])
}

// TestTheLegacyReaderStaysLenientAndThePHCReadersStayStrictAboutTrailingBits: the legacy reader keeps its leniency about trailing bits, and the PHC
// readers do not share it, in the salt as in the hash.
func TestTheLegacyReaderStaysLenientAndThePHCReadersStayStrictAboutTrailingBits(t *testing.T) {
	const plaintext = "correct horse"
	salt := variedBytes(16)
	key, err := pbkdf2.Key(sha256.New, plaintext, salt, 5, 32)
	if err != nil {
		t.Fatal(err)
	}
	saltS, keyS := rawB64(salt), rawB64(key)
	saltLoose, keyLoose := flipLowBit(saltS), flipLowBit(keyS)
	for _, s := range []struct{ loose, exact string }{{saltLoose, saltS}, {keyLoose, keyS}} {
		got, err := base64.RawStdEncoding.DecodeString(s.loose)
		want, _ := base64.RawStdEncoding.DecodeString(s.exact)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("the precondition fails: %q should decode to the same bytes as %q", s.loose, s.exact)
		}
		if _, err := b64.DecodeString(s.loose); err == nil {
			t.Fatalf("the precondition fails: %q should be refused by a strict decoder", s.loose)
		}
	}

	for name, legacy := range map[string]string{
		"salt": "pbkdf2-sha256$5$" + saltLoose + "$" + keyS,
		"hash": "pbkdf2-sha256$5$" + saltS + "$" + keyLoose,
	} {
		e, err := Parse(legacy)
		if err != nil {
			t.Errorf("a legacy entry with trailing bits in its %s is refused, which would fail every table written that way at startup: %v", name, err)
			continue
		}
		if ok, err := e.Verify(plaintext); err != nil || !ok {
			t.Errorf("a legacy entry with trailing bits in its %s does not verify: %v, %v", name, ok, err)
		}
	}

	for name, phc := range map[string]string{
		"pbkdf2 salt": "$pbkdf2-sha256$i=5,l=32$" + saltLoose + "$" + keyS,
		"pbkdf2 hash": "$pbkdf2-sha256$i=5,l=32$" + saltS + "$" + keyLoose,
		"argon2 salt": "$argon2id$v=19$m=64,t=1,p=1$" + saltLoose + "$" + keyS,
		"argon2 hash": "$argon2id$v=19$m=64,t=1,p=1$" + saltS + "$" + keyLoose,
	} {
		if _, err := Parse(phc); !errors.Is(err, ErrMalformedHash) {
			t.Errorf("%s with trailing bits gave %v, want one wrapping %v", name, err, ErrMalformedHash)
		}
	}
}

// TestALineEndingIsRefusedInEveryFormWhereverItSits: a carriage return or a line feed is refused in every form, at the end and
// inside, because Go's base64 decoders skip both.
func TestALineEndingIsRefusedInEveryFormWhereverItSits(t *testing.T) {
	salt, key := variedBytes(16), variedBytes(32)
	entries := map[string]string{
		"legacy":   "pbkdf2-sha256$5$" + rawB64(salt) + "$" + rawB64(key),
		"pbkdf2":   "$pbkdf2-sha256$i=5,l=32$" + rawB64(salt) + "$" + rawB64(key),
		"argon2id": argonLiteral(64, 1, 1, salt, key),
	}
	for name, good := range entries {
		if _, err := Parse(good); err != nil {
			t.Fatalf("%s: the control entry is refused: %v", name, err)
		}
		cut := len(good) - 10
		for label, spoiled := range map[string]string{
			"LF at the end":   good + "\n",
			"CR at the end":   good + "\r",
			"CRLF at the end": good + "\r\n",
			"LF inside":       good[:cut] + "\n" + good[cut:],
			"CR inside":       good[:cut] + "\r" + good[cut:],
		} {
			if _, err := Parse(spoiled); !errors.Is(err, ErrMalformedHash) {
				t.Errorf("%s with %s gave %v, want one wrapping %v", name, label, err, ErrMalformedHash)
			}
		}
	}
}

// TestALegacySaltOfExactlyTheLeastPHCCarriesConverts: a legacy entry becomes PHC exactly when its salt is one PHC may carry.
func TestALegacySaltOfExactlyTheLeastPHCCarriesConverts(t *testing.T) {
	const plaintext = "correct horse"
	for _, c := range []struct {
		saltLen int
		ok      bool
	}{{1, false}, {7, false}, {8, true}, {9, true}} {
		salt := variedBytes(c.saltLen)
		key, err := pbkdf2.Key(sha256.New, plaintext, salt, 4, 32)
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := Parse("pbkdf2-sha256$4$" + rawB64(salt) + "$" + rawB64(key))
		if err != nil {
			t.Fatal(err)
		}
		converted, err := legacy.PHC()
		if !c.ok {
			if !errors.Is(err, ErrMalformedHash) {
				t.Errorf("a legacy salt of %d bytes converts with %v, want one wrapping %v", c.saltLen, err, ErrMalformedHash)
			}
			continue
		}
		if err != nil {
			t.Errorf("a legacy salt of %d bytes is refused: %v", c.saltLen, err)
			continue
		}
		again, err := Parse(converted)
		if err != nil {
			t.Errorf("the conversion of a legacy salt of %d bytes is not read back: %v", c.saltLen, err)
			continue
		}
		if ok, err := again.Verify(plaintext); err != nil || !ok {
			t.Errorf("the conversion of a legacy salt of %d bytes does not verify: %v, %v", c.saltLen, ok, err)
		}
	}
}

// TestTheWriterAcceptsExactlyWhatTheReaderAccepts: the writer accepts exactly the parameters the reader accepts, at every
// edge, without deriving anything.
func TestTheWriterAcceptsExactlyWhatTheReaderAccepts(t *testing.T) {
	salt, key := variedBytes(16), variedBytes(32)
	ms := []uint32{0, 1, 7, 8, 15, 16, 17, 63, 64, 127, 128, 129, 262143, 262144, 262145}
	ts := []uint32{0, 1, 2, 9, 10, 11}
	ps := []uint8{0, 1, 2, 3, 15, 16, 17}
	accepted := 0
	for _, m := range ms {
		for _, ti := range ts {
			for _, p := range ps {
				params := Params{Algorithm: Argon2id, MemoryKiB: m, Iterations: ti, Parallelism: p}
				_, readErr := Parse(argonLiteral(m, ti, p, salt, key))
				writeErr := params.Check()
				if (readErr == nil) != (writeErr == nil) {
					t.Errorf("argon2id m=%d t=%d p=%d: the reader says %v and the writer says %v", m, ti, p, readErr, writeErr)
				}
				if readErr == nil {
					accepted++
				}
			}
		}
	}
	for _, i := range []int{0, 1, 2, 599999, 600000, 9999999, 10000000, 10000001} {
		lit := "$pbkdf2-sha256$i=" + strconv.Itoa(i) + ",l=32$" + rawB64(salt) + "$" + rawB64(key)
		_, readErr := Parse(lit)
		writeErr := Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: i}.Check()
		if (readErr == nil) != (writeErr == nil) {
			t.Errorf("pbkdf2 i=%d: the reader says %v and the writer says %v", i, readErr, writeErr)
		}
		if readErr == nil {
			accepted++
		}
	}
	if accepted < 30 {
		t.Errorf("only %d parameter sets are accepted, so the grid does not reach the accepted edges", accepted)
	}
}

// TestAParameterSetNoEntryCouldCarryIsBelowTheFloor: a parameter set no entry could carry is below the floor.
func TestAParameterSetNoEntryCouldCarryIsBelowTheFloor(t *testing.T) {
	for name, p := range map[string]Params{
		"no lanes":       {Algorithm: Argon2id, MemoryKiB: 1 << 20, Iterations: 10, Parallelism: 0},
		"no iterations":  {Algorithm: Argon2id, MemoryKiB: 1 << 20, Iterations: 0, Parallelism: 1},
		"neither":        {Algorithm: Argon2id, MemoryKiB: 47104, Iterations: 0, Parallelism: 0},
		"pbkdf2 of none": {Algorithm: PBKDF2SHA256},
	} {
		if !p.BelowFloor() {
			t.Errorf("%s: %+v is not below the floor", name, p)
		}
	}
}

// TestAVerifyThatCannotDeriveAnswersNoAndWhy: a derivation that cannot run says no, and says why.
func TestAVerifyThatCannotDeriveAnswersNoAndWhy(t *testing.T) {
	e := Entry{params: Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 1}, salt: []byte("12345678"), key: nil}
	ok, err := e.Verify("anything")
	if err == nil {
		t.Fatal("a derivation the standard library refuses gave no error")
	}
	if ok {
		t.Error("a derivation that could not run reported a match alongside its error")
	}
	if errors.Unwrap(err) == nil {
		t.Errorf("the error %q does not carry the derivation's own, which is the reason", err)
	}
}

// TestTheZeroEntryIsMalformedAndNeverMatches: an entry that was never read from
// anything answers no, and the error says it is malformed.
func TestTheZeroEntryIsMalformedAndNeverMatches(t *testing.T) {
	for _, password := range []string{"", "anything"} {
		ok, err := Entry{}.Verify(password)
		if ok || !errors.Is(err, ErrMalformedHash) {
			t.Errorf("the zero entry gave %v, %v for %q, want no match and an error wrapping %v", ok, err, password, ErrMalformedHash)
		}
	}
}
