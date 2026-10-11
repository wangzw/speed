package static

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/config"
)

// TestAConfigurationThisBackendCannotRunOnIsRefusedAtStartup holds the
// validations that turn a request-time surprise into a startup failure.
func TestAConfigurationThisBackendCannotRunOnIsRefusedAtStartup(t *testing.T) {
	entry := cheapEntry(t, "x")
	valid := func(subjects map[string]string) moduleConfig {
		return moduleConfig{Subjects: subjects, CacheSize: 16, Concurrency: 2}
	}
	for _, c := range []struct {
		name string
		cfg  moduleConfig
		want error
	}{
		{"a subject that is empty", valid(map[string]string{"": entry}), authn.ErrInvalidClaim},
		{"a subject that is blank", valid(map[string]string{"  ": entry}), authn.ErrInvalidClaim},
		{"a subject with a dot", valid(map[string]string{"a.b": entry}), authn.ErrInvalidClaim},
		{"a subject with a slash", valid(map[string]string{"a/b": entry}), authn.ErrInvalidClaim},
		{"a subject with a space", valid(map[string]string{"alice smith": entry}), authn.ErrInvalidClaim},
		{"a subject that begins with a dash", valid(map[string]string{"-alice": entry}), authn.ErrInvalidClaim},
		{"a subject that begins with an underscore", valid(map[string]string{"_alice": entry}), authn.ErrInvalidClaim},
		{"a subject that is not ASCII", valid(map[string]string{"alicé": entry}), authn.ErrInvalidClaim},
		{"a subject of 58 bytes", valid(map[string]string{strings.Repeat("a", 58): entry}), authn.ErrInvalidClaim},
		{"an entry nothing can read", valid(map[string]string{"alice": "plaintext"}), ErrMalformedHash},
		{"a cache of no size", moduleConfig{Subjects: map[string]string{"a": entry}, CacheSize: 0, Concurrency: 2}, authn.ErrInvalidConfig},
		{"a cache of a negative size", moduleConfig{Subjects: map[string]string{"a": entry}, CacheSize: -1, Concurrency: 2}, authn.ErrInvalidConfig},
		{"a cache past its bound", moduleConfig{Subjects: map[string]string{"a": entry}, CacheSize: 1_000_001, Concurrency: 2}, authn.ErrInvalidConfig},
		{"no derivations at once", moduleConfig{Subjects: map[string]string{"a": entry}, CacheSize: 16, Concurrency: 0}, authn.ErrInvalidConfig},
		{"a width past its bound", moduleConfig{Subjects: map[string]string{"a": entry}, CacheSize: 16, Concurrency: 257}, authn.ErrInvalidConfig},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.cfg.resolve()
			if !errors.Is(err, c.want) {
				t.Fatalf("resolving gave %v, want one wrapping %v", err, c.want)
			}
		})
	}

	// The control: the longest name and the edges of the two sizes resolve.
	for _, ok := range []moduleConfig{
		valid(map[string]string{strings.Repeat("a", 57): entry}),
		valid(map[string]string{"A-b_9": entry}),
		{Subjects: map[string]string{"a": entry}, CacheSize: 1, Concurrency: 1},
		{Subjects: map[string]string{"a": entry}, CacheSize: 1_000_000, Concurrency: 256},
	} {
		if _, err := ok.resolve(); err != nil {
			t.Errorf("a configuration this backend runs on was refused: %v", err)
		}
	}
}

// TestTheLongestAccountIdentifierIsTheLongestSubjectName holds the arithmetic the
// subject pattern leans on: the static prefix and the longest name fit the 64
// bytes of an account identifier, and one more byte would not.
func TestTheLongestAccountIdentifierIsTheLongestSubjectName(t *testing.T) {
	longest := authn.StaticPrefix + strings.Repeat("a", 57)
	if _, err := authn.ParseAccountID(longest); err != nil {
		t.Errorf("the longest subject makes the account %d bytes long, which is refused: %v", len(longest), err)
	}
	if _, err := authn.ParseAccountID(longest + "a"); err == nil {
		t.Error("an account of 65 bytes is accepted, so the subject pattern would be too tight")
	}
}

