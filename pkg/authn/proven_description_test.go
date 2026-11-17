package authn_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/admin"
	"github.com/vislake/speed/pkg/authn/profile"
)

// provenWay is one way Verification records, with the words a description uses
// to name it.
type provenWay struct {
	via   string
	named string
}

// TestTheProvenDescriptionsNameTheProvingWays holds the two identifier views'
// `proven` descriptions to Verification.Proves: the description names every
// way once, in runs joined by commas and conjunctions, and each run carries
// only ways that share a Proves() answer. The ways in the proving runs are
// exactly the proving set, and the rest are exactly the non-proving set,
// invite among them. A description that named invite among the proofs, left a
// way out, or mixed the two kinds in one run fails here. It reads runs instead
// of a fixed connector, so rewording the sentence keeps it honest as long as
// the two kinds stay apart.
func TestTheProvenDescriptionsNameTheProvingWays(t *testing.T) {
	ways := []provenWay{
		{authn.VerifiedViaSMSCode, "sms-code"},
		{authn.VerifiedViaEmailCode, "email-code"},
		{authn.VerifiedViaIDPPrefix + "github", "idp:<provider>"},
		{authn.VerifiedViaInvite, "invite"},
		{authn.VerifiedViaAdmin, "admin"},
		{authn.VerifiedViaImportUnverified, "import-unverified"},
		{authn.VerifiedViaSelf, "self"},
	}
	views := []struct {
		name string
		view reflect.Type
	}{
		{"profile", reflect.TypeOf(profile.AuthnProfileIdentifier{})},
		{"admin", reflect.TypeOf(admin.AuthnAdminIdentifier{})},
	}
	for _, view := range views {
		t.Run(view.name, func(t *testing.T) {
			field, ok := view.view.FieldByName("Proven")
			if !ok {
				t.Fatalf("%s view has no Proven field", view.name)
			}
			description := strings.ToLower(field.Tag.Get("description"))
			proving := map[string]bool{}
			for _, way := range ways {
				proving[way.named] = authn.Verification{Via: way.via}.Proves()
			}
			seen := map[string]bool{}
			gotProving := map[string]bool{}
			gotRest := map[string]bool{}
			for _, run := range describedRuns(description, ways) {
				answer := proving[run[0]]
				for _, name := range run {
					if proving[name] != answer {
						t.Fatalf("%s description joins %q with ways of the other kind", view.name, name)
					}
					if seen[name] {
						t.Fatalf("%s description names %q more than once", view.name, name)
					}
					seen[name] = true
					if answer {
						gotProving[name] = true
					} else {
						gotRest[name] = true
					}
				}
			}
			if len(seen) != len(ways) {
				t.Fatalf("%s description names %d of the %d ways: %q", view.name, len(seen), len(ways), description)
			}
			wantProving := map[string]bool{}
			wantRest := map[string]bool{}
			for name, answer := range proving {
				if answer {
					wantProving[name] = true
				} else {
					wantRest[name] = true
				}
			}
			if !reflect.DeepEqual(gotProving, wantProving) {
				t.Errorf("%s description's proving ways are %v, want %v", view.name, gotProving, wantProving)
			}
			if !reflect.DeepEqual(gotRest, wantRest) {
				t.Errorf("%s description's remaining ways are %v, want %v", view.name, gotRest, wantRest)
			}
		})
	}
}

// describedRuns splits the ways a description names into the runs its commas
// and conjunctions join. A run ends where prose, not punctuation and "and" or
// "or", lies between two names, so a rewritten sentence still yields the runs
// it means. Names are matched on word boundaries, which keeps "admin" out of
// "administrator".
func describedRuns(description string, ways []provenWay) [][]string {
	type mention struct {
		at   int
		name string
	}
	var mentions []mention
	for _, way := range ways {
		for _, at := range wordPositions(description, way.named) {
			mentions = append(mentions, mention{at: at, name: way.named})
		}
	}
	sort.Slice(mentions, func(i, j int) bool { return mentions[i].at < mentions[j].at })
	var runs [][]string
	for i, m := range mentions {
		if i == 0 || !joinsRun(description[mentions[i-1].at+len(mentions[i-1].name):m.at]) {
			runs = append(runs, nil)
		}
		runs[len(runs)-1] = append(runs[len(runs)-1], m.name)
	}
	return runs
}

// wordPositions returns every offset at which name occurs in text as a whole
// word, rejecting an occurrence a letter, digit, hyphen or underscore abuts.
func wordPositions(text, name string) []int {
	var positions []int
	for from := 0; ; {
		i := strings.Index(text[from:], name)
		if i < 0 {
			return positions
		}
		i += from
		end := i + len(name)
		if (i == 0 || !isWordByte(text[i-1])) && (end == len(text) || !isWordByte(text[end])) {
			positions = append(positions, i)
		}
		from = i + 1
	}
}

func isWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' ||
		b >= 'A' && b <= 'Z' ||
		b >= '0' && b <= '9' ||
		b == '-' || b == '_'
}

// joinsRun reports whether the text between two names is only punctuation and
// a conjunction, which keeps them in one run.
func joinsRun(between string) bool {
	between = strings.TrimSpace(between)
	between = strings.Trim(between, ".,;:")
	between = strings.TrimSpace(between)
	return between == "" || between == "and" || between == "or"
}
