package account

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

// builtinReservedUsernames is the set of names the system keeps for itself: the
// API paths, the console and its routes, the static asset prefix, and the health
// and readiness probes. It is a literal set, so a path or a name added to the
// system is added here too. A deployment can append names through
// authn.account.reserved-usernames; it cannot take one away.
var builtinReservedUsernames = []string{
	"api", "console", "account", "admin", "system", "login", "logout",
	"signin", "signup", "static", "assets", "health", "ready", "metrics",
	"oauth", "callback", "well-known", "www", "root",
}

// builtinReserved is builtinReservedUsernames as the set the check reads.
var builtinReserved = func() map[string]bool {
	set := make(map[string]bool, len(builtinReservedUsernames))
	for _, name := range builtinReservedUsernames {
		set[name] = true
	}
	return set
}()

// reservedUsernames is the set a username write refuses: the built-in names and
// the names the deployment appended. The built-in set is always in effect, so a
// store that is absent, a read that fails and a value outside the domain all add
// nothing and never take a built-in name away.
func (p *product) reservedUsernames() map[string]bool {
	reserved := make(map[string]bool, len(builtinReserved))
	for name := range builtinReserved {
		reserved[name] = true
	}
	names, err := parseUsernameList(readSetting(p, reservedPath, reservedTextInDomain, defaults.ReservedUsernames))
	if err != nil {
		return reserved
	}
	for _, name := range names {
		normalized, normErr := NormalizeIdentifier(authn.IdentifierUsername, name)
		if normErr != nil {
			continue
		}
		reserved[normalized] = true
	}
	return reserved
}

// checkReservedUsername refuses a normalized username that is in the effective
// set. The error names the class and never the value.
func (p *product) checkReservedUsername(value string) error {
	if p.reservedUsernames()[value] {
		return fmt.Errorf("%w: a username is a name the system keeps for itself", authn.ErrUsernameReserved)
	}
	return nil
}

// parseUsernameList reads the text of the reserved-usernames setting as its
// names. The empty text is the empty list, which is how a layer that holds no
// row and the declared default read.
func parseUsernameList(text string) ([]string, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, nil
	}
	if trimmed[0] != '[' {
		return nil, fmt.Errorf("is not a JSON array")
	}
	var names []string
	if err := json.Unmarshal([]byte(trimmed), &names); err != nil {
		return nil, err
	}
	return names, nil
}

// reservedTextInDomain reports whether a value is a JSON array of usernames. An
// element is normalized when the set is read; one that cannot be a username
// makes the write be refused rather than stored and skipped.
func reservedTextInDomain(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	names, err := parseUsernameList(trimmed)
	if err != nil {
		return false
	}
	for _, name := range names {
		if _, normErr := NormalizeIdentifier(authn.IdentifierUsername, name); normErr != nil {
			return false
		}
	}
	return true
}

// reservedChangeList is one side of a change to the reserved-usernames setting:
// the explicit value, or the declared default when the layer held no row.
func reservedChangeList(text string, declared bool) []string {
	if declared {
		text = defaults.ReservedUsernames
	}
	names, _ := parseUsernameList(text)
	return names
}

// removesReservedMember reports whether before held a name after does not, which
// widens the set of usernames a write accepts.
func removesReservedMember(before, after []string) bool {
	held := reservedSet(after)
	for name := range reservedSet(before) {
		if !held[name] {
			return true
		}
	}
	return false
}

// reservedSet reads a list of names as the normalized set it stands for. A name
// that cannot be normalized was stored around the domain and adds nothing.
func reservedSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		if normalized, err := NormalizeIdentifier(authn.IdentifierUsername, name); err == nil {
			set[normalized] = true
		}
	}
	return set
}
