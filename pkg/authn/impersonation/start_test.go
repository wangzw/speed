package impersonation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/vislake/speed/pkg/audit"
	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/authntest"
	"github.com/vislake/speed/pkg/internal/dbtest"
)

// TestStartBeginsAnImpersonation holds the happy path: the record is written with
// the actor, the target, the tenant, the reason, the actor's session and a
// lifetime, the mandatory audit row is written, and the started outcome is
// counted.
func TestStartBeginsAnImpersonation(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		g, err := r.p.Start(context.Background(), adminIdentity(t), request())
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if g.Actor != actorID || g.Target != targetID || g.Tenant != tenantA || g.SessionID != sessionID {
			t.Errorf("the grant reads %+v, want the actor, the target, the tenant and the session", g)
		}
		if !g.EndedAt.IsZero() {
			t.Errorf("a fresh record reads as ended: %+v", g)
		}
		wantEnd := base.Add(30 * time.Minute)
		if !g.EndsAt.Equal(wantEnd) {
			t.Errorf("EndsAt = %v, want %v (the max-ttl default)", g.EndsAt, wantEnd)
		}
		if r.recordCount() != 1 {
			t.Errorf("the table holds %d records, want 1", r.recordCount())
		}
		rows := r.audit.entries()
		if len(rows) != 1 || rows[0].Action != actionStart || rows[0].Outcome != outcomeGranted {
			t.Errorf("the audit rows are %+v, want one granted start", rows)
		}
		if rows[0].Object != "account:"+string(targetID) || rows[0].Purpose != "a support call" {
			t.Errorf("the granted row reads object %q purpose %q, want the target and the reason", rows[0].Object, rows[0].Purpose)
		}
		if rows[0].Detail[detailGrantID] != g.ID || rows[0].Detail[detailTenant] != tenantA {
			t.Errorf("the granted row's detail is %v, want the grant and the tenant", rows[0].Detail)
		}
		if got := r.counter(outcomeStarted); got != 1 {
			t.Errorf("the started counter reads %d, want 1", got)
		}
	})
}

