// Package budgetalias folds the writings of one email inbox into the single form
// the verification-code budgets and the resend window are counted against.
//
// It is not a normalization of the address. The account module owns what an
// identifier is, the pending record keeps the address the route accepted, and the
// message goes to that address as written. Only the budget and resend keys use
// this form, so one mailbox cannot multiply its allowance by addressing itself
// with a plus suffix, with dots in a Gmail local part, or through the other Gmail
// domain.
package budgetalias

import "strings"

// Canonical returns the inbox form of an email address: lowercased, the local
// part cut at its first "+", and, for gmail.com and googlemail.com only, the dots
// of the local part removed and googlemail.com rewritten to gmail.com.
//
// The "+suffix" is dropped for every domain. Dots are dropped only for the two
// Gmail domains: at every other provider a dot is part of the address and two
// writings with and without it are two mailboxes. A value with no "@" is
// returned lowercased as it stands; every caller builds keys from an address the
// route already accepted, so that shape is a defect and not a case to fold.
func Canonical(email string) string {
	folded := strings.ToLower(strings.TrimSpace(email))
	local, domain, ok := strings.Cut(folded, "@")
	if !ok {
		return folded
	}
	if i := strings.IndexByte(local, '+'); i >= 0 {
		local = local[:i]
	}
	switch domain {
	case "gmail.com", "googlemail.com":
		local = strings.ReplaceAll(local, ".", "")
		domain = "gmail.com"
	}
	return local + "@" + domain
}
