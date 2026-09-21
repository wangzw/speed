package journal

import "github.com/vislake/speed/pkg/log"

// maskedKeySet is the closed set of detail key names whose values are masked.
//
// It is a closed set of flat spellings, and both halves of that are
// load-bearing. Closed, because the module has no way to learn which keys a
// call site considers sensitive — that would be a registration surface, and
// there is none here. Flat, because the log module compares a registered name
// segment by segment against a key path while this module compares it against a
// whole key: restricting the set to spellings with no dot in them makes the two
// readings agree on every member.
//
// The set is registered into the log module's rule set as well, so that a log
// line this module writes carrying one of these keys is masked by the same
// decision. What the two places do not share is everything else: a key another
// module registered, and a value recognised by shape, reach the log and not the
// table.
var maskedKeySet = []string{
	"password",
	"secret",
	"token",
	"credential",
	"authorization",
}

// maskedKeyLookup is maskedKeySet as a set, built once.
var maskedKeyLookup = func() map[string]struct{} {
	set := make(map[string]struct{}, len(maskedKeySet))
	for _, key := range maskedKeySet {
		set[key] = struct{}{}
	}
	return set
}()

// maskedKeys returns the set for registration. It hands back a copy because the
// rule set keeps what it is given, and this package must not be able to change
// a registered rule afterwards.
func maskedKeys() []string {
	return append([]string(nil), maskedKeySet...)
}

// maskDetail replaces the value of every key in the set.
//
// The comparison is on the whole key, spelled exactly: a key that merely
// contains one of these names, or spells it in another case, is left alone.
// Widening the match would mask values whose names happen to resemble these,
// and a reader who found a masked value could no longer tell which rule caught
// it.
func maskDetail(d map[string]string) map[string]string {
	if len(d) == 0 {
		return nil
	}
	out := make(map[string]string, len(d))
	for key, value := range d {
		if _, sensitive := maskedKeyLookup[key]; sensitive {
			out[key] = log.MaskedText
			continue
		}
		out[key] = value
	}
	return out
}