// TestStartRefusesEachStructuralRestriction holds each group's conditions: a
// violation is refused with the sentinel its nature carries, no record is
// written, and a denied attempt is counted. The cases are the closed set the
// design names.
func TestStartRefusesEachStructuralRestriction(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func(t *testing.T, r *rig) authn.Identity
		req   func() authn.ImpersonationRequest
		want  error
	}{
		{
			name:  "a: the actor is already impersonating",
			build: func(t *testing.T, _ *rig) authn.Identity { return impersonatingIdentity(t, sessionBackend) },
			want:  authn.ErrImpersonationRestricted,
		},
		{
			name: "a: the actor is a static subject",
			build: func(t *testing.T, r *rig) authn.Identity {
				// The subject is a live account, so only the static check can
				// refuse it.
				r.accounts.put(authn.AccountID("static.ops"), authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
				return adminIdentity(t, authntest.WithAccount(authn.AccountID("static.ops")), authntest.WithBackend("static"))
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name: "a: the actor's account is disabled",
			build: func(t *testing.T, r *rig) authn.Identity {
				r.accounts.put(actorID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
				return adminIdentity(t)
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name: "a: the actor's account is a service account",
			build: func(t *testing.T, r *rig) authn.Identity {
				r.accounts.put(actorID, authn.Account{Kind: authn.KindService, Status: authn.StatusActive})
				return adminIdentity(t)
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name:  "a: the actor has no session to bind",
			build: func(t *testing.T, _ *rig) authn.Identity { return adminIdentity(t, authntest.WithSession("")) },
			want:  authn.ErrImpersonationNeedsSession,
		},
		{
			name: "a: the assurance is single",
			build: func(t *testing.T, _ *rig) authn.Identity {
				return adminIdentity(t, authntest.WithAssurance(authn.AssuranceSingle))
			},
			want: authn.ErrStepUpRequired,
		},
		{
			name: "a: the authentication is too old",
			build: func(t *testing.T, _ *rig) authn.Identity {
				return adminIdentity(t, authntest.WithAuthTime(base.Add(-10*time.Minute)))
			},
			want: authn.ErrStepUpRequired,
		},
		{
			name: "a: the multi-factor was SMS alone",
			build: func(t *testing.T, _ *rig) authn.Identity {
				return adminIdentity(t, authntest.WithMethods(authn.MethodPassword, authn.MethodSMS))
			},
			want: authn.ErrStepUpRequired,
		},
		{
			name: "a: the actor has no seasoned non-SMS factor",
			build: func(t *testing.T, r *rig) authn.Identity {
				r.mfa.seasoned = false
				return adminIdentity(t)
			},
			want: authn.ErrFactorNotSeasoned,
		},
		{
			name:  "b: the target does not exist",
			build: func(t *testing.T, _ *rig) authn.Identity { return adminIdentity(t) },
			req: func() authn.ImpersonationRequest {
				return authn.ImpersonationRequest{Target: "acct-missing", Tenant: tenantA, Reason: "x"}
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name: "b: the target is the actor",
			build: func(t *testing.T, r *rig) authn.Identity {
				// The actor is a member of the tenant, so only the self check can
				// refuse this.
				r.memberships.set(actorID, tenantA, true)
				return adminIdentity(t)
			},
			req: func() authn.ImpersonationRequest {
				return authn.ImpersonationRequest{Target: actorID, Tenant: tenantA, Reason: "x"}
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name: "b: the target is a service account",
			build: func(t *testing.T, r *rig) authn.Identity {
				r.accounts.put(targetID, authn.Account{Kind: authn.KindService, Status: authn.StatusActive})
				return adminIdentity(t)
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name: "b: the target is a static subject",
			build: func(t *testing.T, r *rig) authn.Identity {
				// The subject is a live member of the tenant, so only the static
				// check can refuse it.
				r.accounts.put(authn.AccountID("static.ops"), authn.Account{Kind: authn.KindUser, Status: authn.StatusActive})
				r.memberships.set(authn.AccountID("static.ops"), tenantA, true)
				return adminIdentity(t)
			},
			req: func() authn.ImpersonationRequest {
				return authn.ImpersonationRequest{Target: "static.ops", Tenant: tenantA, Reason: "x"}
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name: "b: the target is protected",
			build: func(t *testing.T, r *rig) authn.Identity {
				r.accounts.put(targetID, authn.Account{Kind: authn.KindUser, Status: authn.StatusActive, Protected: true})
				return adminIdentity(t)
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name: "b: the target is disabled",
			build: func(t *testing.T, r *rig) authn.Identity {
				r.accounts.put(targetID, authn.Account{Kind: authn.KindUser, Status: authn.StatusDisabled})
				return adminIdentity(t)
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name: "b: the target is not a member of the tenant",
			build: func(t *testing.T, r *rig) authn.Identity {
				r.memberships.set(targetID, tenantA, false)
				return adminIdentity(t)
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name:  "c: the tenant is empty",
			build: func(t *testing.T, _ *rig) authn.Identity { return adminIdentity(t) },
			req: func() authn.ImpersonationRequest {
				return authn.ImpersonationRequest{Target: targetID, Tenant: "", Reason: "x"}
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name:  "c: the tenant carries a blank",
			build: func(t *testing.T, _ *rig) authn.Identity { return adminIdentity(t) },
			req: func() authn.ImpersonationRequest {
				return authn.ImpersonationRequest{Target: targetID, Tenant: "tenant a", Reason: "x"}
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name: "c: the tenant starts with a blank",
			build: func(t *testing.T, r *rig) authn.Identity {
				// The membership is in place, so only the tenant shape check can
				// refuse this.
				r.memberships.set(targetID, " tenant-a", true)
				return adminIdentity(t)
			},
			req: func() authn.ImpersonationRequest {
				return authn.ImpersonationRequest{Target: targetID, Tenant: " tenant-a", Reason: "x"}
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name:  "c: the reason is blank after trimming",
			build: func(t *testing.T, _ *rig) authn.Identity { return adminIdentity(t) },
			req: func() authn.ImpersonationRequest {
				return authn.ImpersonationRequest{Target: targetID, Tenant: tenantA, Reason: "   "}
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name:  "c: the reason starts with a control character",
			build: func(t *testing.T, _ *rig) authn.Identity { return adminIdentity(t) },
			req: func() authn.ImpersonationRequest {
				return authn.ImpersonationRequest{Target: targetID, Tenant: tenantA, Reason: "\x01x"}
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name:  "c: the reason carries a control character",
			build: func(t *testing.T, _ *rig) authn.Identity { return adminIdentity(t) },
			req: func() authn.ImpersonationRequest {
				return authn.ImpersonationRequest{Target: targetID, Tenant: tenantA, Reason: "line\nbreak"}
			},
			want: authn.ErrImpersonationRestricted,
		},
		{
			name:  "c: the reason is over 500 characters",
			build: func(t *testing.T, _ *rig) authn.Identity { return adminIdentity(t) },
			req: func() authn.ImpersonationRequest {
				reason := make([]rune, 501)
				for i := range reason {
					reason[i] = 'x'
				}
				return authn.ImpersonationRequest{Target: targetID, Tenant: tenantA, Reason: string(reason)}
			},
			want: authn.ErrImpersonationRestricted,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				id := c.build(t, r)
				req := request()
				if c.req != nil {
					req = c.req()
				}
				_, err := r.p.Start(context.Background(), id, req)
				if !errors.Is(err, c.want) {
					t.Fatalf("Start = %v, want %v", err, c.want)
				}
				if r.recordCount() != 0 {
					t.Errorf("a refused start wrote %d records, want none", r.recordCount())
				}
				rows := r.audit.entries()
				if len(rows) != 1 || rows[0].Outcome != outcomeDenied {
					t.Errorf("the audit rows are %+v, want one denied attempt", rows)
				}
				if got := r.counter(outcomeDenied); got != 1 {
					t.Errorf("the denied counter reads %d, want 1", got)
				}
			})
		})
	}
}

// TestStartRefusesASecondRecordOnOneSession holds group d: a session that already
// carries a valid record is refused with ErrImpersonationActive.
func TestStartRefusesASecondRecordOnOneSession(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		if _, err := r.p.Start(ctx, adminIdentity(t), request()); err != nil {
			t.Fatalf("the first Start: %v", err)
		}
		_, err := r.p.Start(ctx, adminIdentity(t), request())
		if !errors.Is(err, authn.ErrImpersonationActive) {
			t.Fatalf("the second Start = %v, want ErrImpersonationActive", err)
		}
		if r.recordCount() != 1 {
			t.Errorf("the table holds %d records, want the first one alone", r.recordCount())
		}
	})
}

// TestStartAsksTheDeciderAndRefusesWhenItDeclines holds the decision seam: a
// decline is ErrForbidden, an absent seam refuses everything, and a seam that
// returns an error or panics is ErrUnavailable and never a permission.
func TestStartAsksTheDeciderAndRefusesWhenItDeclines(t *testing.T) {
	decisions := []struct {
		name  string
		setup func(r *rig)
		want  error
	}{
		{"a decline", func(r *rig) { r.decider.allow = false }, authn.ErrForbidden},
		{"an absent decider", func(r *rig) { r.p.decider = nil }, authn.ErrForbidden},
		{"an error", func(r *rig) { r.decider.err = errFromStore }, authn.ErrUnavailable},
		{"a panic", func(r *rig) { r.decider.panic = true }, authn.ErrUnavailable},
	}
	for _, c := range decisions {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				c.setup(r)
				_, err := r.p.Start(context.Background(), adminIdentity(t), request())
				if !errors.Is(err, c.want) {
					t.Fatalf("Start = %v, want %v", err, c.want)
				}
				if r.recordCount() != 0 {
					t.Errorf("a refused start wrote %d records, want none", r.recordCount())
				}
			})
		})
	}
}

// TestStartAsksTheDeciderWithTheFactsOfTheAction holds the question: the action,
// the actor, the target, the tenant, the reason and the lifetime in seconds.
func TestStartAsksTheDeciderWithTheFactsOfTheAction(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		if _, err := r.p.Start(context.Background(), adminIdentity(t), request()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if len(r.decider.calls) != 1 {
			t.Fatalf("the decider was asked %d times, want 1", len(r.decider.calls))
		}
		d := r.decider.calls[0]
		if d.Action != authn.ActionImpersonateStart || d.Actor.Account() != actorID ||
			d.Target != string(targetID) || d.TargetAccount != string(targetID) || d.Tenant != tenantA {
			t.Errorf("the decision reads %+v, want the start action on the target in the tenant", d)
		}
		if d.Params["reason"] != "a support call" {
			t.Errorf("the decision's reason is %q, want the request's", d.Params["reason"])
		}
		// The lifetime is the settled one in whole seconds: the 30-minute
		// max-ttl default is 1800 seconds.
		if d.Params["ttl"] != "1800" {
			t.Errorf("the decision's ttl is %q, want the lifetime in seconds 1800", d.Params["ttl"])
		}
	})
}

// TestStartAsksTheDeciderWithTheTrimmedReason holds that the reason the Decider
// sees is the trimmed one, the string the record and the audit row carry.
func TestStartAsksTheDeciderWithTheTrimmedReason(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		req := request()
		req.Reason = "  a support call  "
		if _, err := r.p.Start(context.Background(), adminIdentity(t), req); err != nil {
			t.Fatalf("Start: %v", err)
		}
		if len(r.decider.calls) != 1 {
			t.Fatalf("the decider was asked %d times, want 1", len(r.decider.calls))
		}
		if got := r.decider.calls[0].Params["reason"]; got != "a support call" {
			t.Errorf("the decision's reason is %q, want the trimmed reason", got)
		}
	})
}

// TestStartWritesTheMandatoryAuditBeforeTheTransaction holds the order the design
// fixes: the mandatory audit is written outside the record transaction. The
// audit's row is written through a second connection to the same SQLite file, so
// that a start that wrote the audit while holding its own write lock would fail
// here and not merely be mis-ordered.
func TestStartWritesTheMandatoryAuditBeforeTheTransaction(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "imp.db")
	primary := dbtest.SQLite(t, dsn, nil).DB().Session(&gorm.Session{Logger: logger.Discard})
	applyMigrations(t, primary, sqliteDialect)
	second := dbtest.SQLite(t, dsn, nil).DB().Session(&gorm.Session{Logger: logger.Discard})
	if err := second.Exec("CREATE TABLE audit_probe (id INTEGER PRIMARY KEY, action TEXT)").Error; err != nil {
		t.Fatalf("creating the probe table: %v", err)
	}

	r := newRig(t, primary)
	probe := &probeAudit{handle: second}
	r.withAudit(probe)
	if _, err := r.p.Start(context.Background(), adminIdentity(t), request()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !probe.wrote {
		t.Error("the audit's own write never landed, so the audit was written inside the record transaction")
	}
	if got := r.recordCount(); got != 1 {
		t.Errorf("the table holds %d records, want 1", got)
	}
}

// probeAudit writes a real row through its own connection to the same database, as
// an audit writing to its own connection would, and reports whether it landed.
type probeAudit struct {
	handle *gorm.DB
	wrote  bool
}

func (a *probeAudit) Record(ctx context.Context, e audit.Entry) error {
	a.wrote = false
	res := a.handle.WithContext(ctx).Exec("INSERT INTO audit_probe (action) VALUES (?)", e.Action)
	if res.Error != nil {
		return res.Error
	}
	a.wrote = true
	return nil
}

// TestStartRefusesWhenTheMandatoryAuditFails holds the mandatory tier: a start
// whose audit cannot be written is refused with ErrAuditRequired and writes no
// record.
func TestStartRefusesWhenTheMandatoryAuditFails(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.audit.err = errFromStore
		_, err := r.p.Start(context.Background(), adminIdentity(t), request())
		if !errors.Is(err, authn.ErrAuditRequired) {
			t.Fatalf("Start = %v, want ErrAuditRequired", err)
		}
		if r.recordCount() != 0 {
			t.Errorf("the table holds %d records, want none", r.recordCount())
		}
	})
}

// TestTwoStartsOnOneSessionRaceToOne holds the assumption the design names: two
// concurrent starts on one session leave exactly one record, and the loser is
// told ErrImpersonationActive. The partial unique index is what arbitrates.
func TestTwoStartsOnOneSessionRaceToOne(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		ctx := context.Background()
		id := adminIdentity(t)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = r.p.Start(ctx, id, request())
			}()
		}
		wg.Wait()

		ok, active := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, authn.ErrImpersonationActive):
				active++
			default:
				t.Errorf("a concurrent start returned %v, want nil or ErrImpersonationActive", err)
			}
		}
		if ok != 1 || active != 1 {
			t.Errorf("the race left %d successes and %d actives, want one of each", ok, active)
		}
		if got := r.recordCount(); got != 1 {
			t.Errorf("the table holds %d records, want 1", got)
		}
	})
}

