package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/mail"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/sms/budget"
	"github.com/vislake/speed/pkg/tenant"
)

// The outcomes an external send is logged under. The set is closed: a call that
// reached a carrier ends as exactly one of these, whichever value the ledger row
// then takes. A carrier's throttled, refused, unroutable and uncertain answers
// keep their own name; an error outside the sentinels is a failed send.
const (
	logOutcomeSent       = "sent"
	logOutcomeThrottled  = "throttled"
	logOutcomeRejected   = "rejected"
	logOutcomeUnroutable = "unroutable"
	logOutcomeUncertain  = "uncertain"
	logOutcomeFailed     = "failed"
)

// Type reports the job type this handler executes; see jobs.Handler.
func (p *pipeline) Type() string { return JobType }

// Handle consumes one delivery job; see jobs.Handler.
//
// The job was claimed by jobs.db, which entered its tenant before handing it
// over, so the context already carries the tenant the ledger rows belong to. The
// handler decodes the payload, runs the submission seam's validation again — a
// worker can meet a payload the submitter never wrote, or a row that has since
// been changed — holds a carried destination against the recipient's identifiers
// once more, resolves the recipient's effective channels, and delivers each of
// them. The destination is repeated here rather than left to the submitter: it
// is the one field a send cannot survive being wrong, and the account store is
// already a hard dependency, so the worker refuses a payload whose destination
// the recipient does not own before a channel is walked. A channel that fails
// does not starve the others and is not hidden by their successes: every channel
// is walked, and their errors are joined into what the handler returns, which is
// what makes the job retry.
func (p *pipeline) Handle(ctx context.Context, job *jobs.Job, _ jobs.ProgressFn) (jobs.Result, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return jobs.Result{}, fmt.Errorf("%s: %w", ModuleName, tenant.ErrNoTenantInContext)
	}
	d, attempt, err := decodeDispatch(job.Payload)
	if err != nil {
		return jobs.Result{}, fmt.Errorf("%s: %w", ModuleName, err)
	}
	spec, err := p.validate(d)
	if err != nil {
		return jobs.Result{}, err
	}
	if err = p.checkDestination(ctx, d); err != nil {
		return jobs.Result{}, err
	}
	channels, err := p.prefs.Effective(ctx, d.Recipient.UserID, d.TypeKey)
	if err != nil {
		return jobs.Result{}, fmt.Errorf("%s: resolving the effective channels of %q: %w", ModuleName, d.TypeKey, err)
	}
	// The text a channel renders is the outbound copy: a tenant-naming
	// parameter is the name there, while the idempotency key is derived from
	// the dispatch as submitted.
	out := p.outbound(ctx, d)
	// A charged sms attempt is refunded by the sms channel itself when the
	// message certainly did not leave the process. A recipient whose effective
	// channels no longer name sms keeps the charge: the charge was made against
	// the matrix the submission read, and the row then counts conservatively.
	var failures []error
	for _, ch := range channels {
		if err := p.deliver(ctx, id, d, out, spec, ch, attempt); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return jobs.Result{}, fmt.Errorf("%s: delivering %q: %w", ModuleName, d.TypeKey, errors.Join(failures...))
	}
	return jobs.Result{}, nil
}

// deliver settles one channel. It derives the channel's idempotency key from
// the dispatch as submitted, reads the ledger and skips a channel that is
// already settled — one that sent, or one whose seam was absent — then sends
// the outbound copy and records the result. A channel that failed before is
// attempted again, which is what a retry of the job is for. The charged sms
// attempt rides along so the sms channel can refund it when the message
// certainly did not leave the process.
func (p *pipeline) deliver(ctx context.Context, id tenant.ID, keyed, out notification.Dispatch, spec notification.TypeSpec, ch notification.Channel, attempt budget.ID) error {
	key, err := channelKey(id, keyed, ch)
	if err != nil {
		return fmt.Errorf("%s: deriving the idempotency key of the %s channel: %w", ModuleName, ch, err)
	}
	held, found, err := p.ledger.lookup(ctx, key)
	if err != nil {
		return err
	}
	if found && held != statusFailed {
		// A settled channel is not attempted again: a success is final, and a
		// skipped channel stays skipped because the seam this assembly did not
		// deliver is not delivered by a retry either.
		return nil
	}
	started := time.Now()
	row, sendErr := p.send(ctx, out, spec, ch, key, attempt)
	row.DurationMS = time.Since(started).Milliseconds()
	row.UpdatedAt = nowMS()
	if err := p.ledger.record(ctx, row); err != nil {
		return err
	}
	return sendErr
}

