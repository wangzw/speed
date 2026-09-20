package gate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vislake/speed/pkg/authn"
)

// claimant is one backend together with the claim it reported, indexed the way
// the request path asks about it.
//
// The name is the one Verifier.Name reports rather than the module name: what
// ResolveAll hands back is a set of instances carrying no module name, and the
// name is what an identity's owner qualification is written with, so it has to
// come from the instance.
type claimant struct {
	name     string
	verifier authn.Verifier
	// subjects is the enumerated claim, as a set.
	subjects map[string]struct{}
	// schemes is the whole-scheme claim, folded to lower case.
	schemes map[string]struct{}
	// owner is the backend's answer to an open claim, non-nil exactly when
	// the claim is open. The claim being open is a statement that the
	// backend answers ownership itself, and a backend that cannot be asked
	// is refused at build time rather than skipped at request time.
	owner authn.Owner
}

// ownership is the table built in Init and read-only afterwards.
//
// The three kinds of claim are not stored alike because they are not asked
// alike: a whole scheme and an enumerated subject are exact keys and are
// indexed, while an open claim has no key at all and is answered by the backend
// during the request.
type ownership struct {
	// claimants is every backend, in the order ResolveAll gave them, which
	// is module-name order. It decides nothing at request time and is kept
	// so that diagnostics list the backends the same way on every build.
	claimants []*claimant
	byName    map[string]*claimant
	byScheme  map[string]*claimant
	bySubject map[string]*claimant
	open      []*claimant
}

// buildOwnership turns what ResolveAll handed back into the table.
//
// Every claim is checked for its own shape first and against the other claims
// second, so that a backend whose claim is malformed is named for that rather
// than for the conflict its malformed claim happens to cause.
func buildOwnership(verifiers []authn.Verifier) (*ownership, error) {
	o := &ownership{
		byName:    make(map[string]*claimant, len(verifiers)),
		byScheme:  make(map[string]*claimant),
		bySubject: make(map[string]*claimant),
	}
	for _, v := range verifiers {
		c, err := newClaimant(v)
		if err != nil {
			return nil, err
		}
		if err := o.add(c); err != nil {
			return nil, err
		}
	}
	return o, nil
}

// newClaimant checks one backend's claim for its own shape.
func newClaimant(v authn.Verifier) (*claimant, error) {
	name := v.Name()
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("%w: a backend reports the name %q, and a nameless backend cannot "+
			"be written into an identity's owner qualification or named in a diagnostic",
			authn.ErrInvalidClaim, name)
	}
	// A name is one half of every identity this backend authenticates, and
	// the two halves are split on the first separator. A name carrying one
	// would be cut in the middle: corp/ldap owning alice writes
	// corp/ldap/alice, which reads back as the subject ldap/alice owned by
	// corp — and the qualified form a caller would write for it resolves to
	// no backend at all, with the gateway reporting that nobody reports
	// that name while the backend is standing right there.
	if strings.Contains(name, authn.Separator) {
		return nil, fmt.Errorf("%w: a backend reports the name %q, which carries %q. That "+
			"separator splits an identity into its owner and its subject, so a name "+
			"holding one cannot be told from a name followed by a subject",
			authn.ErrInvalidClaim, name, authn.Separator)
	}
	claim := v.Claim()
	c := &claimant{
		name:     name,
		verifier: v,
		subjects: make(map[string]struct{}, len(claim.Subjects)),
		schemes:  make(map[string]struct{}, len(claim.Schemes)),
	}
	for _, subject := range claim.Subjects {
		if strings.TrimSpace(subject) == "" {
			return nil, fmt.Errorf("%w: backend %q enumerates the subject %q, which is empty or "+
				"blanks alone and names nobody", authn.ErrInvalidClaim, name, subject)
		}
		if strings.Contains(subject, authn.Separator) {
			return nil, fmt.Errorf("%w: backend %q enumerates the subject %q, which carries %q. "+
				"That separator is what an owner qualification is written with, so a subject "+
				"carrying it cannot be told from one", authn.ErrInvalidClaim, name, subject,
				authn.Separator)
		}
		c.subjects[subject] = struct{}{}
	}
	for _, scheme := range claim.Schemes {
		folded := strings.ToLower(strings.TrimSpace(scheme))
		if folded == "" {
			return nil, fmt.Errorf("%w: backend %q claims the authentication scheme %q, which is "+
				"empty or blanks alone and matches no credential",
				authn.ErrInvalidClaim, name, scheme)
		}
		// An authentication scheme name is a token (RFC 7235), and this
		// one is checked rather than trusted because it does not stay
		// inside the process: it is written into the WWW-Authenticate
		// value of every refusal. A name carrying anything else reaches
		// a caller as a challenge that cannot be parsed, and one
		// carrying a line ending would be a caller-visible header the
		// backend never declared.
		if !isToken(folded) {
			return nil, fmt.Errorf("%w: backend %q claims the authentication scheme %q, which is "+
				"not a token. A scheme name is written into the challenge every refusal "+
				"carries, so it is limited to the characters that may appear in one",
				authn.ErrInvalidClaim, name, scheme)
		}
		c.schemes[folded] = struct{}{}
	}
	if claim.Open {
		owner, ok := v.(authn.Owner)
		if !ok {
			return nil, fmt.Errorf("%w: backend %q states an open claim but does not implement "+
				"authn.Owner, so there is nothing to ask whether a bare subject is its own. "+
				"Implement Owns, or drop the open claim and enumerate the subjects",
				authn.ErrInvalidClaim, name)
		}
		c.owner = owner
	}
	if len(c.subjects) == 0 && len(c.schemes) == 0 && c.owner == nil {
		return nil, fmt.Errorf("%w: backend %q claims no subject space at all — no scheme, no "+
			"enumerated subject and no open claim — so it can never be asked about anything",
			authn.ErrInvalidClaim, name)
	}
	return c, nil
}

