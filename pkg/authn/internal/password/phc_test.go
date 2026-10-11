package password

import (
	"bytes"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// cheapArgon2id is an Argon2id parameter set a test can afford. It is far below
// what OWASP recommends and is read all the same: the reader sets no floor.
var cheapArgon2id = Params{Algorithm: Argon2id, MemoryKiB: 64, Iterations: 1, Parallelism: 1}

// salt16 and salt8 are fixed salts for the cases that write an entry by hand.
var (
	salt16 = []byte("sixteen byte slt")
	salt8  = []byte("8 bytes!")
)

// pbkdf2Literal writes a PBKDF2 PHC entry the way the design states it, from the
// standard library's derivation and the encoding package, so that a case reading
// it does not go through this package's own writer.
func pbkdf2Literal(t *testing.T, plaintext string, salt []byte, iterations, length int) string {
	t.Helper()
	key, err := pbkdf2.Key(sha256.New, plaintext, salt, iterations, length)
	if err != nil {
		t.Fatalf("deriving a key: %v", err)
	}
	return "$pbkdf2-sha256$i=" + strconv.Itoa(iterations) + ",l=" + strconv.Itoa(length) + "$" +
		base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
}

// TestAnEntryIsReadBackAtTheParametersItWasWrittenWith holds the round trip for
// each algorithm, and holds that the entry verifies the password it was made from
// and no other. Reading the parameters back and verifying are both needed: a
// writer that ignored a parameter would read back at the default, and one that
// stated a parameter and derived at another would read back right and never
// verify.
func TestAnEntryIsReadBackAtTheParametersItWasWrittenWith(t *testing.T) {
	for _, p := range []Params{
		cheapArgon2id,
		{Algorithm: Argon2id, MemoryKiB: 128, Iterations: 3, Parallelism: 2},
		DefaultParams(),
		{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 1},
		{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 1000},
	} {
		t.Run(string(p.Algorithm)+"/"+strconv.Itoa(int(p.MemoryKiB))+"/"+strconv.Itoa(p.PBKDF2Iterations), func(t *testing.T) {
			encoded, err := p.Hash("correct horse")
			if err != nil {
				t.Fatalf("writing an entry: %v", err)
			}
			e, err := Parse(encoded)
			if err != nil {
				t.Fatalf("reading back the entry just written: %v", err)
			}
			if e.Params() != p {
				t.Errorf("the entry reads back as %+v, want the %+v it was written with", e.Params(), p)
			}
			if e.Legacy() {
				t.Error("an entry in the PHC form reads as the legacy form")
			}
			if ok, err := e.Verify("correct horse"); err != nil || !ok {
				t.Errorf("the entry does not verify the password it was made from: %v, %v", ok, err)
			}
			for _, wrong := range []string{"", "correct hors", "correct horse ", "Correct horse"} {
				if ok, err := e.Verify(wrong); err != nil || ok {
					t.Errorf("the entry verifies %q, which is not its password: %v, %v", wrong, ok, err)
				}
			}
		})
	}
}

// TestTwoEntriesOfOnePasswordDiffer holds the salt: without a fresh one, two
// accounts sharing a password would hold the same entry, and anybody who could
// read the table would learn which share one without breaking either.
func TestTwoEntriesOfOnePasswordDiffer(t *testing.T) {
	for _, p := range []Params{cheapArgon2id, {Algorithm: PBKDF2SHA256, PBKDF2Iterations: 2}} {
		first, err := p.Hash("shared")
		if err != nil {
			t.Fatalf("writing the first entry: %v", err)
		}
		second, err := p.Hash("shared")
		if err != nil {
			t.Fatalf("writing the second entry: %v", err)
		}
		if first == second {
			t.Errorf("two %s entries of one password are identical, so the salt is not drawn fresh", p.Algorithm)
		}
	}
}

// TestTheStoredFormatIsTheOneTheDesignStates holds the format from the outside,
// in literals and not through this package's writer. A table outlives the binary
// that reads it and an entry may come from another tool, so the format is a
// contract: this case goes red when the field order, the separators, the
// parameter spelling or the base64 variant change.
func TestTheStoredFormatIsTheOneTheDesignStates(t *testing.T) {
	const plaintext = "correct horse"

	t.Run("an Argon2id entry written by the reference implementation's form", func(t *testing.T) {
		// Written once with the library under parameters m=64,t=1,p=1.
		encoded, err := cheapArgon2id.Hash(plaintext)
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Split(encoded, "$")
		if len(fields) != 6 || fields[0] != "" || fields[1] != "argon2id" || fields[2] != "v=19" ||
			fields[3] != "m=64,t=1,p=1" {
			t.Fatalf("the Argon2id entry is %q, want $argon2id$v=19$m=64,t=1,p=1$<salt>$<hash>", encoded)
		}
		for _, field := range fields[4:] {
			if strings.ContainsAny(field, "=") {
				t.Errorf("the entry carries padding in %q", field)
			}
		}
		if salt, err := base64.RawStdEncoding.DecodeString(fields[4]); err != nil || len(salt) != 16 {
			t.Errorf("the salt is not unpadded base64 of 16 bytes: %d, %v", len(salt), err)
		}
		if key, err := base64.RawStdEncoding.DecodeString(fields[5]); err != nil || len(key) != 32 {
			t.Errorf("the hash is not unpadded base64 of 32 bytes: %d, %v", len(key), err)
		}
	})

	t.Run("a PBKDF2 entry written elsewhere is read and verifies", func(t *testing.T) {
		e, err := Parse(pbkdf2Literal(t, plaintext, salt16, 7, 32))
		if err != nil {
			t.Fatalf("an entry in the stated form read back with %v", err)
		}
		if e.Params() != (Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 7}) {
			t.Errorf("the entry reads as %+v", e.Params())
		}
		if ok, _ := e.Verify(plaintext); !ok {
			t.Error("an entry in the stated form does not verify the password it was derived from")
		}
	})

	t.Run("what the writer writes is that form", func(t *testing.T) {
		encoded, err := Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 7}.Hash(plaintext)
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Split(encoded, "$")
		if len(fields) != 5 || fields[0] != "" || fields[1] != "pbkdf2-sha256" || fields[2] != "i=7,l=32" {
			t.Fatalf("the PBKDF2 entry is %q, want $pbkdf2-sha256$i=7,l=32$<salt>$<hash>", encoded)
		}
		salt, err := base64.RawStdEncoding.DecodeString(fields[3])
		if err != nil || len(salt) != 16 {
			t.Fatalf("the salt is not unpadded base64 of 16 bytes: %d, %v", len(salt), err)
		}
		key, err := base64.RawStdEncoding.DecodeString(fields[4])
		if err != nil {
			t.Fatalf("the hash is not unpadded base64: %v", err)
		}
		want, err := pbkdf2.Key(sha256.New, plaintext, salt, 7, 32)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(key, want) {
			t.Error("the hash is not PBKDF2-HMAC-SHA-256 of the password over the salt, at the count, 32 bytes")
		}
	})

	t.Run("the legacy entry is read and verifies", func(t *testing.T) {
		key, err := pbkdf2.Key(sha256.New, plaintext, salt16, 7, 32)
		if err != nil {
			t.Fatal(err)
		}
		legacy := "pbkdf2-sha256$7$" + base64.RawStdEncoding.EncodeToString(salt16) + "$" +
			base64.RawStdEncoding.EncodeToString(key)
		e, err := Parse(legacy)
		if err != nil {
			t.Fatalf("a legacy entry read back with %v", err)
		}
		if !e.Legacy() {
			t.Error("a legacy entry does not read as legacy")
		}
		if e.Params() != (Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 7}) {
			t.Errorf("the entry reads as %+v", e.Params())
		}
		if ok, _ := e.Verify(plaintext); !ok {
			t.Error("a legacy entry does not verify the password it was derived from")
		}
		if ok, _ := e.Verify(plaintext + "x"); ok {
			t.Error("a legacy entry verifies a password it was not derived from")
		}
	})

	t.Run("the legacy and the PHC forms of the same bytes are one hash", func(t *testing.T) {
		legacyKey, err := pbkdf2.Key(sha256.New, plaintext, salt16, 5, 32)
		if err != nil {
			t.Fatal(err)
		}
		phc, err := Parse(pbkdf2Literal(t, plaintext, salt16, 5, 32))
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := Parse("pbkdf2-sha256$5$" + base64.RawStdEncoding.EncodeToString(salt16) + "$" +
			base64.RawStdEncoding.EncodeToString(legacyKey))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(phc.key, legacy.key) || !bytes.Equal(phc.salt, legacy.salt) {
			t.Error("the two forms of one hash carry different bytes: moving a table over would change its hashes")
		}
	})
}

