package relay

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/event/internal/dispatch"
)

type timing struct {
	poll time.Duration
	// burst is how soon the loop reads the table after a row was staged here.
	burst time.Duration
	claim time.Duration
	// publish bounds one publish. It is shorter than claim, and a row is
	// handed to the bus only while its claim still outlasts it, so a relay
	// does not publish a row another relay may already have taken over.
	publish   time.Duration
	statement time.Duration
	// retry is the delay after a row's first refusal. It doubles with every
	// further refusal of that row, up to retryCap.
	retry, retryCap time.Duration
	// grace is how long the stop stage waits for a publish under way, and
	// cutoff how much longer it waits for the loop once that publish is
	// cancelled.
	grace, cutoff time.Duration
	batch         int
}

var defaultTiming = timing{
	poll:      time.Second,
	burst:     10 * time.Millisecond,
	claim:     2 * time.Minute,
	publish:   time.Minute,
	statement: 30 * time.Second,
	retry:     time.Second,
	retryCap:  time.Minute,
	grace:     dispatch.Grace,
	cutoff:    time.Second,
	batch:     64,
}

func (t timing) retryAfter(attempts int) time.Duration {
	delay := t.retry
	for i := 1; i < attempts && delay < t.retryCap; i++ {
		delay *= 2
	}
	return min(delay, t.retryCap)
}

type staged struct {
	id       int64
	eventID  string
	typ      string
	tenant   string
	payload  string
	attempts int
	// traceContext is the tracing context the row carried when it was staged,
	// in the text form dispatch keeps it in; the empty string for a row written
	// before the column existed.
	traceContext string
}

// lastErrorLimit bounds the refusal text a row keeps, in bytes.
const lastErrorLimit = 2000

func (r *relay) run() {
	defer close(r.done)
	defer r.releaseAll()

	timer := time.NewTimer(0)
	defer timer.Stop()
	due := time.Now()
	var burst time.Duration
	failures := 0
	for {
		select {
		case <-r.stopping:
			return
		case <-r.nudges:
			// The row is not visible until its transaction commits, so the first read
			// waits the burst rather than happening now.
			burst = r.timing.burst
			if soon := time.Now().Add(burst); soon.Before(due) {
				timer.Reset(burst)
				due = soon
			}
			continue
		case <-timer.C:
		}

		wait, err := r.pass()
		delay := r.timing.poll
		if err != nil {
			failures++
			// A table that cannot be read is read less often, so a database
			// that is down is not asked every poll and the record of it is
			// not written every poll either.
			delay = min(r.timing.poll<<min(failures-1, 6), r.timing.retryCap)
			r.logger.Error("outbox: reading the staged events failed; they stay staged", "err", err,
				"retry_in", delay)
		} else {
			failures = 0
		}
		if wait > 0 && wait < delay {
			delay = wait
		}
		if burst > 0 {
			delay = min(delay, burst)
			if burst *= 2; burst >= r.timing.poll {
				burst = 0
			}
		}
		timer.Reset(delay)
		due = time.Now().Add(delay)
	}
}

func (r *relay) pass() (time.Duration, error) {
	defer r.passes.Add(1)
	for {
		if r.isStopping() {
			return 0, nil
		}
		wait, found, err := r.nextDue()
		switch {
		case err != nil:
			return 0, err
		case !found:
			return 0, nil
		case wait > 0:
			return wait, nil
		}
		claimedAt := time.Now()
		rows, err := r.claim()
		if err != nil {
			return 0, err
		}
		if len(rows) == 0 {
			return r.timing.burst, nil
		}
		r.relayBatch(rows, claimedAt)
	}
}

func (r *relay) nextDue() (time.Duration, bool, error) {
	ctx, cancel := context.WithTimeout(r.work, r.timing.statement)
	defer cancel()
	var wait sql.NullInt64
	if err := r.handle.WithContext(ctx).Raw(r.sql.nextDue).Row().Scan(&wait); err != nil {
		return 0, false, fmt.Errorf("reading when the next staged event falls due: %w", err)
	}
	if !wait.Valid {
		return 0, false, nil
	}
	return time.Duration(wait.Int64) * time.Millisecond, true, nil
}

