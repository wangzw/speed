package mfa

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/vislake/speed/pkg/authn"
	"github.com/vislake/speed/pkg/authn/internal/step"
)

// errFactorDefect is what a factor's answer is wrapped in when the answer is not
// one that call may give. The factor is at fault, not the client, so the call
// ends as an outage of that factor and not as a refusal.
var errFactorDefect = errors.New("the factor gave an answer its call may not give")

// answerFault is the step a call ends in when a dependency it needs could not be
// read: an outage, and never a refusal, so that a client is not told its code was
// wrong when nothing was checked.
func (m *product) answerFault(err error) authn.Step {
	return authn.StepUnavailable(m.unavailable(err))
}

// lookup finds the factor a call is about and the implementation of its kind.
// The step it returns is the answer when ok is false. A row that is not there, one
// of another account, and one that is not active read alike, as a factor that is
// not enrolled, and none of the three reaches the factor or the back-off.
func (m *product) lookup(ctx context.Context, account authn.AccountID, factorID string, wantStatus authn.FactorStatus) (rec authn.FactorRecord, factor authn.Factor, answer authn.Step, ok bool) {
	if account == "" || factorID == "" {
		return rec, nil, authn.StepRefused(authn.ErrFactorNotEnrolled), false
	}
	rec, found, err := m.register.find(ctx, account, factorID)
	if err != nil {
		m.logger.Warn(ModuleName+": the factor register could not be read",
			slog.String("account", string(account)), slog.String("cause", err.Error()))
		return rec, nil, m.answerFault(err), false
	}
	if !found || rec.Status != wantStatus {
		return rec, nil, authn.StepRefused(authn.ErrFactorNotEnrolled), false
	}
	impl, present := m.factors[rec.Kind]
	if !present {
		m.logger.Warn(ModuleName+": an account has a factor of a kind this assembly has no implementation of",
			slog.String("account", string(account)), slog.String("factor", rec.ID))
		return rec, nil, authn.StepRefused(fmt.Errorf("%w: the kind %q has no implementation here",
			authn.ErrNoFactor, rec.Kind)), false
	}
	return rec, impl, authn.Step{}, true
}