// TestWhatCannotBeAnEntryIsRefusedWhenItIsRead holds the shapes Parse turns away,
// each of which would otherwise reach a derivation or be accepted on the
// leniency of a decoder. Every one wraps ErrMalformedHash.
func TestWhatCannotBeAnEntryIsRefusedWhenItIsRead(t *testing.T) {
	good64 := base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	good16 := base64.RawStdEncoding.EncodeToString(salt16)
	argon := func(params string) string { return "$argon2id$v=19$" + params + "$" + good16 + "$" + good64 }
	pbkdf := func(params string) string { return "$pbkdf2-sha256$" + params + "$" + good16 + "$" + good64 }
	pbkdfGood := "i=1000,l=32"
	legacy := func(iterations, salt, key string) string {
		return "pbkdf2-sha256$" + iterations + "$" + salt + "$" + key
	}
	key32 := base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))

	for _, c := range []struct{ name, encoded string }{
		{"nothing at all", ""},
		{"a plain password written in by hand", "correct horse"},
		{"a dollar sign alone", "$"},
		{"an algorithm that is not read", "$scrypt$ln=15,r=8,p=1$" + good16 + "$" + good64},
		{"an algorithm in capitals", "$ARGON2ID$v=19$m=64,t=1,p=1$" + good16 + "$" + good64},
		{"a variant of Argon2 that is not Argon2id", "$argon2i$v=19$m=64,t=1,p=1$" + good16 + "$" + good64},

		{"an Argon2id entry with an old version", argon("m=64,t=1,p=1")[:len("$argon2id$v=")] + "16" + argon("m=64,t=1,p=1")[len("$argon2id$v=19"):]},
		{"an Argon2id entry with a parameter after the three", argon("m=64,t=1,p=1,x=2")},
		{"an Argon2id entry with a parameter repeated", argon("m=64,m=64,t=1,p=1")},
		{"an Argon2id entry with the parameters out of order", argon("t=1,m=64,p=1")},
		{"an Argon2id entry with a missing parameter", argon("m=64,t=1")},
		{"an Argon2id entry with a signed number", argon("m=+64,t=1,p=1")},
		{"an Argon2id entry with a leading zero", argon("m=064,t=1,p=1")},
		{"an Argon2id entry with no memory", argon("m=0,t=1,p=1")},
		{"an Argon2id entry with a zero iteration count", argon("m=64,t=0,p=1")},
		{"an Argon2id entry with no lanes", argon("m=64,t=1,p=0")},
		{"an Argon2id entry with a number past 32 bits", argon("m=4294967296,t=1,p=1")},
		{"an Argon2id entry with a space in the parameters", argon("m=64, t=1,p=1")},
		{"an Argon2id entry with padding", "$argon2id$v=19$m=64,t=1,p=1$" + good16 + "=$" + good64},
		{"an Argon2id entry with a line ending after it", argon("m=64,t=1,p=1") + "\n"},
		{"an Argon2id entry with a space before it", " " + argon("m=64,t=1,p=1")},
		{"an Argon2id entry with a tab in the salt", "$argon2id$v=19$m=64,t=1,p=1$" + good16[:4] + "\t" + good16[4:] + "$" + good64},
		{"an Argon2id entry with too few segments", "$argon2id$v=19$m=64,t=1,p=1$" + good16},
		{"an Argon2id entry with an extra segment", argon("m=64,t=1,p=1") + "$extra"},
		{"an Argon2id entry with memory below eight KiB for each lane", argon("m=15,t=1,p=2")},
		{"an Argon2id entry past the memory bound", argon("m=" + strconv.Itoa(MaxMemoryKiB+1) + ",t=1,p=1")},
		{"an Argon2id entry past the iteration bound", argon("m=64,t=" + strconv.Itoa(MaxIterations+1) + ",p=1")},
		{"an Argon2id entry past the lane bound", argon("m=1024,t=1,p=" + strconv.Itoa(MaxParallelism+1))},
		{"an Argon2id entry asking for four gigabytes", argon("m=4194304,t=1,p=1")},
		{"an Argon2id entry with a salt of 7 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 7)) + "$" + good64},
		{"an Argon2id entry with a salt of 65 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 65)) + "$" + good64},
		{"an Argon2id entry with a hash of 15 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + good16 + "$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 15))},
		{"an Argon2id entry with a hash of 65 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + good16 + "$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 65))},

		{"a PBKDF2 entry with a parameter missing", pbkdf("i=1000")},
		{"a PBKDF2 entry with an unknown parameter", pbkdf("i=1000,x=32")},
		{"a PBKDF2 entry with i repeated", pbkdf("i=1000,i=2000")},
		{"a PBKDF2 entry with l repeated", pbkdf("l=32,l=32")},
		{"a PBKDF2 entry with a third parameter", pbkdf("i=1000,l=32,x=1")},
		{"a PBKDF2 entry with the parameters out of order", pbkdf("l=32,i=1000")},
		{"a PBKDF2 entry with a signed number", pbkdf("i=+1000,l=32")},
		{"a PBKDF2 entry with a leading zero", pbkdf("i=01000,l=32")},
		{"a PBKDF2 entry with a zero count", pbkdf("i=0,l=32")},
		{"a PBKDF2 entry with a space in the parameters", pbkdf("i=1000, l=32")},
		{"a PBKDF2 entry with the count past its bound", pbkdf("i=" + strconv.Itoa(MaxPBKDF2Iterations+1) + ",l=32")},
		{"a PBKDF2 entry with the count past 32 bits", pbkdf("i=4294967296,l=32")},
		{"a PBKDF2 entry with l below the bound", "$pbkdf2-sha256$i=1000,l=15$" + good16 + "$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 15))},
		{"a PBKDF2 entry with l above the bound", "$pbkdf2-sha256$i=1000,l=65$" + good16 + "$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 65))},
		{"a PBKDF2 entry whose hash is not l bytes long", pbkdf("i=1000,l=48")},
		{"a PBKDF2 entry with a salt of 7 bytes", "$pbkdf2-sha256$i=1000,l=32$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 7)) + "$" + good64},
		{"a PBKDF2 entry with a salt of 65 bytes", "$pbkdf2-sha256$i=1000,l=32$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 65)) + "$" + good64},
		{"a PBKDF2 entry with a salt that is not base64", "$pbkdf2-sha256$i=1000,l=32$not base64$" + good64},
		{"a PBKDF2 entry with padding", "$pbkdf2-sha256$i=1000,l=32$" + good16 + "=$" + good64},
		{"a PBKDF2 entry with a line ending inside the hash", "$pbkdf2-sha256$i=1000,l=32$" + good16 + "$" + good64[:10] + "\n" + good64[10:]},
		{"a PBKDF2 entry with too few segments", "$pbkdf2-sha256$i=1000,l=32$" + good16},
		{"a PBKDF2 entry with trailing bits that are not zero", "$pbkdf2-sha256$i=1000,l=32$" + good16 + "$" + good64[:len(good64)-1] + "B"},

		{"a legacy entry with too few fields", "pbkdf2-sha256$600000$c2FsdA"},
		{"a legacy entry with too many fields", legacy("1000", good16, key32) + "$extra"},
		{"a legacy entry of another scheme", "scrypt$1000$" + good16 + "$" + key32},
		{"a legacy entry with a count that is not a number", legacy("many", good16, key32)},
		{"a legacy entry with a count of zero", legacy("0", good16, key32)},
		{"a legacy entry with a negative count", legacy("-1", good16, key32)},
		{"a legacy entry with a signed count", legacy("+5", good16, key32)},
		{"a legacy entry with a count past the bound", legacy(strconv.Itoa(MaxPBKDF2Iterations+1), good16, key32)},
		{"a legacy entry with a count past 64 bits", legacy("99999999999999999999", good16, key32)},
		{"a legacy entry with an empty salt", legacy("1000", "", key32)},
		{"a legacy entry with a salt that is not base64", legacy("1000", "not base64", key32)},
		{"a legacy entry with a salt of 65 bytes", legacy("1000", base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 65)), key32)},
		{"a legacy entry with a key that is not base64", legacy("1000", good16, "not base64")},
		{"a legacy entry with an empty key", legacy("1000", good16, "")},
		{"a legacy entry with a key of 31 bytes", legacy("1000", good16, base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 31)))},
		{"a legacy entry with a key of 33 bytes", legacy("1000", good16, base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 33)))},
		{"a legacy entry with a space in it", legacy("1000", good16, key32) + " "},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse(c.encoded); !errors.Is(err, ErrMalformedHash) {
				t.Fatalf("reading %q gave %v, want one wrapping %v", c.encoded, err, ErrMalformedHash)
			}
		})
	}
	// The control for the table: the shapes it spoils, left whole, are read, so
	// that every refusal above is the spoiling and not the frame around it.
	for _, whole := range []string{
		argon("m=64,t=1,p=1"),
		argon("m=" + strconv.Itoa(MaxMemoryKiB) + ",t=" + strconv.Itoa(MaxIterations) + ",p=" + strconv.Itoa(MaxParallelism)),
		pbkdf(pbkdfGood),
		pbkdf("i=" + strconv.Itoa(MaxPBKDF2Iterations) + ",l=32"),
		legacy("1000", good16, key32),
		legacy("1", base64.RawStdEncoding.EncodeToString([]byte("s")), key32),
	} {
		if _, err := Parse(whole); err != nil {
			t.Errorf("the whole entry %q was refused: %v", whole, err)
		}
	}
}

