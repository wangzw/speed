// Package slug holds the one normalization a username and a tenant alias take:
// NFKC folding, lower-casing, and the DNS-label-shaped form the two namespaces
// share. A personal tenant's alias is its owner's username, so a value that
// fails here cannot name either.
package slug

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// shape is the form a normalized username or alias takes: 3 to 63 characters
// of [a-z0-9-], beginning with a letter or a digit. It holds no @, so a
// username and an email address are told apart by that character alone.
var shape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,62}$`)

// Normalize folds a value to the shared form and reports whether it is one. A
// value that is not is returned folded, which no caller stores.
func Normalize(value string) (string, bool) {
	folded := strings.ToLower(norm.NFKC.String(value))
	return folded, shape.MatchString(folded)
}