// backoff checks the factor's back-off. When the call may not go on the step is
// its answer: a wait for a factor in back-off, and an outage when the back-off
// cannot be read, because a back-off that cannot be read must not let an attempt
// through.
func (m *product) backoff(ctx context.Context, rec authn.FactorRecord) (authn.Step, bool) {
	wait, err := m.throttle.Check(ctx, authn.ScopeFactor, rec.ID)
	if err != nil {
		m.logger.Warn(ModuleName+": the back-off of a factor could not be read",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", err.Error()))
		return m.answerFault(err), true
	}
	if wait > 0 {
		return authn.StepThrottled(wait), true
	}
	return authn.Step{}, false
}

// failed counts one failed attempt against the factor. When it is the one that
// started a back-off the event and the count follow. A failure to count is an
// Error and the attempt stays refused.
func (m *product) failed(ctx context.Context, rec authn.FactorRecord) {
	engaged, err := m.throttle.Fail(ctx, authn.ScopeFactor, rec.ID)
	if err != nil {
		m.logger.Error(ModuleName+": a failed attempt could not be counted",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", err.Error()))
		return
	}
	if !engaged {
		return
	}
	m.meters.engaged(ctx)
	m.publishEngaged(ctx, rec.Account)
	m.logger.Info(ModuleName+": a factor was put into back-off",
		slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
		slog.String("reason_class", authn.ReasonThrottled))
}

// cleared forgets the failures of a factor that has just passed.
func (m *product) cleared(ctx context.Context, rec authn.FactorRecord) {
	if err := m.throttle.Succeed(ctx, authn.ScopeFactor, rec.ID); err != nil {
		m.logger.Error(ModuleName+": the failures of a factor could not be cleared",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", err.Error()))
	}
}

// defect ends a call whose factor gave an answer the call may not have. It is
// logged at Error, which is where a defect goes, and answered as an outage of
// the factor.
func (m *product) defect(rec authn.FactorRecord, call string, got step.Kind) authn.Step {
	m.logger.Error(ModuleName+": a factor gave an answer its call may not give",
		slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
		slog.String("call", call), slog.String("answer", got.String()))
	return authn.StepUnavailable(fmt.Errorf("%w: %s from %s", errFactorDefect, got, call))
}

// Challenge starts a verification on the chosen factor: it sends a code or makes
// a passkey's options. The factor's back-off is checked first, and a factor in
// back-off is sent nothing. A challenge is no guess, so it counts nothing.
func (m *product) Challenge(ctx context.Context, f authn.FlowState, factorID string) (authn.Step, error) {
	rec, factor, answer, ok := m.lookup(ctx, f.Account, factorID, authn.FactorActive)
	if !ok {
		return answer, nil
	}
	if answer, stop := m.backoff(ctx, rec); stop {
		return answer, nil
	}
	got, err := factor.Challenge(ctx, f, rec)
	if err != nil {
		m.logger.Error(ModuleName+": a factor failed to start a verification",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", err.Error()))
		return authn.StepUnavailable(fmt.Errorf("%w: %w", errFactorDefect, err)), nil
	}
	switch kind := step.KindOf(got); kind {
	case step.KindPrompt, step.KindData, step.KindRefused, step.KindThrottled, step.KindUnavailable:
		return got, nil
	default:
		return m.defect(rec, "Challenge", kind), nil
	}
}

// passAllowed reports whether a pass is one a factor of the kind may give: it
// names a method, and every method it names is the kind's own. A pass that
// claimed another method would reach Assurance as a second factor of a kind it
// is not.
func passAllowed(kind authn.FactorKind, pass authn.Pass) bool {
	own := methodsOf(kind)
	if len(pass.Methods) == 0 {
		return false
	}
	for _, method := range pass.Methods {
		if !slices.Contains(own, method) {
			return false
		}
	}
	return true
}

// Verify checks a second factor. The factor's back-off is checked first and a
// factor in back-off is not asked; then the factor is, a failure is counted and a
// pass clears the count, and what the call came to is counted for the
// dashboards.
//
// The answer is the factor's, with nothing added: StepPassed, StepRefused,
// StepThrottled or StepUnavailable. A factor that is not enrolled, that is still
// pending or disabled, or that belongs to another account is refused before it
// is asked and before it is counted.
func (m *product) Verify(ctx context.Context, f authn.FlowState, factorID string, in authn.StepInput) (authn.Step, error) {
	rec, factor, answer, ok := m.lookup(ctx, f.Account, factorID, authn.FactorActive)
	if !ok {
		return answer, nil
	}
	if answer, stop := m.backoff(ctx, rec); stop {
		m.meters.verified(ctx, rec.Kind, outcomeOf(answer))
		return answer, nil
	}
	got, err := factor.Verify(ctx, f, rec, in)
	if err != nil {
		m.logger.Error(ModuleName+": a factor failed to verify",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", err.Error()))
		m.meters.verified(ctx, rec.Kind, verifyUnavailable)
		return authn.StepUnavailable(fmt.Errorf("%w: %w", errFactorDefect, err)), nil
	}
	switch kind := step.KindOf(got); kind {
	case step.KindPassed:
		if !passAllowed(rec.Kind, step.PassOf(got)) {
			m.meters.verified(ctx, rec.Kind, verifyUnavailable)
			return m.defect(rec, "Verify", kind), nil
		}
		m.cleared(ctx, rec)
		if err := m.register.touch(ctx, rec.ID, m.clock.Now()); err != nil {
			m.logger.Warn(ModuleName+": the use of a factor could not be recorded",
				slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
				slog.String("cause", err.Error()))
		}
		m.meters.verified(ctx, rec.Kind, verifyOK)
		return got, nil
	case step.KindRefused:
		m.failed(ctx, rec)
		outcome := verifyBad
		if errors.Is(step.CauseOf(got), authn.ErrReplay) {
			outcome = verifyReplay
			m.logger.Info(ModuleName+": a one-time value was presented again",
				slog.String("account", string(rec.Account)), slog.String("factor", rec.ID))
		}
		m.meters.verified(ctx, rec.Kind, outcome)
		return got, nil
	case step.KindThrottled, step.KindUnavailable:
		m.meters.verified(ctx, rec.Kind, outcomeOf(got))
		return got, nil
	default:
		m.meters.verified(ctx, rec.Kind, verifyUnavailable)
		return m.defect(rec, "Verify", kind), nil
	}
}

// outcomeOf is the outcome a step that is not a pass or a refusal is counted as.
func outcomeOf(s authn.Step) string {
	if step.KindOf(s) == step.KindThrottled {
		return verifyThrottled
	}
	return verifyUnavailable
}

// pendingKey is the context key under which Enroll hands the pending row to the
// factor it calls.
type pendingKey struct{}

// PendingFactor is the pending row the call to Factor.Enroll in progress is
// registering, which Enroll wrote before it called the factor and which it
// activates after the factor's last step. A factor reads it to key the data of
// its own by the row's identifier. It is the only way a factor learns the
// identifier, so a factor that does not find one has been called by something
// other than Enroll and registers nothing.
func PendingFactor(ctx context.Context) (authn.FactorRecord, bool) {
	rec, ok := ctx.Value(pendingKey{}).(authn.FactorRecord)
	return rec, ok
}

// The envelope Enroll keeps its own state in. The engine keeps the private bytes
// of a registration as they are between steps, and Enroll needs the identifier of
// the pending row on the next one, so every step that moves on carries it in
// front of the factor's own bytes: the version, the identifier's length and the
// identifier.
const envelopeVersion = 1

func wrapPrivate(id string, inner []byte) []byte {
	out := make([]byte, 0, 1+binary.MaxVarintLen64+len(id)+len(inner))
	out = append(out, envelopeVersion)
	out = binary.AppendUvarint(out, uint64(len(id)))
	out = append(out, id...)
	return append(out, inner...)
}

func unwrapPrivate(b []byte) (id string, inner []byte, ok bool) {
	if len(b) < 2 || b[0] != envelopeVersion {
		return "", nil, false
	}
	n, read := binary.Uvarint(b[1:])
	rest := b[1+max(read, 0):]
	if read <= 0 || n == 0 || n > uint64(len(rest)) {
		return "", nil, false
	}
	return string(rest[:n]), rest[n:], true
}

// withEnvelope returns the factor's answer with its private bytes wrapped, for an
// answer that moves the registration on, and the answer as it is for any other.
func withEnvelope(id string, s authn.Step) authn.Step {
	var wrapped authn.Step
	switch step.KindOf(s) {
	case step.KindPrompt:
		wrapped = authn.StepPrompt(step.NameOf(s), wrapPrivate(id, step.PrivateOf(s)), step.FieldsOf(s)...)
	case step.KindData:
		wrapped = authn.StepData(step.NameOf(s), wrapPrivate(id, step.PrivateOf(s)), step.DataOf(s))
	default:
		return s
	}
	return wrapped.WithSends(step.SendsOf(s))
}

// Enroll is the one way a factor is registered, a step at a time.
//
// The first step, which is the one whose flow state holds no private bytes,
// counts the account's factors of the kind and writes a pending row, then calls
// the factor. Every later step finds that row again through the private bytes the
// previous step left and calls the factor on it. The last step, the one the
// factor answers with StepEnrolled, makes the row active and publishes the
// change. A row that is not active is never used to verify, and a registration
// that is abandoned is swept once it has been pending past pending-ttl.
//
// A step after the first checks the factor's back-off and counts a refusal
// against it, because the confirmation step is a guess at a code.
func (m *product) Enroll(ctx context.Context, f authn.FlowState, kind authn.FactorKind, in authn.StepInput) (authn.Step, error) {
	if f.Account == "" {
		return authn.Step{}, fmt.Errorf("%w: a registration is for an account", authn.ErrInvalidArgument)
	}
	if len(f.Private) == 0 {
		return m.beginEnroll(ctx, f, kind, in)
	}
	return m.continueEnroll(ctx, f, kind, in)
}

func (m *product) beginEnroll(ctx context.Context, f authn.FlowState, kind authn.FactorKind, in authn.StepInput) (authn.Step, error) {
	factor, present := m.factors[kind]
	if !present {
		return authn.Step{}, fmt.Errorf("%w: no factor of the kind %q is taken in", authn.ErrInvalidArgument, kind)
	}
	rec, err := m.register.insertPending(ctx, f.Account, kind, m.settings.maxPerKind(), m.clock.Now())
	switch {
	case errors.Is(err, authn.ErrFactorLimit):
		m.record(ctx, actionEnroll, outcomeDenied, authn.FactorRecord{Account: f.Account, Kind: kind}, byClassSelf)
		return authn.Step{}, authn.ErrFactorLimit
	case err != nil:
		m.logger.Warn(ModuleName+": the factor register could not be written",
			slog.String("account", string(f.Account)), slog.String("cause", err.Error()))
		return m.answerFault(err), nil
	}
	got, callErr := factor.Enroll(context.WithValue(ctx, pendingKey{}, rec), f, in)
	return m.settleEnroll(ctx, rec, got, callErr, true), nil
}

func (m *product) continueEnroll(ctx context.Context, f authn.FlowState, kind authn.FactorKind, in authn.StepInput) (authn.Step, error) {
	id, inner, ok := unwrapPrivate(f.Private)
	if !ok {
		return authn.StepRefused(authn.ErrFlowInvalid), nil
	}
	rec, factor, answer, found := m.lookup(ctx, f.Account, id, authn.FactorPending)
	if !found {
		return answer, nil
	}
	if kind != "" && kind != rec.Kind {
		return authn.StepRefused(authn.ErrFlowInvalid), nil
	}
	if answer, stop := m.backoff(ctx, rec); stop {
		return answer, nil
	}
	next := f.Clone()
	next.Private = inner
	got, callErr := factor.Enroll(context.WithValue(ctx, pendingKey{}, rec), next, in)
	return m.settleEnroll(ctx, rec, got, callErr, false), nil
}

// settleEnroll turns a factor's answer to a registration step into the engine's.
// first is whether the pending row was written by this call: an answer that does
// not move the registration on leaves nothing for a later step to find, so the
// row is dropped at once and not left to the sweep.
func (m *product) settleEnroll(ctx context.Context, rec authn.FactorRecord, got authn.Step, callErr error, first bool) authn.Step {
	if callErr != nil {
		m.logger.Error(ModuleName+": a factor failed to register",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", callErr.Error()))
		if first {
			m.dropPending(ctx, rec)
		}
		return authn.StepUnavailable(fmt.Errorf("%w: %w", errFactorDefect, callErr))
	}
	switch kind := step.KindOf(got); kind {
	case step.KindPrompt, step.KindData:
		return withEnvelope(rec.ID, got)
	case step.KindEnrolled:
		return m.finishEnroll(ctx, rec, got)
	case step.KindRefused:
		if first {
			m.dropPending(ctx, rec)
		} else {
			m.failed(ctx, rec)
		}
		return got
	case step.KindThrottled, step.KindUnavailable:
		if first {
			m.dropPending(ctx, rec)
		}
		return got
	default:
		if first {
			m.dropPending(ctx, rec)
		}
		return m.defect(rec, "Enroll", kind)
	}
}

// finishEnroll is the last step: the row is made active, and only now. A row
// that is no longer pending, which a sweep, a reset or a removal made it, is not
// brought back, and the registration is refused.
func (m *product) finishEnroll(ctx context.Context, rec authn.FactorRecord, got authn.Step) authn.Step {
	at := m.clock.Now()
	won, err := m.register.activate(ctx, rec.Account, rec.ID, at)
	switch {
	case err != nil:
		m.logger.Warn(ModuleName+": the factor register could not be written",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", err.Error()))
		return m.answerFault(err)
	case !won:
		return authn.StepRefused(authn.ErrFactorNotEnrolled)
	}
	m.cleared(ctx, rec)
	rec.Status = authn.FactorActive
	rec.ActivatedAt = at
	m.publishChanged(ctx, rec, changeEnrolled, byClassSelf)
	m.record(ctx, actionEnroll, outcomeSuccess, rec, byClassSelf)
	return authn.StepEnrolled(rec).WithSends(step.SendsOf(got))
}

// dropPending removes a pending row that no later step will find, and the data
// the factor wrote for it. The row goes first and conditionally, so that a
// registration that is finishing and this removal cannot both win.
func (m *product) dropPending(ctx context.Context, rec authn.FactorRecord) {
	removed, err := m.register.removePending(ctx, rec.ID)
	if err != nil {
		m.logger.Warn(ModuleName+": a pending factor could not be removed; the sweep will",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", err.Error()))
		return
	}
	if removed {
		m.cleanKind(ctx, rec)
	}
}

// cleanKind has the factor of the row's kind remove the data it keeps for the
// row. A kind with no implementation here has no one to ask, and what it left
// stays with the module that is not taken in.
func (m *product) cleanKind(ctx context.Context, rec authn.FactorRecord) {
	factor, present := m.factors[rec.Kind]
	if !present {
		return
	}
	if err := factor.Remove(ctx, rec.Account, rec); err != nil {
		m.logger.Warn(ModuleName+": the data a factor kept for a removed row could not be removed",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", err.Error()))
	}
}