// send runs one channel's attempt and builds its ledger row. The row carries the
// channel's terminal status; the error is what the handler joins so the job
// retries, and it is nil for a success and for a skip.
func (p *pipeline) send(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec, ch notification.Channel, key string, attempt budget.ID) (sendRow, error) {
	now := nowMS()
	row := sendRow{
		ID:              uuid.NewString(),
		TypeKey:         d.TypeKey,
		Channel:         string(ch),
		RecipientClass:  string(d.Recipient.Class),
		RecipientUserID: d.Recipient.UserID,
		IdempotencyKey:  key,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	switch ch {
	case notification.ChannelInApp:
		return p.sendInApp(ctx, d, spec, row)
	case notification.ChannelEmail:
		return p.sendEmail(ctx, d, spec, row)
	case notification.ChannelSMS:
		return p.sendSMS(ctx, d, spec, row, attempt)
	default:
		row.Status = statusFailed
		row.Error = textPtr(fmt.Sprintf("the channel %q is not one this module sends on", ch))
		return row, fmt.Errorf("%w: %q", notification.ErrUnknownChannel, ch)
	}
}

// sendInApp renders the message and writes it to the inbox, then records the
// channel as settled. The row is the inbox's to announce: this module publishes
// no event of its own — the created event a subscriber pushes to a connected
// client is published by the module that wrote the row, after that row
// committed.
func (p *pipeline) sendInApp(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec, row sendRow) (sendRow, error) {
	msg := p.message(ctx, d, spec)
	if _, err := p.inbox.Write(ctx, d.Recipient.UserID, msg, row.IdempotencyKey); err != nil {
		row.Status = statusFailed
		row.Error = textPtr(err.Error())
		return row, fmt.Errorf("%s: writing the in-app message of %q: %w", ModuleName, d.TypeKey, err)
	}
	row.Status = statusSucceeded
	return row, nil
}

// sendEmail sends one email when the mail seam is present. A missing seam skips
// the channel; a dispatch that names no destination is this module's own failure,
// recorded with this module's own reason and never reaching the carrier.
//
// A receipt is acceptance, not delivery: the carrier took the message and named
// it, and the ledger's succeeded says that much and no more. Whether the
// recipient received it is the carrier's to report, and this module follows no
// such report.
//
// Every error a carrier answers with is one terminal failure of the channel: the
// mail sentinels tell a throttled call, a refusal and an uncertain outcome apart,
// and all three are the same thing here — this module retries none of them
// itself, and the retry is the job's.
func (p *pipeline) sendEmail(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec, row sendRow) (sendRow, error) {
	if !p.seams.emailReady() {
		row.Status = statusSkipped
		return row, nil
	}
	to, err := destination(d.Params)
	if err != nil {
		return p.localFailure(ctx, row, "notification.delivery.email-destination", err)
	}
	// The recipient's zone is read once, before anything is rendered: email
	// text is fixed at send time, so the instant must be the recipient's local
	// one. A failed read is not "no zone stored" — rendering in UTC would freeze
	// the wrong instant into a mail the recipient cannot re-render — so the
	// channel fails and the job retries. The email channel appears at most once
	// in an effective set, and a settled channel is skipped before this point.
	zone, _, err := p.resolver.TimeZoneFor(ctx, d.Recipient.UserID)
	if err != nil {
		row.Status = statusFailed
		row.Error = textPtr(err.Error())
		// The line names the type and the cause, never the recipient. It is what
		// tells an operator that a persistent read failure is holding mail back.
		p.logger.Error(ModuleName+": the recipient's time zone could not be read; the email was not rendered",
			"type_key", d.TypeKey, "cause", err.Error())
		return row, fmt.Errorf("%s: reading the recipient's time zone for the email of %q: %w", ModuleName, d.TypeKey, err)
	}
	msg := p.emailMessage(ctx, d, spec, to, row.IdempotencyKey, zone)
	started := time.Now()
	receipt, err := p.seams.mailer.Send(ctx, msg)
	elapsed := time.Since(started)
	if err != nil {
		row.Status = statusFailed
		row.Error = textPtr(err.Error())
		p.reportSendFailure(notification.ChannelEmail, d.TypeKey, err, elapsed)
		return row, fmt.Errorf("%s: sending the email of %q: %w", ModuleName, d.TypeKey, err)
	}
	row.Status = statusSucceeded
	row.ProviderReceiptID = textPtr(receipt.ProviderID)
	p.reportSent(notification.ChannelEmail, d.TypeKey, receipt.ProviderID, elapsed)
	return row, nil
}

// sendSMS sends one sms message when both sms seams are present. A missing seam
// skips the channel; a parameter that cannot be stringified, a missing
// destination and a type that declares no template are this module's own
// failures, recorded with this module's own reason and never reaching the
// vendor. Every path that certainly leaves the message unsent refunds the
// charged attempt, so the send budget is given back while the attempt budget
// keeps counting it: a skipped channel, a local refusal and a carrier's throttle,
// rejection or unroutable answer. An uncertain outcome and an error outside the
// carrier's sentinels keep the budget, because the message may have gone out.
//
// A receipt is acceptance, not delivery: a carrier whose receipts are
// asynchronous answers a send with the vendor's message identifier alone, so the
// ledger's succeeded says the vendor took the message, not that the recipient
// received it. The final state arrives through the vendor's own channel, which
// this module does not follow.
func (p *pipeline) sendSMS(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec, row sendRow, attempt budget.ID) (sendRow, error) {
	if !p.seams.smsReady() {
		row.Status = statusSkipped
		p.refundAttempt(ctx, attempt)
		return row, nil
	}
	template, err := p.smsTemplate(ctx, d, spec)
	if err != nil {
		return p.localSMSFailure(ctx, row, attempt, "notification.delivery.sms-template", err)
	}
	params, err := stringifyParams(d.Params)
	if err != nil {
		return p.localSMSFailure(ctx, row, attempt, "notification.delivery.sms-params", err)
	}
	to, err := destination(d.Params)
	if err != nil {
		return p.localSMSFailure(ctx, row, attempt, "notification.delivery.sms-destination", err)
	}
	resolved, err := p.seams.registry.Resolve(sms.Message{
		To:       to,
		Template: template,
		Params:   params,
		Locale:   d.Locale,
		DedupeID: row.IdempotencyKey,
	})
	if err != nil {
		row.Status = statusFailed
		row.Error = textPtr(err.Error())
		p.refundAttempt(ctx, attempt)
		return row, fmt.Errorf("%s: resolving the sms template %q: %w", ModuleName, template, err)
	}
	started := time.Now()
	receipt, err := p.seams.sender.Send(ctx, resolved)
	elapsed := time.Since(started)
	if err != nil {
		row.Status = statusFailed
		row.Error = textPtr(err.Error())
		if definitiveFailure(err) {
			p.refundAttempt(ctx, attempt)
		}
		p.reportSendFailure(notification.ChannelSMS, d.TypeKey, err, elapsed)
		return row, fmt.Errorf("%s: sending the sms message of %q: %w", ModuleName, d.TypeKey, err)
	}
	row.Status = statusSucceeded
	row.ProviderReceiptID = textPtr(receipt.ProviderID)
	p.reportSent(notification.ChannelSMS, d.TypeKey, receipt.ProviderID, elapsed)
	return row, nil
}

// localSMSFailure records one of this module's own refusals as a failed sms
// channel and refunds the charged attempt: nothing reached the carrier, so the
// send budget is given back. The reason written into the ledger is rendered from
// this module's own catalog, so the producer of the failure renders its text;
// the returned error is the cause itself, which is what the handler joins.
func (p *pipeline) localSMSFailure(ctx context.Context, row sendRow, attempt budget.ID, key string, cause error) (sendRow, error) {
	row, err := p.localFailure(ctx, row, key, cause)
	p.refundAttempt(ctx, attempt)
	return row, err
}

// localFailure records one of this module's own refusals as a failed channel.
// The reason written into the ledger is rendered from this module's own catalog,
// so the producer of the failure renders its text; the returned error is the
// cause itself, which is what the handler joins.
func (p *pipeline) localFailure(ctx context.Context, row sendRow, key string, cause error) (sendRow, error) {
	row.Status = statusFailed
	row.Error = textPtr(p.reason(ctx, key, cause.Error(), nil))
	return row, cause
}

// reportSent writes the line for an external send a carrier accepted. The line
// goes to the module's own logger (see Module), never to a logger taken from the
// handler's context: the job runs on a context the queue created, which carries
// no injected logger, so FromContext would land these records on the process
// default chain instead of the configured destinations. The line carries the
// channel, the declared type, the outcome, the carrier's own identifier and the
// duration of the call; the recipient, the message text and every parameter
// value stay out of it.
func (p *pipeline) reportSent(ch notification.Channel, typeKey, providerID string, elapsed time.Duration) {
	p.logger.Info(ModuleName+": an external message was sent",
		slog.String("channel", string(ch)),
		slog.String("type_key", typeKey),
		slog.String("outcome", logOutcomeSent),
		slog.String("provider_id", providerID),
		slog.Int64("duration_ms", elapsed.Milliseconds()))
}

// reportSendFailure writes the line for an external send a carrier did not
// accept. The line carries the channel, the declared type, the outcome, the
// cause's own text and the duration of the call; the recipient, the message text
// and every parameter value stay out of it. A classified carrier answer is the
// carrier working as declared and is written at Warn; an error outside the
// sentinels is a defect of the carrier and is written at Error.
func (p *pipeline) reportSendFailure(ch notification.Channel, typeKey string, err error, elapsed time.Duration) {
	outcome := sendFailureOutcome(err)
	attrs := []any{
		slog.String("channel", string(ch)),
		slog.String("type_key", typeKey),
		slog.String("outcome", outcome),
		slog.String("cause", err.Error()),
		slog.Int64("duration_ms", elapsed.Milliseconds()),
	}
	if outcome == logOutcomeFailed {
		p.logger.Error(ModuleName+": an external message was not sent", attrs...)
		return
	}
	p.logger.Warn(ModuleName+": an external message was not sent", attrs...)
}

// sendFailureOutcome maps a carrier's error onto the closed set an external-send
// line reports. The sentinels both carrier packages classify into keep their own
// name; an error outside the sentinels is a failed send. A ledger row is still
// one `failed` for all of them — the distinction is the log line's, which an
// operator reads to see why.
func sendFailureOutcome(err error) string {
	switch {
	case errors.Is(err, mail.ErrThrottled), errors.Is(err, sms.ErrThrottled):
		return logOutcomeThrottled
	case errors.Is(err, mail.ErrRejected), errors.Is(err, sms.ErrRejected):
		return logOutcomeRejected
	case errors.Is(err, sms.ErrUnroutable):
		return logOutcomeUnroutable
	case errors.Is(err, mail.ErrUncertain), errors.Is(err, sms.ErrUncertain):
		return logOutcomeUncertain
	default:
		return logOutcomeFailed
	}
}
