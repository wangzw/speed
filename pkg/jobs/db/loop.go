package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/vislake/speed/pkg/event"
	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/tenant"
)

const (
	statementTimeout = 30 * time.Second
	publishTimeout   = 30 * time.Second
	cutoff           = 5 * time.Second
	errorLimit       = 2000
)

type worker struct {
	token    string
	pivot    string
	failures int
}

type claimed struct {
	ID          string
	Tenant      string
	Type        string
	Priority    int
	Payload     []byte
	Attempts    int
	MaxRetries  int
	TimeoutMS   int64
	ScheduledAt int64
	StartedAt   *int64
	CreatedAt   int64
	UpdatedAt   int64
	ProgressPct int
	ProgressMsg string
}

type published struct {
	ID     string `gorm:"column:id"`
	Tenant string `gorm:"column:tenant_id"`
	Type   string `gorm:"column:type"`
	Status string `gorm:"column:status"`
}

func (c claimed) view() *jobs.Job {
	return &jobs.Job{
		ID:          jobs.JobID(c.ID),
		Type:        c.Type,
		Status:      jobs.StatusRunning,
		Priority:    jobs.Priority(c.Priority),
		Payload:     c.Payload,
		Attempts:    c.Attempts,
		MaxRetries:  c.MaxRetries,
		Timeout:     time.Duration(c.TimeoutMS) * time.Millisecond,
		ProgressPct: c.ProgressPct,
		ProgressMsg: c.ProgressMsg,
		ScheduledAt: msTime(c.ScheduledAt),
		StartedAt:   msTimePtr(c.StartedAt),
		CreatedAt:   msTime(c.CreatedAt),
		UpdatedAt:   msTime(c.UpdatedAt),
	}
}

func (q *queue) runWorker(w *worker) {
	defer q.release(w.token)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-q.stopping:
			return
		case <-timer.C:
		}
		delay, failed := q.pass(w)
		if failed {
			w.failures++
			delay = min(q.tuned.poll(q.logger)<<min(w.failures-1, 6), q.tuned.retryMax(q.logger))
		} else {
			w.failures = 0
		}
		timer.Reset(delay)
	}
}

func (q *queue) pass(w *worker) (time.Duration, bool) {
	poll := q.tuned.poll(q.logger)
	q.republish(w)
	for {
		if q.isStopping() {
			return 0, false
		}
		wait, staged, err := q.dueNext()
		if err != nil {
			q.logger.Error("jobs.db: reading when the next job falls due failed; the rows are left where they are",
				"error", err)
			return poll, true
		}
		switch {
		case !staged:
			return poll, false
		case wait > 0:
			return min(wait, poll), false
		}
		rows, err := q.claim(w)
		if err != nil {
			q.logger.Error("jobs.db: claiming the due jobs failed; the rows are left where they are", "error", err)
			return poll, true
		}
		if len(rows) == 0 {
			return poll, false
		}
		q.runBatch(w, rows)
	}
}

func (q *queue) dueNext() (time.Duration, bool, error) {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	var wait sql.NullInt64
	if err := q.handle.WithContext(ctx).Raw(q.sql.dueNext).Row().Scan(&wait); err != nil {
		return 0, false, fmt.Errorf("reading when the next job falls due: %w", err)
	}
	if !wait.Valid {
		return 0, false, nil
	}
	return time.Duration(wait.Int64) * time.Millisecond, true, nil
}