// TestStartAcceptsAReasonOfExactlyMaxRunes holds the upper boundary of the
// reason: a reason of exactly the maximum is admitted, one longer is refused.
// The length is written as a literal, so that moving the bound does not move the
// input with it.
func TestStartAcceptsAReasonOfExactlyMaxRunes(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		req := request()
		req.Reason = strings.Repeat("x", 500)
		if _, err := r.p.Start(context.Background(), adminIdentity(t), req); err != nil {
			t.Fatalf("Start with a %d-character reason = %v, want nil", maxReasonRunes, err)
		}
	})
}

// TestStartAcceptsAWebAuthnSecondFactor holds the second strong factor the design
// admits: a passkey beside the password is a non-SMS second factor, as a one-time
// code is.
func TestStartAcceptsAWebAuthnSecondFactor(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		id := adminIdentity(t, authntest.WithMethods(authn.MethodPassword, authn.MethodWebAuthn))
		if _, err := r.p.Start(context.Background(), id, request()); err != nil {
			t.Fatalf("Start with a WebAuthn factor = %v, want nil", err)
		}
		if got := r.recordCount(); got != 1 {
			t.Errorf("the table holds %d records, want 1", got)
		}
	})
}

// TestStartRefusesANonLocalMultiFactor holds the requirement the design names:
// the multi-factor must be locally verified, so an enterprise IdP's declaration
// of the same level does not admit a start.
func TestStartRefusesANonLocalMultiFactor(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		id := adminIdentity(t, authntest.WithIDPAssured(true),
			authntest.WithProvider("12345678-1234-1234-1234-123456789012"))
		_, err := r.p.Start(context.Background(), id, request())
		if !errors.Is(err, authn.ErrStepUpRequired) {
			t.Fatalf("Start with an IdP-declared multi-factor = %v, want ErrStepUpRequired", err)
		}
	})
}

