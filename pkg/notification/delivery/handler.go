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

const (
	logOutcomeSent       = "sent"
	logOutcomeThrottled  = "throttled"
	logOutcomeRejected   = "rejected"
	logOutcomeUnroutable = "unroutable"
	logOutcomeUncertain  = "uncertain"
	logOutcomeFailed     = "failed"
)

func (p *pipeline) Type() string { return JobType }

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
	out := p.outbound(ctx, d)
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
	// text is fixed at send time, so the instant must be the recipient's
	// local one.
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

func (p *pipeline) localSMSFailure(ctx context.Context, row sendRow, attempt budget.ID, key string, cause error) (sendRow, error) {
	row, err := p.localFailure(ctx, row, key, cause)
	p.refundAttempt(ctx, attempt)
	return row, err
}

func (p *pipeline) localFailure(ctx context.Context, row sendRow, key string, cause error) (sendRow, error) {
	row.Status = statusFailed
	row.Error = textPtr(p.reason(ctx, key, cause.Error(), nil))
	return row, cause
}

func (p *pipeline) reportSent(ch notification.Channel, typeKey, providerID string, elapsed time.Duration) {
	p.logger.Info(ModuleName+": an external message was sent",
		slog.String("channel", string(ch)),
		slog.String("type_key", typeKey),
		slog.String("outcome", logOutcomeSent),
		slog.String("provider_id", providerID),
		slog.Int64("duration_ms", elapsed.Milliseconds()))
}

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
