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

// timing is every duration and size the relay runs by.
type timing struct {
	// poll is how often the loop reads the table when nothing told it a row
	// was staged: the longest a row staged on another replica, or given back
	// by a relay that stopped, waits for this one.
	poll time.Duration
	// burst is how soon the loop reads the table after a row was staged here.
	// The row is staged inside the caller's transaction and is visible from
	// its commit on, which nothing reports, so the loop reads again after
	// burst, twice burst, and so on until the interval reaches poll. A
	// staging brings the next read forward to burst from then and never puts
	// off one due sooner, so under a steady stream of stagings the loop
	// still reads at least every burst.
	burst time.Duration
	// claim is how long a relay holds the rows it claimed. Another relay takes
	// a row whose claim has run out, which is how the rows a replica held when
	// it died are published at all.
	claim time.Duration
	// publish bounds one publish. It is shorter than claim, and a row is
	// handed to the bus only while its claim still outlasts it, so a relay
	// does not publish a row another relay may already have taken over.
	publish time.Duration
	// statement bounds one statement of the loop's own, so a database that
	// stops answering stalls one pass rather than the relay.
	statement time.Duration
	// retry is the delay after a row's first refusal. It doubles with every
	// further refusal of that row, up to retryCap.
	retry, retryCap time.Duration
	// grace is how long the stop stage waits for a publish under way, and
	// cutoff how much longer it waits for the loop once that publish is
	// cancelled.
	grace, cutoff time.Duration
	// batch is how many rows one claim takes.
	batch int
}

// defaultTiming is what the module runs by.
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

// retryAfter is the delay after a row's attempts-th refusal.
func (t timing) retryAfter(attempts int) time.Duration {
	delay := t.retry
	for i := 1; i < attempts && delay < t.retryCap; i++ {
		delay *= 2
	}
	return min(delay, t.retryCap)
}

// staged is one row a claim took.
type staged struct {
	id       int64
	eventID  string
	typ      string
	tenant   string
	payload  string
	attempts int
}

// lastErrorLimit bounds the refusal text a row keeps, in bytes.
const lastErrorLimit = 2000

// run is the loop. It reads the table at start; after a staging here, on the
// burst schedule; when the next row falls due; and, while the table can be
// read, at least every poll. On its way out it gives back every row it still
// holds.
func (r *relay) run() {
	defer close(r.done)
	defer r.releaseAll()

	timer := time.NewTimer(0)
	defer timer.Stop()
	// due is when the timer fires, which is when the loop next reads.
	due := time.Now()
	var burst time.Duration
	failures := 0
	for {
		select {
		case <-r.stopping:
			return
		case <-r.nudges:
			// The row is not visible until its transaction commits, so the
			// first read waits the burst rather than happening now. The
			// read is brought forward and never put off: stagings closer
			// together than the burst would otherwise put it off for good.
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

// pass relays every row that is due, and reports how long until the next one
// falls due, zero when nothing more is staged or the relay is stopping.
func (r *relay) pass() (time.Duration, error) {
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
			// Every due row is being claimed by another relay at this moment,
			// or was claimed between the two statements.
			return r.timing.burst, nil
		}
		r.relayBatch(rows, claimedAt)
	}
}

// nextDue reads how long until the earliest staged row falls due, and whether
// any row is staged at all.
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

// claim takes up to one batch of due rows for this relay, in the order they
// were staged.
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
		if err := rows.Scan(&s.id, &s.eventID, &s.typ, &s.tenant, &s.payload, &s.attempts); err != nil {
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

// relayBatch publishes the rows one claim took, one at a time. It stops at the
// stop stage, and gives the rest back when their claim no longer outlasts a
// publish, so a row is never handed to the bus while another relay may be
// taking it over.
func (r *relay) relayBatch(rows []staged, claimedAt time.Time) {
	lastStart := claimedAt.Add(r.timing.claim - r.timing.publish)
	for _, row := range rows {
		if r.isStopping() {
			// The rest are given back on the way out.
			return
		}
		if time.Now().After(lastStart) {
			r.releaseAll()
			return
		}
		r.relayOne(row)
	}
}

// relayOne publishes one row, and removes it once the bus accepted its event
// or gives it back, due after a delay, when the bus refused it.
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
		// The stop stage cancelled this publish, and nothing refused it: the
		// row is given back on the way out as it was, with no refusal
		// counted against it or recorded.
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

// publish hands one row's event to the bus, within the publish bound.
//
// A bus that panics has refused the event like any other failure: the loop is
// the only thing relaying this table on this replica, and a panic let through
// would end it with every staged row left where it is and nothing saying so.
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
	return r.bus.Publish(ctx, event.Event{ID: row.eventID, Type: row.typ, TenantID: row.tenant, Payload: payload})
}

// exec runs one statement of the loop's own, within the statement bound.
func (r *relay) exec(statement string, args ...any) error {
	ctx, cancel := context.WithTimeout(r.work, r.timing.statement)
	defer cancel()
	return r.handle.WithContext(ctx).Exec(statement, args...).Error
}

// releaseAll gives back every row this relay still holds, due at once. It
// runs on a context of its own, since at the stop stage the loop's may already
// be cancelled, and a row it cannot give back is taken over once its claim runs
// out.
func (r *relay) releaseAll() {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.work), r.timing.statement)
	defer cancel()
	if err := r.handle.WithContext(ctx).Exec(r.sql.release, r.token).Error; err != nil {
		r.logger.Error("outbox: the rows this relay held could not be given back; another relay takes "+
			"them once their claim runs out", "err", err)
	}
}

// isStopping reports whether the stop stage has begun.
func (r *relay) isStopping() bool {
	select {
	case <-r.stopping:
		return true
	default:
		return false
	}
}

// truncate cuts s to at most limit bytes, on a character boundary.
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
