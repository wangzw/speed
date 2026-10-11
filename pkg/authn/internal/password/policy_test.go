package password

import "testing"

func TestNormalizeIsNFKC(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"fullwidth digits", "１２３４", "1234"},
		{"ligature", "ﬁle", "file"},
		{"composed form", "é", "é"},
		{"ascii is untouched", "correct horse", "correct horse"},
		{"the empty string", "", ""},
	} {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("%s: Normalize(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestEffectiveFallsBackOnACombinationBelowTheFloor(t *testing.T) {
	for _, c := range []struct {
		name     string
		in       Params
		want     Params
		fellBack bool
	}{
		{"the default combination", DefaultParams(), DefaultParams(), false},
		{
			"another combination on the floor",
			Params{Algorithm: Argon2id, MemoryKiB: 12288, Iterations: 3, Parallelism: 1},
			Params{Algorithm: Argon2id, MemoryKiB: 12288, Iterations: 3, Parallelism: 1},
			false,
		},
		{
			"memory legal and iterations legal but the pair is below every floor row",
			Params{Algorithm: Argon2id, MemoryKiB: 12288, Iterations: 2, Parallelism: 1},
			DefaultParams(), true,
		},
		{
			"one iteration at the memory of the two-iteration row",
			Params{Algorithm: Argon2id, MemoryKiB: 19456, Iterations: 1, Parallelism: 1},
			DefaultParams(), true,
		},
		{"no lane", Params{Algorithm: Argon2id, MemoryKiB: 19456, Iterations: 2}, DefaultParams(), true},
		{
			"pbkdf2 at the floor",
			Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 600_000},
			Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 600_000},
			false,
		},
		{
			"pbkdf2 below the floor",
			Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 599_999},
			Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 600_000},
			true,
		},
		{
			"pbkdf2 above what an entry may state",
			Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: MaxPBKDF2Iterations + 1},
			Params{Algorithm: PBKDF2SHA256, PBKDF2Iterations: 600_000},
			true,
		},
	} {
		got, fellBack := c.in.Effective()
		if got != c.want || fellBack != c.fellBack {
			t.Errorf("%s: Effective() = %+v, %v; want %+v, %v", c.name, got, fellBack, c.want, c.fellBack)
		}
	}
}

func TestHoldTakesTheCapacityADerivationWould(t *testing.T) {
	g := NewGovernor(1, 1024)
	release, err := g.Hold(t.Context(), 1024, 0)
	if err != nil {
		t.Fatalf("holding the whole budget: %v", err)
	}
	if _, err = g.Hold(t.Context(), 8, 0); err == nil {
		t.Fatal("a second hold found capacity while the first one held every slot")
	}
	release()
	again, err := g.Hold(t.Context(), 8, 0)
	if err != nil {
		t.Fatalf("holding after the release: %v", err)
	}
	again()
}
