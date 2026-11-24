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

var errFactorDefect = errors.New("the factor gave an answer its call may not give")

func (m *product) answerFault(err error) authn.Step {
	return authn.StepUnavailable(m.unavailable(err))
}

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

func (m *product) cleared(ctx context.Context, rec authn.FactorRecord) {
	if err := m.throttle.Succeed(ctx, authn.ScopeFactor, rec.ID); err != nil {
		m.logger.Error(ModuleName+": the failures of a factor could not be cleared",
			slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
			slog.String("cause", err.Error()))
	}
}

func (m *product) defect(rec authn.FactorRecord, call string, got step.Kind) authn.Step {
	m.logger.Error(ModuleName+": a factor gave an answer its call may not give",
		slog.String("account", string(rec.Account)), slog.String("factor", rec.ID),
		slog.String("call", call), slog.String("answer", got.String()))
	return authn.StepUnavailable(fmt.Errorf("%w: %s from %s", errFactorDefect, got, call))
}

// Challenge starts a verification on the chosen factor, sending a code or making a passkey's options. It checks the factor's back-off first and sends nothing to a factor in back-off.
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

// Verify checks a second factor after checking its back-off, counts a failure and clears the count on a pass, and reports the verification. The answer is the factor's own Step; a factor that is not active, not enrolled or another account's is refused before it is asked.
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
		switch cause := step.CauseOf(got); {
		case errors.Is(cause, authn.ErrFactorFlagged):
			m.logger.Warn(ModuleName+": a factor's credential was flagged as a possible clone",
				slog.String("account", string(rec.Account)), slog.String("factor", rec.ID))
			m.flagged(ctx, rec)
		case errors.Is(cause, authn.ErrReplay):
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

func outcomeOf(s authn.Step) string {
	if step.KindOf(s) == step.KindThrottled {
		return verifyThrottled
	}
	return verifyUnavailable
}

type pendingKey struct{}

// PendingFactor returns the pending row Enroll is registering, for a factor to key its own data by its identifier. It is present only in a call made by Enroll.
func PendingFactor(ctx context.Context) (authn.FactorRecord, bool) {
	rec, ok := ctx.Value(pendingKey{}).(authn.FactorRecord)
	return rec, ok
}

// Enroll carries its own state in front of the factor's private bytes: the envelope version, the pending row identifier's length and the identifier.
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

// Enroll registers a factor one step at a time: the first step counts the kind and writes a pending row, later steps find it through the private bytes, and the last step activates it and publishes the change. A step after the first is held to the factor's back-off.
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
	label, err := normalizeFactorLabel(in.Fields[labelField])
	if err != nil {
		return authn.StepRefused(err), nil
	}
	if !factorTakesName(kind) && label != "" {
		return authn.StepRefused(fmt.Errorf("%w: a %s factor carries no name", authn.ErrInvalidArgument, kind)), nil
	}
	limit, replaces := m.kindLimit(kind)
	rec, err := m.register.insertPending(ctx, f.Account, kind, label, limit, replaces, m.clock.Now())
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

func (m *product) finishEnroll(ctx context.Context, rec authn.FactorRecord, got authn.Step) authn.Step {
	at := m.clock.Now()
	var won bool
	var err error
	if _, replaces := m.kindLimit(rec.Kind); replaces {
		won, err = m.register.activateReplacing(ctx, rec.Account, rec.ID, rec.Kind, at)
	} else {
		won, err = m.register.activate(ctx, rec.Account, rec.ID, at)
	}
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

// Abandon releases what a registration flow created when the flow is abandoned: it removes only the pending row the flow wrote, and only while it is still pending, together with the factor's own data. It is idempotent.
func (m *product) Abandon(ctx context.Context, f authn.FlowState) error {
	id, _, ok := unwrapPrivate(f.Private)
	if !ok {
		return nil
	}
	rec, found, err := m.register.find(ctx, f.Account, id)
	if err != nil {
		return fmt.Errorf("reading the factor a flow registered: %w", err)
	}
	if !found || rec.Status != authn.FactorPending {
		return nil
	}
	removed, err := m.register.removePending(ctx, id)
	if err != nil {
		return fmt.Errorf("removing the pending factor a flow registered: %w", err)
	}
	if !removed {
		return nil
	}
	m.cleanKind(ctx, rec)
	return nil
}

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