// TestTheLifetimeIsCappedByTheSettingAndTheCeiling holds the two upper bounds the
// design fixes: a request longer than the setting is cut to the setting, and a
// setting at the top of its domain is still cut at the hard hour.
func TestTheLifetimeIsCappedByTheSettingAndTheCeiling(t *testing.T) {
	t.Run("a request longer than the setting", func(t *testing.T) {
		onEachDialect(t, func(t *testing.T, r *rig) {
			r.store.set(maxTTLPath, 20*time.Minute)
			req := request()
			req.TTL = time.Hour
			g, err := r.p.Start(context.Background(), adminIdentity(t), req)
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			if want := base.Add(20 * time.Minute); !g.EndsAt.Equal(want) {
				t.Errorf("EndsAt = %v, want the setting %v", g.EndsAt, want)
			}
		})
	})
	t.Run("a setting at the top of its domain", func(t *testing.T) {
		onEachDialect(t, func(t *testing.T, r *rig) {
			r.store.set(maxTTLPath, time.Hour)
			// The session outlives the setting, so the shorter of the two wins.
			r.sessions.session.ExpiresAt = base.Add(2 * time.Hour)
			g, err := r.p.Start(context.Background(), adminIdentity(t), request())
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			if want := base.Add(time.Hour); !g.EndsAt.Equal(want) {
				t.Errorf("EndsAt = %v, want %v", g.EndsAt, want)
			}
		})
	})
}