// add puts one checked claim into the table, refusing an overlap on an exact
// key. Two open claims cannot overlap here, because an open claim has no key;
// that overlap surfaces at request time as a refusal.
func (o *ownership) add(c *claimant) error {
	if other, taken := o.byName[c.name]; taken {
		return fmt.Errorf("%w: two backends report the name %q, and an identity qualified with "+
			"it would name both. Change one of the two names (the other claims %s)",
			authn.ErrClaimConflict, c.name, describeClaim(other))
	}
	for scheme := range c.schemes {
		if other, taken := o.byScheme[scheme]; taken {
			return fmt.Errorf("%w: backends %q and %q both claim the authentication scheme %q "+
				"whole, and a credential bearing it would have two authorities. Change one "+
				"of the two claims", authn.ErrClaimConflict, other.name, c.name, scheme)
		}
	}
	for subject := range c.subjects {
		if other, taken := o.bySubject[subject]; taken {
			return fmt.Errorf("%w: backends %q and %q both enumerate the subject %q, and a "+
				"request for it would have two authorities. Change one of the two claims",
				authn.ErrClaimConflict, other.name, c.name, subject)
		}
	}

	for scheme := range c.schemes {
		o.byScheme[scheme] = c
	}
	for subject := range c.subjects {
		o.bySubject[subject] = c
	}
	o.byName[c.name] = c
	if c.owner != nil {
		o.open = append(o.open, c)
	}
	o.claimants = append(o.claimants, c)
	return nil
}

// isToken reports whether s is an HTTP token: the character set RFC 9110 allows
// in a field name, a scheme name and the like. It is ASCII by construction,
// which is what makes taking the first byte of a scheme safe elsewhere.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// describeClaim renders a claim the way a diagnostic names it.
func describeClaim(c *claimant) string {
	var parts []string
	if len(c.schemes) > 0 {
		parts = append(parts, fmt.Sprintf("%d scheme(s) whole", len(c.schemes)))
	}
	if len(c.subjects) > 0 {
		parts = append(parts, fmt.Sprintf("%d enumerated subject(s)", len(c.subjects)))
	}
	if c.owner != nil {
		parts = append(parts, "an open claim")
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, " and ")
}

// names lists the backends in the order the table was built, for the startup
// diagnostic that is the only way to notice a backend imported but never
// written into the command.
func (o *ownership) names() []string {
	out := make([]string, 0, len(o.claimants))
	for _, c := range o.claimants {
		out = append(out, c.name)
	}
	return out
}

// enumerates reports whether this backend wrote the subject out by hand.
func (c *claimant) enumerates(subject string) bool {
	_, ok := c.subjects[subject]
	return ok
}

// schemes lists every authentication scheme a backend claims whole, in a stable
// order. The redaction rules are built from it, so that what is masked is what
// this assembly actually carries rather than a fixed list.
func (o *ownership) schemes() []string {
	out := make([]string, 0, len(o.byScheme))
	for scheme := range o.byScheme {
		out = append(out, scheme)
	}
	sort.Strings(out)
	return out
}