func (q *queue) claim(w *worker) ([]claimed, error) {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	lease := q.tuned.lease(q.logger).Milliseconds()
	rows, err := q.handle.WithContext(ctx).
		Raw(q.sql.claim, w.token, lease, lease, w.pivot, q.tuned.batch(q.logger)).Rows()
	if err != nil {
		return nil, fmt.Errorf("claiming the due jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var taken []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(&c.ID, &c.Tenant, &c.Type, &c.Priority, &c.Payload, &c.Attempts,
			&c.MaxRetries, &c.TimeoutMS, &c.ScheduledAt, &c.StartedAt, &c.CreatedAt, &c.UpdatedAt,
			&c.ProgressPct, &c.ProgressMsg); err != nil {
			return nil, fmt.Errorf("reading a claimed job: %w", err)
		}
		taken = append(taken, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the claimed jobs: %w", err)
	}
	if n := len(taken); n > 0 {
		w.pivot = taken[n-1].Tenant
	}
	return taken, nil
}

func (q *queue) runBatch(w *worker, rows []claimed) {
	for _, row := range rows {
		if q.isStopping() {
			return
		}
		q.execute(w, row)
	}
}

func (q *queue) execute(w *worker, row claimed) {
	cancelRequested, held, err := q.cancelCheck(w, row.ID)
	if err != nil {
		q.logger.Error("jobs.db: reading whether a claimed job was cancelled failed; the row stays claimed "+
			"until its claim runs out", "job_id", row.ID, "error", err)
		return
	}
	if !held {
		return
	}
	if cancelRequested {
		q.settleCancelled(w, row)
		return
	}
	handler, ok := q.handlerOf(row.Type)
	if !ok {
		q.settleFailure(w, row, fmt.Errorf("%w: no handler is registered for %q", jobs.ErrNoHandler, row.Type))
		return
	}
	entered, err := q.catalog.Enter(q.work, tenant.ID(row.Tenant))
	if err != nil {
		q.settleFailure(w, row, err)
		return
	}
	job := row.view()
	ctx, cancel := context.WithTimeout(entered, job.Timeout)
	defer cancel()
	result, err := handler.Handle(ctx, job, func(pct int, msg string) {
		job.ProgressPct, job.ProgressMsg = pct, msg
		q.recordProgress(w, row.ID, pct, msg)
	})
	if expired := ctx.Err(); expired != nil {
		cause := fmt.Errorf("the execution ran past the job's timeout of %s and did not return: %w",
			job.Timeout, expired)
		if !errors.Is(expired, context.DeadlineExceeded) {
			cause = fmt.Errorf("the execution was cancelled before the handler returned: %w", expired)
		}
		q.settleFailure(w, row, cause)
		return
	}
	if err != nil {
		q.settleFailure(w, row, err)
		return
	}
	q.settleSucceeded(w, row, result)
}

func (q *queue) cancelCheck(w *worker, id string) (bool, bool, error) {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	var requested bool
	err := q.handle.WithContext(ctx).Raw(q.sql.cancelCheck, id, w.token).Row().Scan(&requested)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("reading the cancellation request: %w", err)
	}
	return requested, true, nil
}

func (q *queue) recordProgress(w *worker, id string, pct int, msg string) {
	if _, err := q.exec(q.sql.progress, pct, msg, id, w.token); err != nil {
		q.logger.Warn("jobs.db: recording a job's progress failed; the job runs on", "job_id", id, "error", err)
	}
}

func (q *queue) settleSucceeded(w *worker, row claimed, result jobs.Result) {
	attempts := row.Attempts + 1
	if err := q.settle(q.sql.succeed, result.Data, attempts, row.ID, w.token); err != nil {
		q.settleMissed("succeeded", row.ID, err)
		return
	}
	q.publishTerminal(row, jobs.StatusSucceeded)
}

func (q *queue) settleCancelled(w *worker, row claimed) {
	if err := q.settle(q.sql.cancelled, row.ID, w.token); err != nil {
		q.settleMissed("cancelled", row.ID, err)
		return
	}
	q.publishTerminal(row, jobs.StatusCancelled)
}