// TestAnEntryNothingCanReadIsBothAnInvalidClaimAndAMalformedHash holds the two
// sentinels the startup failure wraps: the one every refusal of a declaration
// carries, and the one that names what was wrong with the entry.
func TestAnEntryNothingCanReadIsBothAnInvalidClaimAndAMalformedHash(t *testing.T) {
	_, err := moduleConfig{Subjects: map[string]string{"alice": "plaintext"}, CacheSize: 16, Concurrency: 2}.resolve()
	if !errors.Is(err, authn.ErrInvalidClaim) {
		t.Errorf("an entry nothing can read failed with %v, want one wrapping %v", err, authn.ErrInvalidClaim)
	}
	if !errors.Is(err, ErrMalformedHash) {
		t.Errorf("an entry nothing can read failed with %v, want one wrapping %v", err, ErrMalformedHash)
	}
}

// TestAFailureNamesTheKeyThatIsWrong holds where the blame lands: a failure
// names the key to correct, and an unusable entry names its subject and no part
// of the entry.
func TestAFailureNamesTheKeyThatIsWrong(t *testing.T) {
	_, err := moduleConfig{Subjects: map[string]string{"alice smith": cheapEntry(t, "x")}, CacheSize: 16, Concurrency: 2}.resolve()
	if err == nil || !strings.Contains(err.Error(), ConfigNamespace+"."+subjectsItem) {
		t.Errorf("a subject outside the format failed with %v, want it to name %s.%s", err, ConfigNamespace, subjectsItem)
	}
	_, err = moduleConfig{Subjects: map[string]string{"a": cheapEntry(t, "x")}, CacheSize: 0, Concurrency: 2}.resolve()
	if err == nil || !strings.Contains(err.Error(), ConfigNamespace+"."+cacheSizeItem) {
		t.Errorf("a cache of no size failed with %v, want it to name %s.%s", err, ConfigNamespace, cacheSizeItem)
	}
	_, err = moduleConfig{Subjects: map[string]string{"a": cheapEntry(t, "x")}, CacheSize: 16, Concurrency: 0}.resolve()
	if err == nil || !strings.Contains(err.Error(), ConfigNamespace+"."+concurrencyItem) {
		t.Errorf("a width of none failed with %v, want it to name %s.%s", err, ConfigNamespace, concurrencyItem)
	}
	_, err = moduleConfig{Subjects: map[string]string{"alice": "plaintext"}, CacheSize: 16, Concurrency: 2}.resolve()
	if err == nil || !strings.Contains(err.Error(), `"alice"`) || !strings.Contains(err.Error(), subjectsItem) {
		t.Errorf("an unusable entry failed with %v, want it to name the subject and the key", err)
	}
}

// TestAnUnusableEntryNeverReachesAnErrorMessage holds the one thing an error
// about a credential table must not do.
func TestAnUnusableEntryNeverReachesAnErrorMessage(t *testing.T) {
	const looksLikeASecret = "$argon2id$v=19$m=64,t=1,p=1$AAAAAAAAAAAAAAAA$NOT-BASE64!!"

	_, err := moduleConfig{
		Subjects: map[string]string{"alice": looksLikeASecret}, CacheSize: 16, Concurrency: 2,
	}.resolve()
	if err == nil {
		t.Fatal("an unreadable entry resolved without complaint")
	}
	if strings.Contains(err.Error(), looksLikeASecret) || strings.Contains(err.Error(), "AAAAAAAA") {
		t.Errorf("the failure reads %q and echoes the stored entry, which is the secret this table "+
			"exists to hold", err)
	}
}