func (r *relay) claim() ([]staged, error) {
	ctx, cancel := context.WithTimeout(r.work, r.timing.statement)
	defer cancel()
	rows, err := r.handle.WithContext(ctx).
		Raw(r.sql.claim, r.token, r.timing.claim.Milliseconds(), r.timing.batch).Rows()
	if err != nil {
		return nil, fmt.Errorf("claiming staged events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var claimed []staged
	for rows.Next() {
		var s staged
		if err := rows.Scan(&s.id, &s.eventID, &s.typ, &s.tenant, &s.payload, &s.traceContext, &s.attempts); err != nil {
			return nil, fmt.Errorf("reading a claimed staged event: %w", err)
		}
		claimed = append(claimed, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the claimed staged events: %w", err)
	}
	// RETURNING promises no order; the rows are published in the order they
	// were staged.
	slices.SortFunc(claimed, func(a, b staged) int { return cmp.Compare(a.id, b.id) })
	return claimed, nil
}

func (r *relay) relayBatch(rows []staged, claimedAt time.Time) {
	lastStart := claimedAt.Add(r.timing.claim - r.timing.publish)
	for _, row := range rows {
		if r.isStopping() {
			return
		}
		if time.Now().After(lastStart) {
			r.releaseAll()
			return
		}
		r.relayOne(row)
	}
}

func (r *relay) relayOne(row staged) {
	err := r.publish(row)
	if err == nil {
		if rerr := r.exec(r.sql.remove, row.id); rerr != nil {
			r.logger.Error("outbox: a published event's row could not be removed; it will be published "+
				"again once its claim runs out", "event_id", row.eventID, "type", row.typ, "err", rerr)
		} else if row.attempts > 0 {
			r.logger.Info("outbox: a staged event the bus had refused was published",
				"event_id", row.eventID, "type", row.typ, "attempts", row.attempts+1)
		}
		return
	}
	if r.work.Err() != nil {
		return
	}
	attempts := row.attempts + 1
	delay := r.timing.retryAfter(attempts)
	if uerr := r.exec(r.sql.refuse, truncate(err.Error(), lastErrorLimit), delay.Milliseconds(), row.id,
		r.token); uerr != nil {
		r.logger.Error("outbox: a refused event's row could not be given back; it is published again once "+
			"its claim runs out", "event_id", row.eventID, "type", row.typ, "err", uerr)
	}
	r.logger.Warn("outbox: the bus refused a staged event; it stays staged and is published again",
		"event_id", row.eventID, "type", row.typ, "tenant_id", row.tenant, "attempts", attempts,
		"retry_in", delay, "err", err)
}

func (r *relay) publish(row staged) (err error) {
	payload, err := dispatch.Payload([]byte(row.payload))
	if err != nil {
		return fmt.Errorf("the staged payload does not decode: %w", err)
	}
	ctx, cancel := context.WithTimeout(r.work, r.timing.publish)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("the bus panicked: %v", recovered)
		}
	}()
	// The tracing context was fixed at staging, so the delivery's span descends
	// from the write's span, not from this relay's publish.
	return r.bus.Publish(ctx, event.Event{
		ID: row.eventID, Type: row.typ, TenantID: row.tenant,
		TraceContext: dispatch.DecodeTraceContext(row.traceContext), Payload: payload,
	})
}

func (r *relay) exec(statement string, args ...any) error {
	ctx, cancel := context.WithTimeout(r.work, r.timing.statement)
	defer cancel()
	return r.handle.WithContext(ctx).Exec(statement, args...).Error
}

func (r *relay) releaseAll() {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.work), r.timing.statement)
	defer cancel()
	if err := r.handle.WithContext(ctx).Exec(r.sql.release, r.token).Error; err != nil {
		r.logger.Error("outbox: the rows this relay held could not be given back; another relay takes "+
			"them once their claim runs out", "err", err)
	}
}

func (r *relay) isStopping() bool {
	select {
	case <-r.stopping:
		return true
	default:
		return false
	}
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