func (q *queue) settleFailure(w *worker, row claimed, cause error) {
	attempts := row.Attempts + 1
	text := truncate(cause.Error(), errorLimit)
	if attempts > row.MaxRetries {
		if err := q.settle(q.sql.deadLetter, text, attempts, row.ID, w.token); err != nil {
			q.settleMissed("dead_letter", row.ID, err)
			return
		}
		q.logger.Warn("jobs.db: a job failed past its retry limit and went to dead_letter",
			"job_id", row.ID, "type", row.Type, "attempts", attempts, "error", text)
		q.publishTerminal(row, jobs.StatusDeadLetter)
		return
	}
	backoff := retryAfter(q.tuned.retryBase(q.logger), q.tuned.retryMax(q.logger), attempts)
	if err := q.settle(q.sql.fail, text, attempts, backoff.Milliseconds(), row.ID, w.token); err != nil {
		q.settleMissed("retrying", row.ID, err)
		return
	}
	q.logger.Warn("jobs.db: a job failed and is retried after its backoff", "job_id", row.ID, "type", row.Type,
		"attempts", attempts, "max_retries", row.MaxRetries, "retry_in", backoff, "error", text)
}

func (q *queue) settleMissed(what, id string, err error) {
	if errors.Is(err, errClaimLost) {
		q.logger.Warn("jobs.db: the row was taken over before this executor settled it, so this execution's "+
			"outcome is dropped and the executor holding it writes the row", "job_id", id, "settle", what)
		return
	}
	q.logger.Error("jobs.db: settling a job failed; it is taken over once its claim runs out",
		"job_id", id, "settle", what, "error", err)
}

func (q *queue) settle(statement string, args ...any) error {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	res := q.handle.WithContext(ctx).Exec(statement, args...)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errClaimLost
	}
	return nil
}

func (q *queue) exec(statement string, args ...any) (int64, error) {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	res := q.handle.WithContext(ctx).Exec(statement, args...)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

var errClaimLost = errors.New("jobs.db: the row is no longer held by this executor")

func (q *queue) release(token string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(q.work), statementTimeout)
	defer cancel()
	if err := q.handle.WithContext(ctx).Exec(q.sql.release, token).Error; err != nil {
		q.logger.Error("jobs.db: giving back the rows this executor held failed; they are taken over once "+
			"their claim runs out", "error", err)
	}
}

func (q *queue) republish(w *worker) {
	if q.bus == nil {
		return
	}
	rows, err := q.owing()
	if err != nil {
		q.logger.Error("jobs.db: reading the jobs whose terminal event is unpublished failed", "error", err)
		return
	}
	for _, row := range rows {
		if q.isStopping() {
			return
		}
		q.publishFrom(row.ID, row.Tenant, row.Type, jobs.Status(row.Status))
	}
}

func (q *queue) owing() ([]published, error) {
	ctx, cancel := context.WithTimeout(q.work, statementTimeout)
	defer cancel()
	var rows []published
	err := q.handle.WithContext(ctx).Raw(q.sql.pendingPublish, q.tuned.batch(q.logger)).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("reading the jobs owing a terminal event: %w", err)
	}
	return rows, nil
}

func (q *queue) publishTerminal(row claimed, status jobs.Status) {
	if q.bus == nil {
		return
	}
	q.publishFrom(row.ID, row.Tenant, row.Type, status)
}

func (q *queue) publishFrom(id, tenant, typ string, status jobs.Status) {
	evt := event.Event{
		ID:       id,
		Type:     terminalEventType(status),
		TenantID: tenant,
		Payload:  Terminal{ID: jobs.JobID(id), Type: typ, Status: status},
	}
	ctx, cancel := context.WithTimeout(q.work, publishTimeout)
	defer cancel()
	if err := q.bus.Publish(ctx, evt); err != nil {
		q.logger.Error("jobs.db: publishing a job's terminal event failed; the job keeps its terminal status "+
			"and the event is published again", "job_id", id, "event", evt.Type, "error", err)
		return
	}
	if _, err := q.exec(q.sql.markPublished, id); err != nil {
		q.logger.Error("jobs.db: recording that a terminal event was published failed; it is published "+
			"again", "job_id", id, "error", err)
	}
}

func terminalEventType(status jobs.Status) string {
	switch status {
	case jobs.StatusSucceeded:
		return SucceededEventType
	case jobs.StatusDeadLetter:
		return DeadLetterEventType
	case jobs.StatusCancelled:
		return CancelledEventType
	default:
		return ""
	}
}

func (q *queue) isStopping() bool {
	select {
	case <-q.stopping:
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