// TestABackendWithNobodyToVouchForRefusesToStart holds what this backend does
// when its table names no subject, which is not a stance it may take. A command
// that names this module has promised it starts, so a table it cannot answer for
// stops the startup, and the failure names the key: under an assembly carrying
// several backends, that line is the only thing telling an operator which of them
// could not start and what it lacked.
func TestABackendWithNobodyToVouchForRefusesToStart(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		{"the key is absent", `{"authn":{"static":{}}}`},
		{"the table is empty", `{"authn":{"static":{"subjects":{}}}}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := prepare(authntest.ConfigReader(t, Module(), c.body))
			if err == nil {
				t.Fatal("a table naming no subject was carried into the run, so a command would " +
					"come up with a backend that authenticates nobody")
			}
			for _, want := range []string{ModuleName, ConfigNamespace + "." + subjectsItem} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the failure reads %q and does not carry %q", err, want)
				}
			}
			if !errors.Is(err, authn.ErrInvalidClaim) {
				t.Errorf("the failure reads %q and does not wrap %v", err, authn.ErrInvalidClaim)
			}
		})
	}
}

// TestTheNameKeyIsGone holds that authn.static.name is no longer a key. Left in
// place and ignored it would make a configuration look as though it still did
// something, which is worse than refusing to start: the name is a constant now.
func TestTheNameKeyIsGone(t *testing.T) {
	body := `{"authn":{"static":{"name":"local","subjects":{"alice":"` + cheapEntry(t, "x") + `"}}}}`
	if _, err := load(t, body); err == nil {
		t.Fatal("a configuration that sets authn.static.name was loaded, so the key is still accepted")
	} else if !strings.Contains(err.Error(), "authn.static.name") {
		t.Errorf("the failure reads %q, want it to name the key that is no longer accepted", err)
	}
}

// TestTheKeysAreReadFromConfigurationAndTheDefaultsHold holds the two sizes
// through the real loader: unset they take the declared defaults, set they are
// followed.
func TestTheKeysAreReadFromConfigurationAndTheDefaultsHold(t *testing.T) {
	entry := cheapEntry(t, "x")

	reader := authntest.ConfigReader(t, Module(), `{"authn":{"static":{"subjects":{"alice":"`+entry+`"}}}}`)
	got, err := readConfig(reader)
	if err != nil {
		t.Fatalf("reading a table with no sizes: %v", err)
	}
	if got.cacheSize != 1024 {
		t.Errorf("the cache holds %d checks by default, want 1024", got.cacheSize)
	}
	if got.concurrency < 1 || got.concurrency > 8 {
		t.Errorf("the width is %d by default, want min(GOMAXPROCS, 8)", got.concurrency)
	}

	reader = authntest.ConfigReader(t, Module(), `{"authn":{"static":{"cache-size":7,"concurrency":3,"subjects":{"alice":"`+entry+`"}}}}`)
	got, err = readConfig(reader)
	if err != nil {
		t.Fatalf("reading a table with sizes: %v", err)
	}
	if got.cacheSize != 7 || got.concurrency != 3 {
		t.Errorf("the sizes read as %d and %d, want 7 and 3", got.cacheSize, got.concurrency)
	}
	if _, ok := got.subjects["alice"]; !ok || len(got.subjects) != 1 {
		t.Errorf("the table reads as %d subject(s), want alice alone", len(got.subjects))
	}
}

// TestTheSubjectTableIsSensitiveAndTakesTheMainSourceAlone holds the declaration
// of the one item that carries secrets: it is required, sensitive, and read from
// the primary source only, so it cannot be set from a command line that a process
// listing shows.
func TestTheSubjectTableIsSensitiveAndTakesTheMainSourceAlone(t *testing.T) {
	item := schema().Items[subjectsItem]
	if !item.Required || !item.Sensitive {
		t.Errorf("the subject table is declared %+v, want required and sensitive", item)
	}
	if item.Origins != config.OriginPrimary {
		t.Errorf("the subject table takes the origins %v, want the primary source alone", item.Origins)
	}
	if item.Description == "" {
		t.Error("the subject table has no description, which a sensitive item requires")
	}
	if _, found := schema().Items["name"]; found {
		t.Error("the schema still declares a name item")
	}
	for _, key := range []string{cacheTTLKey, freeAttemptsKey, failWindowKey, maxBackoffKey} {
		if _, found := schema().Items[key]; found {
			t.Errorf("the configuration schema declares %q, which is a setting and not configuration", key)
		}
		if _, found := settingsSchema().Items[key]; !found {
			t.Errorf("the settings schema does not declare %q", key)
		}
	}
	for _, key := range []string{cacheSizeItem, concurrencyItem} {
		if _, found := settingsSchema().Items[key]; found {
			t.Errorf("the settings schema declares %q, which is fixed at construction and so is configuration", key)
		}
	}
	if got := strconv.Itoa(configDefaults.CacheSize); got != "1024" {
		t.Errorf("the default cache size is %s, want 1024", got)
	}
}
