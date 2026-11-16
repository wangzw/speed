package invite

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/budgetalias"
	"github.com/vislake/speed/pkg/settings"
)

// budgetPrefix starts every counting key this module writes. The key-value
// store partitions nothing, so a module that did not name its own keys would
// collide with another one silently.
const budgetPrefix = ModuleName + ":budget:"

// The kinds of budget this module keeps. They are the first segment after the
// prefix, so two kinds never share a key.
const (
	budgetIssueAddress = "issue-address"
	budgetIssueIssuer  = "issue-issuer"
	budgetIssueGlobal  = "issue-global"
	budgetProbeToken   = "probe-token"
	budgetProbeClient  = "probe-client"
)

// issueBudget is the set of limits one issuance is charged against, read from
// the settings store together so a decision cannot pair limits from two
// different reads of the store.
type issueBudget struct {
	window  time.Duration
	address int
	issuer  int
	global  int
}

// probeBudget is the set of limits one acceptance probe is charged against.
type probeBudget struct {
	window time.Duration
	token  int
	client int
}

// chargeIssuer charges the issuing account's send budget. It runs before the
// target checks, so an attempt the checks refuse still spends the account that
// made it: a caller cannot walk addresses or tenants for free. It is skipped
// when the context carries no identity, which is a host command issuing an
// invitation without an HTTP caller; the address and deployment budgets still
// bound that path.
//
// The increment happens before the decision, so a throttled issuance still
// spends what it was charged against.
func (p *product) chargeIssuer(ctx context.Context, now time.Time) error {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil
	}
	b := p.issueBudget()
	return p.charge(ctx, budgetIssueIssuer, id.Key(), b.window, b.issuer, now)
}

// chargeAttempt charges the invited address and the deployment. It runs only
// after every target check has passed. The address is counted in its canonical
// inbox form; the deployment budget is the ceiling no operator can pass.
//
// A count stands for a send, and a send is only one that writes its row, so the
// release function gives both counts back when the write does not land. The
// deployment refusal itself takes the address count back here, because the
// address was counted first and the refusal means nothing was sent.
func (p *product) chargeAttempt(ctx context.Context, email string, now time.Time) (release func(), err error) {
	b := p.issueBudget()
	subject := budgetalias.Canonical(email)
	if err := p.charge(ctx, budgetIssueAddress, subject, b.window, b.address, now); err != nil {
		return nil, err
	}
	if err := p.charge(ctx, budgetIssueGlobal, "", b.window, b.global, now); err != nil {
		p.uncharge(ctx, budgetIssueAddress, subject, b.window, now)
		return nil, err
	}
	return func() {
		p.uncharge(ctx, budgetIssueAddress, subject, b.window, now)
		p.uncharge(ctx, budgetIssueGlobal, "", b.window, now)
	}, nil
}

// uncharge takes back one count chargeAttempt added. A count that cannot be
// taken back is an Error line: the write it stood for did not happen, and the
// caller's answer is already decided, so nothing else changes.
func (p *product) uncharge(ctx context.Context, kind, subject string, window time.Duration, now time.Time) {
	if p.kv == nil {
		return
	}
	if _, err := p.kv.IncrByFloatWithTTL(ctx, budgetKey(kind, subject, window, now), -1, 2*window); err != nil {
		p.logger.Error(ModuleName+": returning an unused send budget failed",
			slog.String("budget", kind), slog.String("cause", err.Error()))
	}
}

// chargeProbe applies the acceptance probe budgets: one keyed by the token's
// digest and one by the client address. It runs before the token is looked up,
// so an over-budget probe is refused whether or not the token exists and the
// refusal reveals nothing about it.
//
// The token budget is keyed by the digest, never the plaintext: the token is the
// whole credential of acceptance and must not be written anywhere.
func (p *product) chargeProbe(ctx context.Context, token, client string, now time.Time) error {
	b := p.probeBudget()
	if token != "" {
		if err := p.charge(ctx, budgetProbeToken, tokenDigest(token), b.window, b.token, now); err != nil {
			return err
		}
	}
	if client != "" {
		if err := p.charge(ctx, budgetProbeClient, clientSubject(client), b.window, b.client, now); err != nil {
			return err
		}
	}
	return nil
}