// TestStartRefusesASessionThatIsGone holds the corner the design names: a session
// whose status read says it is not there leaves no lifetime to bind the record
// by, so the start is refused as needing a session, not as unavailable.
func TestStartRefusesASessionThatIsGone(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.sessions.statusErr = authn.ErrSessionNotFound
		_, err := r.p.Start(context.Background(), adminIdentity(t), request())
		if !errors.Is(err, authn.ErrImpersonationNeedsSession) {
			t.Fatalf("Start = %v, want ErrImpersonationNeedsSession", err)
		}
	})
}

// TestAnUnverifiedAccountCannotStart holds the signup gate of a start: a pending
// self-registration is refused 403 contact-required before the strength
// conditions, writes no record, counts no denied attempt and writes no audit
// row. An account that never registered starts as before, and a service account
// is refused as an impersonation restriction without its state being read.
func TestAnUnverifiedAccountCannotStart(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.verification.state = authn.SignupStatus{State: authn.SignupPending}
		_, err := r.p.Start(context.Background(), adminIdentity(t), request())
		if !errors.Is(err, authn.ErrContactRequired) {
			t.Fatalf("Start = %v, want ErrContactRequired", err)
		}
		if r.recordCount() != 0 {
			t.Errorf("a refused start wrote %d records, want none", r.recordCount())
		}
		if rows := r.audit.entries(); len(rows) != 0 {
			t.Errorf("a refused start wrote %d audit rows, want none", len(rows))
		}
		if got := r.counter(outcomeDenied); got != 0 {
			t.Errorf("a refused start counted %d denied attempts, want none", got)
		}
		if len(r.decider.calls) != 0 {
			t.Errorf("the Decider was asked %d times, want none", len(r.decider.calls))
		}
	})
}

