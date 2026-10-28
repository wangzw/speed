package account

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
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
// nothing and never take a built-in name away. An element the setting holds is
// normalized as it is read; one that is not a username after that is skipped and
// reported, and the rest of the array stands.
func (p *product) reservedUsernames() map[string]bool {
	reserved := make(map[string]bool, len(builtinReserved))
	for name := range builtinReserved {
		reserved[name] = true
	}
	names, err := parseUsernameList(readSetting(p, reservedPath, reservedTextInDomain, defaults.ReservedUsernames))
	if err != nil {
		return reserved
	}
	skipped := 0
	for _, name := range names {
		normalized, normErr := NormalizeIdentifier(authn.IdentifierUsername, name)
		if normErr != nil {
			skipped++
			continue
		}
		reserved[normalized] = true
	}
	p.reportSkippedReserved(skipped)
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

// reservedTextInDomain reports whether a value is a JSON array of strings. An
// element that is not a username is not refused here: the set is normalized as
// it is read, and one element that cannot be a username must not take the rest
// of the array with it.
func reservedTextInDomain(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	_, err := parseUsernameList(trimmed)
	return err == nil
}

// reservedSkippedKey is the dedupe key of the Warn a read makes when it skips an
// element of the reserved-usernames setting. It is kept apart from the setting's
// own key so that a failed read and a skipped element do not clear each other's
// line.
const reservedSkippedKey = reservedPath + ":element"

// reportSkippedReserved writes the one Warn a read of the reserved-usernames
// setting makes when it skips elements that are not usernames, and clears it
// when a read skips none. The line names neither the elements nor the value.
func (p *product) reportSkippedReserved(skipped int) {
	problem := ""
	detail := ""
	if skipped > 0 {
		problem = "an element of the reserved-usernames setting is not a username, so it is ignored"
		detail = strconv.Itoa(skipped)
	}
	p.reportOnce(slog.LevelWarn, reservedSkippedKey, reservedPath, problem, detail)
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
