package password

import "testing"

// TestTheDecoyIsMadeForTheParametersAskedFor holds that the decoy carries the
// parameters it is made for and is made again when they change.
func TestTheDecoyIsMadeForTheParametersAskedFor(t *testing.T) {
	var d Decoy
	first := cheapArgon2id
	second := Params{Algorithm: Argon2id, MemoryKiB: 128, Iterations: 2, Parallelism: 1}
	pbkdf := Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 3}

	a, err := d.For(first)
	if err != nil {
		t.Fatalf("making a decoy: %v", err)
	}
	if a.Params() != first {
		t.Errorf("the decoy has %+v, want the %+v asked for", a.Params(), first)
	}
	again, err := d.For(first)
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest() != again.Digest() {
		t.Error("the decoy was made again for the same parameters")
	}

	b, err := d.For(second)
	if err != nil {
		t.Fatal(err)
	}
	if b.Params() != second || b.Digest() == a.Digest() {
		t.Errorf("a change of parameters did not make a new decoy: %+v", b.Params())
	}
	c, err := d.For(pbkdf)
	if err != nil {
		t.Fatal(err)
	}
	if c.Params() != pbkdf {
		t.Errorf("the decoy for PBKDF2 has %+v", c.Params())
	}
}

// TestNoPasswordMatchesTheDecoy holds that the decoy is not an entry anyone can
// authenticate against.
func TestNoPasswordMatchesTheDecoy(t *testing.T) {
	var d Decoy
	e, err := d.For(cheapArgon2id)
	if err != nil {
		t.Fatal(err)
	}
	for _, guess := range []string{"", "password", "decoy", randomText()} {
		if ok, err := e.Verify(guess); err != nil || ok {
			t.Errorf("the decoy verified %q: %v, %v", guess, ok, err)
		}
	}
}

// TestADecoyIsRefusedForParametersNoReaderAccepts holds that a decoy is refused the
// parameters no entry reader accepts.
func TestADecoyIsRefusedForParametersNoReaderAccepts(t *testing.T) {
	var d Decoy
	if _, err := d.For(Params{}); err == nil {
		t.Error("a decoy was made for no parameters at all")
	}
}

// TestTheDecoyIsMadeFromAFreshDrawEachTime holds that each new decoy draws a fresh
// text, a served-again decoy draws none, and no text repeats.
func TestTheDecoyIsMadeFromAFreshDrawEachTime(t *testing.T) {
	var drawn []string
	d := Decoy{text: func() string {
		drawn = append(drawn, randomText())
		return drawn[len(drawn)-1]
	}}
	for _, p := range []Params{cheapArgon2id, cheapArgon2id, cheapPBKDF2, cheapArgon2id} {
		if _, err := d.For(p); err != nil {
			t.Fatal(err)
		}
	}
	if len(drawn) != 3 {
		t.Fatalf("the decoy drew %d times, want 3", len(drawn))
	}
	seen := map[string]bool{}
	for _, text := range drawn {
		if seen[text] {
			t.Errorf("the decoy was made twice from the same text")
		}
		seen[text] = true
	}
}

// TestRandomTextIsFreshAndLongEnoughToBeNoOnesChoice holds that randomText is 64
// hex characters and differs on every call.
func TestRandomTextIsFreshAndLongEnoughToBeNoOnesChoice(t *testing.T) {
	a, b := randomText(), randomText()
	if len(a) != 64 || len(b) != 64 {
		t.Errorf("the texts are %d and %d characters, want 64", len(a), len(b))
	}
	if a == b {
		t.Error("two draws gave the same text")
	}
}