// TestAVerifiedOrNeverRegisteredAccountStarts holds the exemption of the gate:
// neither a verified self-registration nor an account that never registered is
// refused by it.
func TestAVerifiedOrNeverRegisteredAccountStarts(t *testing.T) {
	for _, c := range []struct {
		name  string
		state authn.SignupStatus
	}{
		{"a never-registered account", authn.SignupStatus{State: authn.SignupNone}},
		{"a verified registration", authn.SignupStatus{State: authn.SignupVerified}},
	} {
		t.Run(c.name, func(t *testing.T) {
			onEachDialect(t, func(t *testing.T, r *rig) {
				r.verification.state = c.state
				if _, err := r.p.Start(context.Background(), adminIdentity(t), request()); err != nil {
					t.Fatalf("Start = %v, want a grant", err)
				}
			})
		})
	}
}

// TestAServiceAccountIsNotGated holds the exemption of the account kind: a
// service account is not a self-registration, so the gate does not read its
// state, and the kind check refuses the start with the structural answer.
func TestAServiceAccountIsNotGated(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.accounts.put(actorID, authn.Account{Kind: authn.KindService, Status: authn.StatusActive})
		r.verification.state = authn.SignupStatus{State: authn.SignupPending}
		_, err := r.p.Start(context.Background(), adminIdentity(t), request())
		if !errors.Is(err, authn.ErrImpersonationRestricted) {
			t.Fatalf("Start = %v, want ErrImpersonationRestricted", err)
		}
		if got := r.verification.readCount(); got != 0 {
			t.Errorf("a service account's signup state was read %d times, want none", got)
		}
	})
}