// clientSubject is the key a client address is counted under. An IPv6 address
// counts by its /64: a host is routinely handed a whole /64, and counting whole
// addresses would let one host walk through its own prefix and never fill the
// coarse bound. An address that does not parse is counted as it is written.
func clientSubject(client string) string {
	ip := net.ParseIP(client)
	if ip == nil {
		return client
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	v6 := ip.To16()
	if v6 == nil {
		return client
	}
	return (&net.IPNet{IP: v6.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
}

// charge counts one unit against a fixed-window budget and reports whether the
// window is still within its limit. A counter that cannot be written is an
// outage and not a pass: the error carries authn.ErrUnavailable, so the refusal
// direction is to refuse rather than to let an uncounted message or probe
// through.
func (p *product) charge(ctx context.Context, kind, subject string, window time.Duration, limit int, now time.Time) error {
	if p.kv == nil {
		// The capability is required, so this is only a hand-built product in a
		// case that did not supply one: the default limit is not observable and
		// the safest direction is to refuse.
		return fmt.Errorf("%w: %s: the budget counter is not configured", authn.ErrUnavailable, ModuleName)
	}
	count, err := p.kv.IncrByFloatWithTTL(ctx, budgetKey(kind, subject, window, now), 1, 2*window)
	if err != nil {
		return fmt.Errorf("%w: %s: counting an invitation budget: %w", authn.ErrUnavailable, ModuleName, err)
	}
	if count > float64(limit) {
		return &authn.ThrottledError{RetryAfter: untilNextWindow(now, window)}
	}
	return nil
}

// budgetKey is the key one budget's count for one window lives under. The
// subject is escaped so a colon in it cannot build another budget's key; the
// percent sign is escaped first, so the escape of a colon is not escaped twice.
func budgetKey(kind, subject string, window time.Duration, now time.Time) string {
	index := now.UnixNano() / int64(window)
	var b strings.Builder
	b.WriteString(budgetPrefix)
	b.WriteString(kind)
	b.WriteByte(':')
	b.WriteString(escapeBudget(subject))
	b.WriteByte(':')
	b.WriteString(strconv.FormatInt(index, 10))
	return b.String()
}

// escapeBudget percent-encodes the two characters that would otherwise be read
// as separators.
func escapeBudget(segment string) string {
	if !strings.ContainsAny(segment, ":%") {
		return segment
	}
	segment = strings.ReplaceAll(segment, "%", "%25")
	return strings.ReplaceAll(segment, ":", "%3A")
}

// untilNextWindow is how long the window a count fell into has left: the delay a
// throttled refusal states, never below one second so that a client does not
// retry in the same window.
func untilNextWindow(now time.Time, window time.Duration) time.Duration {
	if window <= 0 {
		return time.Second
	}
	next := now.Truncate(window).Add(window)
	wait := next.Sub(now)
	if wait < time.Second {
		return time.Second
	}
	return wait
}

// issueBudget reads the issuance limits. A read that fails or lands outside the
// domain is the declared default, reported once at Error; a security budget is
// not silently disabled on a bad row, it falls back to its declared default.
func (p *product) issueBudget() issueBudget {
	return issueBudget{
		window:  readTuned(p, issueWindowPath, defaults.IssueWindow, windowInDomain),
		address: readTuned(p, issueAddressPath, defaults.IssueAddressLimit, limitInDomain),
		issuer:  readTuned(p, issueIssuerPath, defaults.IssueIssuerLimit, limitInDomain),
		global:  readTuned(p, issueGlobalPath, defaults.IssueGlobalLimit, limitInDomain),
	}
}

// probeBudget reads the probe limits the same way.
func (p *product) probeBudget() probeBudget {
	return probeBudget{
		window: readTuned(p, probeWindowPath, defaults.ProbeWindow, windowInDomain),
		token:  readTuned(p, probeTokenPath, defaults.ProbeTokenLimit, limitInDomain),
		client: readTuned(p, probeClientPath, defaults.ProbeClientLimit, limitInDomain),
	}
}

// readTuned reads one setting and falls back to its declared default when the
// store is absent, the read fails, or the value is outside its domain. The fault
// is reported once per distinct detail.
func readTuned[T any](p *product, path string, fallback T, inDomain func(T) bool) T {
	if p.store == nil {
		return fallback
	}
	v, err := settings.Get[T](p.store, settings.Global(), path)
	switch {
	case err != nil:
		p.report(path, "the setting cannot be read, so its declared default is used", err.Error())
		return fallback
	case !inDomain(v):
		p.report(path, "the setting holds a value outside its domain, so its declared default is used",
			fmt.Sprint(v))
		return fallback
	}
	p.report(path, "", "")
	return v
}
