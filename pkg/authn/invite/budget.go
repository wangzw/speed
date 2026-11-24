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

const budgetPrefix = ModuleName + ":budget:"

const (
	budgetIssueAddress = "issue-address"
	budgetIssueIssuer  = "issue-issuer"
	budgetIssueGlobal  = "issue-global"
	budgetProbeToken   = "probe-token"
	budgetProbeClient  = "probe-client"
)

type issueBudget struct {
	window  time.Duration
	address int
	issuer  int
	global  int
}

type probeBudget struct {
	window time.Duration
	token  int
	client int
}

// chargeIssuer charges the issuing account's send budget. It runs before the
// target checks, so a refused attempt still spends the issuing account.
func (p *product) chargeIssuer(ctx context.Context, now time.Time) error {
	id, ok := authn.FromContext(ctx)
	if !ok {
		return nil
	}
	b := p.issueBudget()
	return p.charge(ctx, budgetIssueIssuer, id.Key(), b.window, b.issuer, now)
}

// chargeAttempt charges the invited address and the deployment. It runs only
// after every target check has passed.
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

func (p *product) uncharge(ctx context.Context, kind, subject string, window time.Duration, now time.Time) {
	if p.kv == nil {
		return
	}
	if _, err := p.kv.IncrByFloatWithTTL(ctx, budgetKey(kind, subject, window, now), -1, 2*window); err != nil {
		p.logger.Error(ModuleName+": returning an unused send budget failed",
			slog.String("budget", kind), slog.String("cause", err.Error()))
	}
}

// chargeProbe applies the acceptance probe budgets. It runs before the token is
// looked up, and its token key is the digest, never the plaintext.
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

// charge counts one unit against a fixed-window budget. A counter that cannot be
// written is an outage, never a pass.
func (p *product) charge(ctx context.Context, kind, subject string, window time.Duration, limit int, now time.Time) error {
	if p.kv == nil {
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

// budgetKey builds the key one budget's count for one window lives under. The
// subject is escaped so a colon in it cannot build another budget's key.
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

func escapeBudget(segment string) string {
	if !strings.ContainsAny(segment, ":%") {
		return segment
	}
	segment = strings.ReplaceAll(segment, "%", "%25")
	return strings.ReplaceAll(segment, ":", "%3A")
}

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

func (p *product) issueBudget() issueBudget {
	return issueBudget{
		window:  readTuned(p, issueWindowPath, defaults.IssueWindow, windowInDomain),
		address: readTuned(p, issueAddressPath, defaults.IssueAddressLimit, limitInDomain),
		issuer:  readTuned(p, issueIssuerPath, defaults.IssueIssuerLimit, limitInDomain),
		global:  readTuned(p, issueGlobalPath, defaults.IssueGlobalLimit, limitInDomain),
	}
}

func (p *product) probeBudget() probeBudget {
	return probeBudget{
		window: readTuned(p, probeWindowPath, defaults.ProbeWindow, windowInDomain),
		token:  readTuned(p, probeTokenPath, defaults.ProbeTokenLimit, limitInDomain),
		client: readTuned(p, probeClientPath, defaults.ProbeClientLimit, limitInDomain),
	}
}

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
