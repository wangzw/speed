package password

import (
	"errors"
	"strings"
	"testing"

	"github.com/alexedwards/argon2id"
)

// The vectors below were derived outside this package: the PBKDF2 pair with
// Python's hashlib.pbkdf2_hmac and the Argon2id one with x/crypto's argon2.IDKey
// called directly, under the password and the 16-byte salt "0123456789abcdef".
// They are literals so that a change to the alphabet, the parameter order, the
// spelling of l= or the padding of this package's writer and reader cannot move
// the expectation with it.
const (
	vectorPassword = "correct horse battery staple"

	vectorPBKDF2 = "$pbkdf2-sha256$i=1000,l=32$MDEyMzQ1Njc4OWFiY2RlZg$yqSq2SygY1sB4EcH9f2FG0JTMES+wqLsOT5YmiRBplI"
	vectorLegacy = "pbkdf2-sha256$1000$MDEyMzQ1Njc4OWFiY2RlZg$yqSq2SygY1sB4EcH9f2FG0JTMES+wqLsOT5YmiRBplI"
	vectorArgon2 = "$argon2id$v=19$m=64,t=1,p=1$MDEyMzQ1Njc4OWFiY2RlZg$aq42HBL+IPAitHspChw/8mx5JRX2jFDkTmlsBskwH/0"
)

// TestEntriesDerivedOutsideThisPackageAreReadAndVerified holds the three forms
// against the fixed vectors: each is read at the parameters it states, verifies
// the password it was derived from and not another, and the two PBKDF2 forms are
// one hash in two shells.
func TestEntriesDerivedOutsideThisPackageAreReadAndVerified(t *testing.T) {
	for name, tc := range map[string]struct {
		encoded string
		want    Params
		legacy  bool
	}{
		"pbkdf2 phc": {vectorPBKDF2, Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 1000}, false},
		"pbkdf2 old": {vectorLegacy, Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 1000}, true},
		"argon2id":   {vectorArgon2, Params{Algorithm: Argon2id, MemoryKiB: 64, Iterations: 1, Parallelism: 1}, false},
	} {
		t.Run(name, func(t *testing.T) {
			e, err := Parse(tc.encoded)
			if err != nil {
				t.Fatalf("the vector is refused: %v", err)
			}
			if e.Params() != tc.want || e.Legacy() != tc.legacy {
				t.Errorf("the vector reads as %+v (legacy %v), want %+v (legacy %v)", e.Params(), e.Legacy(), tc.want, tc.legacy)
			}
			if ok, err := e.Verify(vectorPassword); err != nil || !ok {
				t.Errorf("the password the vector was derived from gives %v, %v", ok, err)
			}
			for _, wrong := range []string{"", vectorPassword + " ", strings.ToUpper(vectorPassword)} {
				if ok, _ := e.Verify(wrong); ok {
					t.Errorf("the vector verifies %q", wrong)
				}
			}
		})
	}
}

// TestThePBKDF2WriterWritesTheVectorByteForByte holds the PBKDF2 dialect from
// the writer's side: given the vector's salt the writer's output is the vector.
func TestThePBKDF2WriterWritesTheVectorByteForByte(t *testing.T) {
	got, err := encodePBKDF2(1000, []byte("0123456789abcdef"), vectorPassword)
	if err != nil {
		t.Fatal(err)
	}
	if got != vectorPBKDF2 {
		t.Errorf("the writer wrote %q, want %q", got, vectorPBKDF2)
	}
}

// TestAnArgon2idEntryAgreesWithTheLibraryBothWays holds the Argon2id form
// against the library that defines it, from both sides: the library verifies
// what this package writes, and this package reads and verifies what the
// library writes.
func TestAnArgon2idEntryAgreesWithTheLibraryBothWays(t *testing.T) {
	ours, err := cheapArgon2id.Hash(vectorPassword)
	if err != nil {
		t.Fatal(err)
	}
	if ok, cerr := argon2id.ComparePasswordAndHash(vectorPassword, ours); cerr != nil || !ok {
		t.Errorf("the library does not verify what this package wrote: %v, %v", ok, cerr)
	}
	if ok, _ := argon2id.ComparePasswordAndHash("another", ours); ok {
		t.Error("the library verifies another password against what this package wrote")
	}

	theirs, err := argon2id.CreateHash(vectorPassword, &argon2id.Params{
		Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := Parse(theirs)
	if err != nil {
		t.Fatalf("this package refuses what the library wrote: %v", err)
	}
	if ok, err := e.Verify(vectorPassword); err != nil || !ok {
		t.Errorf("this package does not verify what the library wrote: %v, %v", ok, err)
	}
	if ok, _ := e.Verify("another"); ok {
		t.Error("this package verifies another password against what the library wrote")
	}
	if ok, err := argon2id.ComparePasswordAndHash(vectorPassword, vectorArgon2); err != nil || !ok {
		t.Errorf("the library does not verify the fixed vector: %v, %v", ok, err)
	}
}

// TestALegacyEntryBecomesPHCWithoutLoss holds the conversion: the salt and the
// key are the same bytes in the new shell, the converted entry is the PBKDF2
// vector, and one password verifies against the entry before and after.
func TestALegacyEntryBecomesPHCWithoutLoss(t *testing.T) {
	old, err := Parse(vectorLegacy)
	if err != nil {
		t.Fatal(err)
	}
	converted, err := old.PHC()
	if err != nil {
		t.Fatal(err)
	}
	if converted != vectorPBKDF2 {
		t.Errorf("the legacy entry converts to %q, want %q", converted, vectorPBKDF2)
	}
	now, err := Parse(converted)
	if err != nil {
		t.Fatalf("the converted entry is refused: %v", err)
	}
	if now.Legacy() {
		t.Error("the converted entry still reads as legacy")
	}
	for name, e := range map[string]Entry{"before": old, "after": now} {
		if ok, verr := e.Verify(vectorPassword); verr != nil || !ok {
			t.Errorf("%s the conversion the password gives %v, %v", name, ok, verr)
		}
		if ok, _ := e.Verify("another"); ok {
			t.Errorf("%s the conversion another password verifies", name)
		}
	}

	// An entry that is already PHC is returned as it is, and what cannot be
	// written in PHC is refused.
	again, err := now.PHC()
	if err != nil || again != converted {
		t.Errorf("a PHC entry converts to %q, %v, want itself", again, err)
	}
	shortSalt := "pbkdf2-sha256$1000$QQ$yqSq2SygY1sB4EcH9f2FG0JTMES+wqLsOT5YmiRBplI"
	short, err := Parse(shortSalt)
	if err != nil {
		t.Fatalf("a legacy entry with a one-byte salt is refused when read: %v", err)
	}
	if _, err := short.PHC(); !errors.Is(err, ErrMalformedHash) {
		t.Errorf("a legacy entry whose salt PHC cannot carry converts with %v, want one wrapping %v", err, ErrMalformedHash)
	}
	if _, err := (Entry{}).PHC(); !errors.Is(err, ErrMalformedHash) {
		t.Errorf("the zero entry converts with %v, want one wrapping %v", err, ErrMalformedHash)
	}
}
