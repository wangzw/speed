package account

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

// builtinReservedUsernames is the set of names the system keeps for itself. A path
// or name added to the system is added here too; a deployment can append but never
// remove.
var builtinReservedUsernames = []string{
	"api", "console", "account", "admin", "system", "login", "logout",
	"signin", "signup", "static", "assets", "health", "ready", "metrics",
	"oauth", "callback", "well-known", "www", "root",
}

var builtinReserved = func() map[string]bool {
	set := make(map[string]bool, len(builtinReservedUsernames))
	for _, name := range builtinReservedUsernames {
		set[name] = true
	}
	return set
}()

// reservedUsernames is the built-in set plus the deployment's appended names. An
// absent, unreadable or out-of-domain store adds nothing and never removes a
// built-in name.
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

// personalUsernamePrefix is reserved as a prefix, not as a literal name.
const personalUsernamePrefix = "personal-"

func (p *product) checkReservedUsername(value string) error {
	if strings.HasPrefix(value, personalUsernamePrefix) {
		return fmt.Errorf("%w: a username is a name the system keeps for itself", authn.ErrUsernameReserved)
	}
	if p.reservedUsernames()[value] {
		return fmt.Errorf("%w: a username is a name the system keeps for itself", authn.ErrUsernameReserved)
	}
	return nil
}

func (p *product) ReservedUsername(_ context.Context, username string) (bool, error) {
	normalized, err := NormalizeIdentifier(authn.IdentifierUsername, username)
	if err != nil {
		return false, err
	}
	return p.checkReservedUsername(normalized) != nil, nil
}

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

func reservedTextInDomain(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	_, err := parseUsernameList(trimmed)
	return err == nil
}

const reservedSkippedKey = reservedPath + ":element"

func (p *product) reportSkippedReserved(skipped int) {
	problem := ""
	detail := ""
	if skipped > 0 {
		problem = "an element of the reserved-usernames setting is not a username, so it is ignored"
		detail = strconv.Itoa(skipped)
	}
	p.reportOnce(slog.LevelWarn, reservedSkippedKey, reservedPath, problem, detail)
}

func reservedChangeList(text string, declared bool) []string {
	if declared {
		text = defaults.ReservedUsernames
	}
	names, _ := parseUsernameList(text)
	return names
}

func removesReservedMember(before, after []string) bool {
	held := reservedSet(after)
	for name := range reservedSet(before) {
		if !held[name] {
			return true
		}
	}
	return false
}

func reservedSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		if normalized, err := NormalizeIdentifier(authn.IdentifierUsername, name); err == nil {
			set[normalized] = true
		}
	}
	return set
}
