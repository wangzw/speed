package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/notification"
	"github.com/vislake/speed/pkg/sms"
	"github.com/vislake/speed/pkg/tenant"
)

// Type reports the job type this handler executes; see jobs.Handler.
func (p *pipeline) Type() string { return JobType }

// Handle consumes one delivery job; see jobs.Handler.
//
// The job was claimed by jobs.db, which entered its tenant before handing it
// over, so the context already carries the tenant the ledger rows belong to. The
// handler decodes the payload, runs the submission seam's validation again — a
// worker can meet a payload the submitter never wrote, or a row that has since
// been changed — resolves the recipient's effective channels, and delivers each
// of them. A channel that fails does not starve the others and is not hidden by
// their successes: every channel is walked, and their errors are joined into
// what the handler returns, which is what makes the job retry.
func (p *pipeline) Handle(ctx context.Context, job *jobs.Job, _ jobs.ProgressFn) (jobs.Result, error) {
	id, ok := tenant.FromContext(ctx)
	if !ok {
		return jobs.Result{}, fmt.Errorf("%s: %w", ModuleName, tenant.ErrNoTenantInContext)
	}
	d, err := decodeDispatch(job.Payload)
	if err != nil {
		return jobs.Result{}, fmt.Errorf("%s: %w", ModuleName, err)
	}
	spec, err := p.validate(d)
	if err != nil {
		return jobs.Result{}, err
	}
	channels, err := p.prefs.Effective(ctx, d.Recipient.UserID, d.TypeKey)
	if err != nil {
		return jobs.Result{}, fmt.Errorf("%s: resolving the effective channels of %q: %w", ModuleName, d.TypeKey, err)
	}
	var failures []error
	for _, ch := range channels {
		if err := p.deliver(ctx, id, d, spec, ch); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return jobs.Result{}, fmt.Errorf("%s: delivering %q: %w", ModuleName, d.TypeKey, errors.Join(failures...))
	}
	return jobs.Result{}, nil
}

// deliver settles one channel. It derives the channel's idempotency key, reads
// the ledger and skips a channel that is already settled — one that sent, or one
// whose seam was absent — then sends and records the result. A channel that
// failed before is attempted again, which is what a retry of the job is for.
func (p *pipeline) deliver(ctx context.Context, id tenant.ID, d notification.Dispatch, spec notification.TypeSpec, ch notification.Channel) error {
	key, err := channelKey(id, d, ch)
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
	row, sendErr := p.send(ctx, d, spec, ch, key)
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
func (p *pipeline) send(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec, ch notification.Channel, key string) (sendRow, error) {
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
		// The mail capability is not in the tree this release, so this module
		// references no mail seam and the email channel is always unavailable —
		// the same shape as an assembly that simply did not deliver one.
		row.Status = statusSkipped
		return row, nil
	case notification.ChannelSMS:
		return p.sendSMS(ctx, d, spec, row)
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

// sendSMS sends one sms message when both sms seams are present. A missing seam
// skips the channel; a parameter that cannot be stringified, a missing
// destination and a type that declares no template are this module's own
// failures, recorded with this module's own reason and never reaching the
// vendor.
func (p *pipeline) sendSMS(ctx context.Context, d notification.Dispatch, spec notification.TypeSpec, row sendRow) (sendRow, error) {
	if !p.seams.smsReady() {
		row.Status = statusSkipped
		return row, nil
	}
	template, err := p.smsTemplate(ctx, d, spec)
	if err != nil {
		return p.localFailure(ctx, row, "notification.delivery.sms-template", err)
	}
	params, err := stringifyParams(d.Params)
	if err != nil {
		return p.localFailure(ctx, row, "notification.delivery.sms-params", err)
	}
	to, err := destination(d.Params)
	if err != nil {
		return p.localFailure(ctx, row, "notification.delivery.sms-destination", err)
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
		return row, fmt.Errorf("%s: resolving the sms template %q: %w", ModuleName, template, err)
	}
	receipt, err := p.seams.sender.Send(ctx, resolved)
	if err != nil {
		row.Status = statusFailed
		row.Error = textPtr(err.Error())
		return row, fmt.Errorf("%s: sending the sms message of %q: %w", ModuleName, d.TypeKey, err)
	}
	row.Status = statusSucceeded
	row.ProviderReceiptID = textPtr(receipt.ProviderID)
	return row, nil
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
