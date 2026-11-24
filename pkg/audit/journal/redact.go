package journal

import "github.com/vislake/speed/pkg/log"

// maskedKeySet is the closed set of detail key names whose values are
// masked.
var maskedKeySet = []string{
	"password",
	"secret",
	"token",
	"credential",
	"authorization",
}

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