// TestAnEntryIsHeldToItsBoundsFromBothSides holds each bound at the value just
// inside it and the one just outside, which is where a bound that drifted by one
// either refuses a legitimate entry or lets a hostile one through.
func TestAnEntryIsHeldToItsBoundsFromBothSides(t *testing.T) {
	good := base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	salt := func(n int) string { return base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, n)) }
	hash := salt
	for _, c := range []struct {
		name    string
		encoded string
		ok      bool
	}{
		{"memory at the bound", "$argon2id$v=19$m=262144,t=1,p=1$" + salt(16) + "$" + good, true},
		{"memory over the bound", "$argon2id$v=19$m=262145,t=1,p=1$" + salt(16) + "$" + good, false},
		{"iterations at the bound", "$argon2id$v=19$m=64,t=10,p=1$" + salt(16) + "$" + good, true},
		{"iterations over the bound", "$argon2id$v=19$m=64,t=11,p=1$" + salt(16) + "$" + good, false},
		{"lanes at the bound", "$argon2id$v=19$m=128,t=1,p=16$" + salt(16) + "$" + good, true},
		{"lanes over the bound", "$argon2id$v=19$m=136,t=1,p=17$" + salt(16) + "$" + good, false},
		{"memory at eight KiB a lane", "$argon2id$v=19$m=16,t=1,p=2$" + salt(16) + "$" + good, true},
		{"memory under eight KiB a lane", "$argon2id$v=19$m=15,t=1,p=2$" + salt(16) + "$" + good, false},
		{"an Argon2id salt of 8 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + salt(8) + "$" + good, true},
		{"an Argon2id salt of 7 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + salt(7) + "$" + good, false},
		{"an Argon2id salt of 64 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + salt(64) + "$" + good, true},
		{"an Argon2id salt of 65 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + salt(65) + "$" + good, false},
		{"an Argon2id hash of 16 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + salt(16) + "$" + hash(16), true},
		{"an Argon2id hash of 15 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + salt(16) + "$" + hash(15), false},
		{"an Argon2id hash of 64 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + salt(16) + "$" + hash(64), true},
		{"an Argon2id hash of 65 bytes", "$argon2id$v=19$m=64,t=1,p=1$" + salt(16) + "$" + hash(65), false},
		{"a PBKDF2 count at the bound", "$pbkdf2-sha256$i=10000000,l=32$" + salt(16) + "$" + good, true},
		{"a PBKDF2 count over the bound", "$pbkdf2-sha256$i=10000001,l=32$" + salt(16) + "$" + good, false},
		{"a PBKDF2 length of 16", "$pbkdf2-sha256$i=1,l=16$" + salt(16) + "$" + hash(16), true},
		{"a PBKDF2 length of 15", "$pbkdf2-sha256$i=1,l=15$" + salt(16) + "$" + hash(15), false},
		{"a PBKDF2 length of 64", "$pbkdf2-sha256$i=1,l=64$" + salt(16) + "$" + hash(64), true},
		{"a PBKDF2 length of 65", "$pbkdf2-sha256$i=1,l=65$" + salt(16) + "$" + hash(65), false},
		{"a PBKDF2 salt of 8 bytes", "$pbkdf2-sha256$i=1,l=32$" + salt(8) + "$" + good, true},
		{"a PBKDF2 salt of 7 bytes", "$pbkdf2-sha256$i=1,l=32$" + salt(7) + "$" + good, false},
		{"a PBKDF2 salt of 64 bytes", "$pbkdf2-sha256$i=1,l=32$" + salt(64) + "$" + good, true},
		{"a PBKDF2 salt of 65 bytes", "$pbkdf2-sha256$i=1,l=32$" + salt(65) + "$" + good, false},
		{"a legacy salt of 1 byte", "pbkdf2-sha256$1$" + salt(1) + "$" + good, true},
		{"a legacy salt of 64 bytes", "pbkdf2-sha256$1$" + salt(64) + "$" + good, true},
		{"a legacy salt of 65 bytes", "pbkdf2-sha256$1$" + salt(65) + "$" + good, false},
		{"a legacy count at the bound", "pbkdf2-sha256$10000000$" + salt(16) + "$" + good, true},
		{"a legacy count over the bound", "pbkdf2-sha256$10000001$" + salt(16) + "$" + good, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.encoded)
			if c.ok && err != nil {
				t.Errorf("the entry was refused: %v", err)
			}
			if !c.ok && !errors.Is(err, ErrMalformedHash) {
				t.Errorf("the entry gave %v, want one wrapping %v", err, ErrMalformedHash)
			}
		})
	}
}

// TestReadingAnEntryNeverEchoesIt holds the rule an error about a credential
// has to keep: the thing it complains about is the secret. Each case fails on
// one field, so that the text of the failure could quote the others.
func TestReadingAnEntryNeverEchoesIt(t *testing.T) {
	secrets := []string{"c2VjcmV0c2FsdHNlY3JldHM", "c2VjcmV0a2V5bWF0ZXJpYWxzZWNyZXRrZXltYXRlcmlhbA", "not base64!"}
	for _, broken := range []string{
		"$argon2id$v=19$m=64,t=1,p=1$" + secrets[0] + "$" + secrets[2],
		"$argon2id$v=19$m=64,t=1,p=1,x=1$" + secrets[0] + "$" + secrets[1],
		"$pbkdf2-sha256$i=1000,l=32$" + secrets[0] + "$" + secrets[1],
		"$pbkdf2-sha256$i=1000,l=32$" + secrets[0] + "$" + secrets[2],
		"pbkdf2-sha256$600000$" + secrets[0] + "$" + secrets[1],
		"pbkdf2-sha256$600000$" + secrets[0] + "$" + secrets[2],
	} {
		_, err := Parse(broken)
		if err == nil {
			t.Fatalf("the entry %q was read without complaint", broken)
		}
		for _, secret := range secrets {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the failure reads %q and carries %q out of the entry", err, secret)
			}
		}
	}
}

// TestAHostileEntryIsRefusedBeforeAnythingIsDerived holds the reason the bounds
// come before the derivation: an entry's own numbers must not decide what one
// check costs. The entry asks for four gigabytes of memory and a count of
// billions; Parse refuses it, and the zero Entry a refused read leaves verifies
// nothing, so there is no derivation to reach.
func TestAHostileEntryIsRefusedBeforeAnythingIsDerived(t *testing.T) {
	good := base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	for _, hostile := range []string{
		"$argon2id$v=19$m=4194304,t=10,p=16$" + good + "$" + good,
		"$pbkdf2-sha256$i=4000000000,l=32$" + good + "$" + good,
		"pbkdf2-sha256$2000000000$" + good + "$" + good,
	} {
		e, err := Parse(hostile)
		if !errors.Is(err, ErrMalformedHash) {
			t.Errorf("the hostile entry gave %v, want one wrapping %v", err, ErrMalformedHash)
		}
		if ok, err := e.Verify("anything"); ok || err == nil {
			t.Errorf("the entry a refused read leaves verified a password (%v, %v)", ok, err)
		}
	}
}

// TestTheWriterRefusesWhatNoReaderWouldAccept holds Params.Hash to what Parse
// reads. The standard library takes a count below one as one and does not
// complain, so without the refusal the writer would hand back an entry that every
// startup reading the table then turns away.
func TestTheWriterRefusesWhatNoReaderWouldAccept(t *testing.T) {
	for _, p := range []Params{
		{},
		{Algorithm: "scrypt"},
		{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 0},
		{Algorithm: PBKDF2SHA256, PBKDF2Iterations: -1},
		{Algorithm: PBKDF2SHA256, PBKDF2Iterations: math.MinInt},
		{Algorithm: PBKDF2SHA256, PBKDF2Iterations: MaxPBKDF2Iterations + 1},
		{Algorithm: Argon2id, MemoryKiB: 64, Iterations: 0, Parallelism: 1},
		{Algorithm: Argon2id, MemoryKiB: 64, Iterations: 1, Parallelism: 0},
		{Algorithm: Argon2id, MemoryKiB: 8, Iterations: 1, Parallelism: 2},
		{Algorithm: Argon2id, MemoryKiB: MaxMemoryKiB + 1, Iterations: 1, Parallelism: 1},
		{Algorithm: Argon2id, MemoryKiB: 64, Iterations: MaxIterations + 1, Parallelism: 1},
		{Algorithm: Argon2id, MemoryKiB: 4096, Iterations: 1, Parallelism: MaxParallelism + 1},
	} {
		encoded, err := p.Hash("correct horse")
		if !errors.Is(err, ErrMalformedHash) {
			t.Errorf("writing with %+v gave %v, want a failure wrapping %v", p, err, ErrMalformedHash)
		}
		if encoded != "" {
			t.Errorf("writing with %+v handed back an entry alongside its failure", p)
		}
	}
}

// TestTheFloorIsTheOneOWASPStates holds BelowFloor at each combination of the
// OWASP minimums from both sides, and holds Weaker at the three ways one set of
// parameters can be weaker than another.
func TestTheFloorIsTheOneOWASPStates(t *testing.T) {
	a := func(m, ti uint32, p uint8) Params {
		return Params{Algorithm: Argon2id, MemoryKiB: m, Iterations: ti, Parallelism: p}
	}
	for _, c := range []struct {
		name  string
		p     Params
		below bool
	}{
		{"47104 KiB once", a(47104, 1, 1), false},
		{"47103 KiB once", a(47103, 1, 1), true},
		{"19456 KiB twice", a(19456, 2, 1), false},
		{"19455 KiB twice", a(19455, 2, 1), true},
		{"12288 KiB three times", a(12288, 3, 1), false},
		{"12287 KiB three times", a(12287, 3, 1), true},
		{"9216 KiB four times", a(9216, 4, 1), false},
		{"9215 KiB four times", a(9215, 4, 1), true},
		{"7168 KiB five times", a(7168, 5, 1), false},
		{"7167 KiB five times", a(7167, 5, 1), true},
		{"a larger memory with fewer iterations than its row", a(19456, 1, 1), true},
		{"more of everything", a(65536, 10, 4), false},
		{"the default", DefaultParams(), false},
		{"600000 PBKDF2 iterations", Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 600_000}, false},
		{"599999 PBKDF2 iterations", Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 599_999}, true},
		{"no algorithm", Params{}, true},
	} {
		if got := c.p.BelowFloor(); got != c.below {
			t.Errorf("%s: BelowFloor is %v, want %v", c.name, got, c.below)
		}
	}

	current := DefaultParams()
	for _, c := range []struct {
		name   string
		p      Params
		weaker bool
	}{
		{"the same", current, false},
		{"less memory", a(19455, 2, 1), true},
		{"fewer iterations", a(19456, 1, 1), true},
		{"fewer lanes than a current with two", a(19456, 2, 0), true},
		{"more of each", a(32768, 3, 2), false},
		{"another algorithm", Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 1_000_000}, true},
	} {
		if got := c.p.Weaker(current); got != c.weaker {
			t.Errorf("%s: Weaker is %v, want %v", c.name, got, c.weaker)
		}
	}
	pbk := Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 600_000}
	if !(Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 599_999}).Weaker(pbk) {
		t.Error("a PBKDF2 entry with fewer iterations is not weaker")
	}
	if (Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 600_000}).Weaker(pbk) {
		t.Error("a PBKDF2 entry with the same iterations is weaker")
	}
}