// TestTheSignupGateIsAnsweredBeforeTheStrengthConditions holds the gate's place
// in group a: an initiator that fails the assurance, method-set and Seasoned
// conditions is answered contact-required, not the step-up 401 or the
// factor-not-seasoned 403 those conditions would give. The same identity with a
// state that is not pending reaches the step-up, which proves it does fail the
// conditions the gate ran ahead of.
func TestTheSignupGateIsAnsweredBeforeTheStrengthConditions(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.mfa.seasoned = false
		id := adminIdentity(t, authntest.WithAssurance(authn.AssuranceSingle))

		r.verification.state = authn.SignupStatus{State: authn.SignupPending}
		_, err := r.p.Start(context.Background(), id, request())
		if !errors.Is(err, authn.ErrContactRequired) {
			t.Fatalf("Start with a weak, unseasoned, pending initiator = %v, want ErrContactRequired", err)
		}
		if len(r.decider.calls) != 0 {
			t.Errorf("the Decider was asked %d times, want none", len(r.decider.calls))
		}

		r.verification.state = authn.SignupStatus{State: authn.SignupNone}
		if _, err = r.p.Start(context.Background(), id, request()); !errors.Is(err, authn.ErrStepUpRequired) {
			t.Fatalf("Start with a weak, unseasoned initiator = %v, want ErrStepUpRequired", err)
		}
	})
}

// TestTheSignupGateJudgesTheInitiatorNotTheTarget holds the subject of the gate:
// an impersonating identity's Account() is the target, so reading the state from
// it would let the target's pending registration refuse the administrator's
// start. The initiator's own state decides, and the target's never enters the
// judgement.
func TestTheSignupGateJudgesTheInitiatorNotTheTarget(t *testing.T) {
	t.Run("a pending target does not refuse the initiator", func(t *testing.T) {
		onEachDialect(t, func(t *testing.T, r *rig) {
			r.verification.setState(targetID, authn.SignupStatus{State: authn.SignupPending})
			r.verification.setState(actorID, authn.SignupStatus{State: authn.SignupVerified})
			_, err := r.p.Start(context.Background(), impersonatingIdentity(t, sessionBackend), request())
			if !errors.Is(err, authn.ErrImpersonationRestricted) {
				t.Fatalf("Start = %v, want ErrImpersonationRestricted", err)
			}
			asked := r.verification.askedIDs()
			if len(asked) != 1 || asked[0] != actorID {
				t.Errorf("the signup state was read for %v, want the initiator %q alone", asked, actorID)
			}
		})
	})
	t.Run("a pending initiator is refused by its own state", func(t *testing.T) {
		onEachDialect(t, func(t *testing.T, r *rig) {
			r.verification.setState(actorID, authn.SignupStatus{State: authn.SignupPending})
			r.verification.setState(targetID, authn.SignupStatus{State: authn.SignupVerified})
			_, err := r.p.Start(context.Background(), impersonatingIdentity(t, sessionBackend), request())
			if !errors.Is(err, authn.ErrContactRequired) {
				t.Fatalf("Start = %v, want ErrContactRequired", err)
			}
		})
	})
}

// TestAnUnreadableSignupStateCannotStart holds the failure direction: a state
// that cannot be read is unavailable, writes no record and no audit row, and
// counts no denied attempt.
func TestAnUnreadableSignupStateCannotStart(t *testing.T) {
	onEachDialect(t, func(t *testing.T, r *rig) {
		r.verification.err = errors.New("boom")
		_, err := r.p.Start(context.Background(), adminIdentity(t), request())
		if !errors.Is(err, authn.ErrUnavailable) {
			t.Fatalf("Start = %v, want ErrUnavailable", err)
		}
		if r.recordCount() != 0 {
			t.Errorf("the failed start wrote %d records, want none", r.recordCount())
		}
		if rows := r.audit.entries(); len(rows) != 0 {
			t.Errorf("the failed start wrote %d audit rows, want none", len(rows))
		}
		if got := r.counter(outcomeDenied); got != 0 {
			t.Errorf("the failed start counted %d denied attempts, want none", got)
		}
	})
}
