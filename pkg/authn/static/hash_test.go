package static

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn/internal/password"
)

// TestHashWritesTheArgon2idEntryTheDesignStates holds the default Argon2id entry at the least OWASP combination and its verification.
func TestHashWritesTheArgon2idEntryTheDesignStates(t *testing.T) {
	encoded, err := Hash("correct horse")
	if err != nil {
		t.Fatalf("writing an entry: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("Hash wrote %q, want an Argon2id entry at m=19456,t=2,p=1", encoded)
	}
	if parseErr := ParseHash(encoded); parseErr != nil {
		t.Errorf("the entry Hash wrote is not readable: %v", parseErr)
	}
	e, err := password.Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if ok, verifyErr := e.Verify("correct horse"); verifyErr != nil || !ok {
		t.Errorf("the entry does not verify its own password: %v, %v", ok, verifyErr)
	}
	if e.Params().BelowFloor() {
		t.Errorf("the default entry is below the floor OWASP states: %+v", e.Params())
	}

	again, err := Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if again == encoded {
		t.Error("two entries of one password are identical, so the salt is not drawn fresh")
	}
}

// TestHashPBKDF2WritesTheCountItWasAskedFor holds the chosen cost and that only the right password verifies.
func TestHashPBKDF2WritesTheCountItWasAskedFor(t *testing.T) {
	for _, iterations := range []int{1, 3, 1_000} {
		encoded, err := HashPBKDF2("correct horse", iterations)
		if err != nil {
			t.Fatalf("writing an entry at %d: %v", iterations, err)
		}
		if want := "$pbkdf2-sha256$i=" + strconv.Itoa(iterations) + ",l=32$"; !strings.HasPrefix(encoded, want) {
			t.Errorf("HashPBKDF2 wrote %q at %d, want it to begin %q", encoded, iterations, want)
		}
		e, err := password.Parse(encoded)
		if err != nil {
			t.Fatalf("reading back an entry at %d: %v", iterations, err)
		}
		if e.Params().PBKDF2Iterations != iterations {
			t.Errorf("an entry written at %d reads back at %d", iterations, e.Params().PBKDF2Iterations)
		}
		if ok, _ := e.Verify("correct horse"); !ok {
			t.Errorf("the entry at %d does not verify the password it was made from", iterations)
		}
		if ok, _ := e.Verify("correct hors"); ok {
			t.Errorf("the entry at %d verifies a password it was not made from", iterations)
		}
	}
	first, _ := HashPBKDF2("shared", 2)
	second, _ := HashPBKDF2("shared", 2)
	if first == second {
		t.Error("two entries of one password at one count are identical, so the salt is not drawn fresh")
	}
}

// TestACountNoEntryCanCarryIsRefusedRatherThanWritten holds the writer to
// what the reader accepts.
func TestACountNoEntryCanCarryIsRefusedRatherThanWritten(t *testing.T) {
	for _, iterations := range []int{0, -1, math.MinInt, password.MaxPBKDF2Iterations + 1} {
		encoded, err := HashPBKDF2("correct horse", iterations)
		if !errors.Is(err, ErrMalformedHash) {
			t.Errorf("HashPBKDF2 at %d gave %v, want a failure wrapping %v", iterations, err, ErrMalformedHash)
		}
		if encoded != "" {
			t.Errorf("HashPBKDF2 at %d handed back an entry alongside its failure", iterations)
		}
	}
}

// TestParseHashReadsEveryFormAndRefusesWhatIsNotOne holds the three forms and an ErrMalformedHash that echoes nothing.
func TestParseHashReadsEveryFormAndRefusesWhatIsNotOne(t *testing.T) {
	phc, err := HashPBKDF2("x", 2)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Split(phc, "$")
	argon, err := Hash("x")
	if err != nil {
		t.Fatal(err)
	}
	for name, encoded := range map[string]string{
		"argon2id":      argon,
		"pbkdf2-sha256": phc,
		"the old form":  "pbkdf2-sha256$2$" + fields[3] + "$" + fields[4],
	} {
		if err := ParseHash(encoded); err != nil {
			t.Errorf("%s: an entry in the form was refused: %v", name, err)
		}
	}
	const secret = "c2VjcmV0c2FsdHNlY3JldHM"
	for name, encoded := range map[string]string{
		"nothing":            "",
		"a plain password":   "correct horse",
		"an unknown scheme":  "$scrypt$ln=15$" + secret + "$" + secret,
		"a broken old entry": "pbkdf2-sha256$600000$" + secret + "$not base64!",
	} {
		err := ParseHash(encoded)
		if !errors.Is(err, ErrMalformedHash) {
			t.Errorf("%s: gave %v, want one wrapping %v", name, err, ErrMalformedHash)
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("%s: the failure carries part of the entry: %v", name, err)
		}
	}
}

// TestTheSentinelIsTheOneTheReaderReturns holds that ErrMalformedHash is one shared error.
func TestTheSentinelIsTheOneTheReaderReturns(t *testing.T) {
	if !errors.Is(ErrMalformedHash, password.ErrMalformedHash) ||
		!errors.Is(fmt.Errorf("wrapped: %w", password.ErrMalformedHash), ErrMalformedHash) {
		t.Error("static.ErrMalformedHash and password.ErrMalformedHash are two errors")
	}
}
