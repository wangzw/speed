package static

import (
	"strings"
	"testing"
)

// TestNoResolverErrorRepeatsTheSubjectTable holds the one thing a failure about
// this module's credentials must not do: repeat the table. The subjects item is
// the module's secret — the names it answers for and the password entries it
// holds — so a refusal names the key to correct and never the stored material.
//
// Two parts of the table are driven here. A name that does not fit the format is
// not echoed, because the table is a secret as a whole and a name outside the
// format can be anything at all. An entry the module cannot read is not echoed
// either, because it is the stored secret the table exists to hold. The entry
// case repeats TestAnUnusableEntryNeverReachesAnErrorMessage on purpose: this
// test states the whole-table rule in one place, and naming only the format rule
// would leave the two halves of the table looking as though only one is guarded.
func TestNoResolverErrorRepeatsTheSubjectTable(t *testing.T) {
	entry := cheapEntry(t, "x")

	t.Run("a name that does not fit the format", func(t *testing.T) {
		const name = "not a valid subject name #1"
		_, err := moduleConfig{Subjects: map[string]string{name: entry}, CacheSize: 16, Concurrency: 2}.resolve()
		if err == nil {
			t.Fatal("a subject outside the format resolved without complaint")
		}
		if strings.Contains(err.Error(), name) {
			t.Errorf("the failure repeats the subject name: %v", err)
		}
	})

	t.Run("an entry nothing can read", func(t *testing.T) {
		const entryValue = "$argon2id$v=19$m=64,t=1,p=1$SENTINELSALT0SENTINELSALT$SENTINELHASHVALUE0"
		_, err := moduleConfig{Subjects: map[string]string{"alice": entryValue}, CacheSize: 16, Concurrency: 2}.resolve()
		if err == nil {
			t.Fatal("an unreadable entry resolved without complaint")
		}
		if strings.Contains(err.Error(), entryValue) || strings.Contains(err.Error(), "SENTINEL") {
			t.Errorf("the failure repeats the stored entry: %v", err)
		}
	})
}