// TestADigestStandsForTheEntryAsItWasWritten holds what the cache key leans on:
// the same entry has the same digest, and an entry written again has another.
func TestADigestStandsForTheEntryAsItWasWritten(t *testing.T) {
	encoded, err := cheapArgon2id.Hash("secret")
	if err != nil {
		t.Fatal(err)
	}
	first, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	other, err := cheapArgon2id.Hash("secret")
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := Parse(other)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest() != again.Digest() {
		t.Error("one entry read twice has two digests")
	}
	if first.Digest() == rewritten.Digest() {
		t.Error("an entry written again has the digest of the first: a changed password would keep its cached checks")
	}
}

// fipsChild marks the copy of the test binary that
// TestASaltThisProcessCannotDeriveWithIsRefusedWhenItIsRead starts under FIPS
// 140-only mode.
const fipsChild = "SPEED_AUTHN_PASSWORD_FIPS_CHILD"

// TestASaltThisProcessCannotDeriveWithIsRefusedWhenItIsRead holds the same rule
// for what the derivation itself refuses. In FIPS 140-only mode the standard
// library will not derive from a salt shorter than 128 bits, so an entry
// carrying one reads as well formed and then refuses the right password for
// ever. Whether a derivation can run is the derivation's own answer, and it is
// asked when the entry is read. The mode is fixed when a process starts, so the
// case runs in a copy of this test binary started under it.
func TestASaltThisProcessCannotDeriveWithIsRefusedWhenItIsRead(t *testing.T) {
	if os.Getenv(fipsChild) == "" {
		child := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
		child.Env = append(os.Environ(), fipsChild+"=1", "GODEBUG=fips140=only")
		if out, err := child.CombinedOutput(); err != nil {
			t.Fatalf("the case failed in FIPS 140-only mode: %v\n%s", err, out)
		}
		return
	}

	// Eight bytes of salt: enough for the format, short of what the derivation
	// accepts in this mode.
	for _, short := range []string{
		"$pbkdf2-sha256$i=2,l=32$" + base64.RawStdEncoding.EncodeToString(salt8) + "$" +
			base64.RawStdEncoding.EncodeToString(make([]byte, 32)),
		"pbkdf2-sha256$2$" + base64.RawStdEncoding.EncodeToString(salt8) + "$" +
			base64.RawStdEncoding.EncodeToString(make([]byte, 32)),
	} {
		_, err := Parse(short)
		if !errors.Is(err, ErrMalformedHash) {
			t.Errorf("an entry with an 8-byte salt read back with %v in FIPS 140-only mode, want a "+
				"failure wrapping %v: the derivation refuses that salt here, so it could never verify",
				err, ErrMalformedHash)
			continue
		}
		// The derivation's own refusal rides along as a second cause, which is
		// what tells the operator why the entry is unusable here.
		var joined interface{ Unwrap() []error }
		if !errors.As(err, &joined) || len(joined.Unwrap()) != 2 {
			t.Errorf("the failure for an 8-byte salt carries %v, want both %v and the derivation's own error", err, ErrMalformedHash)
		}
	}

	// An entry the writer wrote is one the derivation accepts in this mode too.
	written, err := Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 1000}.Hash("a long enough password")
	if err != nil {
		t.Fatalf("writing an entry in FIPS 140-only mode: %v", err)
	}
	e, err := Parse(written)
	if err != nil {
		t.Fatalf("reading back an entry written in FIPS 140-only mode: %v", err)
	}
	if ok, err := e.Verify("a long enough password"); err != nil || !ok {
		t.Errorf("an entry written in FIPS 140-only mode does not verify its own password: %v, %v", ok, err)
	}
}
