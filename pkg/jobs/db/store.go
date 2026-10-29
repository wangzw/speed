package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/vislake/speed/pkg/jobs"
	"github.com/vislake/speed/pkg/tenant"
)

// job is the row shape of the jobs table.
//
// It embeds the tenant convention, which is both the declaration that a row
// belongs to a tenant and the column that carries it: a statement issued
// through this model is scoped to the tenant of its context, filled with it on
// insert and judged against it on write. The key leads with that column, as
// every scoped table's does.
//
// Every instant is an epoch count in milliseconds, matching the columns the
// migration declares, so a row read here and a row the loop's native
// statements touch carry the same value.
type job struct {
	tenant.Model
	ID                  string  `gorm:"column:id;primaryKey"`
	Type                string  `gorm:"column:type;not null"`
	Status              string  `gorm:"column:status;not null"`
	Priority            int     `gorm:"column:priority;not null"`
	Payload             []byte  `gorm:"column:payload"`
	Result              []byte  `gorm:"column:result"`
	Error               *string `gorm:"column:error"`
	Attempts            int     `gorm:"column:attempts;not null"`
	MaxRetries          int     `gorm:"column:max_retries;not null"`
	TimeoutMS           int64   `gorm:"column:timeout_ms;not null"`
	ProgressPct         int     `gorm:"column:progress_pct;not null"`
	ProgressMsg         string  `gorm:"column:progress_msg;not null"`
	IdempotencyKey      *string `gorm:"column:idempotency_key"`
	ScheduledAt         int64   `gorm:"column:scheduled_at;not null"`
	AvailableAt         int64   `gorm:"column:available_at;not null"`
	StartedAt           *int64  `gorm:"column:started_at"`
	CompletedAt         *int64  `gorm:"column:completed_at"`
	ClaimedBy           *string `gorm:"column:claimed_by"`
	ClaimExpiresAt      *int64  `gorm:"column:claim_expires_at"`
	CancelRequested     bool    `gorm:"column:cancel_requested;not null"`
	TerminalPublishedAt *int64  `gorm:"column:terminal_published_at"`
	CreatedAt           int64   `gorm:"column:created_at;not null"`
	UpdatedAt           int64   `gorm:"column:updated_at;not null"`
}

// TableName is the table the jobs live in.
func (job) TableName() string { return tableName }

// view builds the read-only job a caller or a handler sees out of a row.
func (r *job) view() *jobs.Job {
	out := &jobs.Job{
		ID:          jobs.JobID(r.ID),
		Type:        r.Type,
		Status:      jobs.Status(r.Status),
		Priority:    jobs.Priority(r.Priority),
		Payload:     r.Payload,
		Error:       "",
		Attempts:    r.Attempts,
		MaxRetries:  r.MaxRetries,
		Timeout:     time.Duration(r.TimeoutMS) * time.Millisecond,
		ProgressPct: r.ProgressPct,
		ProgressMsg: r.ProgressMsg,
		ScheduledAt: msTime(r.ScheduledAt),
		StartedAt:   msTimePtr(r.StartedAt),
		CompletedAt: msTimePtr(r.CompletedAt),
		CreatedAt:   msTime(r.CreatedAt),
		UpdatedAt:   msTime(r.UpdatedAt),
	}
	if r.Error != nil {
		out.Error = *r.Error
	}
	if r.Result != nil {
		out.Result = &jobs.Result{Data: r.Result}
	}
	return out
}

// msTime reads an epoch count in milliseconds as an instant.
func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// msTimePtr reads an optional epoch count as an optional instant.
func msTimePtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := msTime(*ms)
	return &t
}

// keyPtr is the value an idempotency key column takes: the key, or a null when
// the caller named none.
func keyPtr(key string) *string {
	if key == "" {
		return nil
	}
	return &key
}

// nowMilli reads the database's own clock, in milliseconds since the Unix
// epoch, the clock every due-ness judgement is made by. Every instant this
// module writes is read from it: a row whose available_at came from the host's
// clock instead would sit a millisecond or so in the future on an engine whose
// clock trails the host's, and miss the claim that follows, waiting out a poll
// for no reason. Reading it first and writing the value is enough, because the
// claim's own read happens later and so is never behind it.
func (q *queue) nowMilli(ctx context.Context) (int64, error) {
	var now int64
	if err := q.handle.WithContext(ctx).Raw("SELECT " + q.sql.now).Row().Scan(&now); err != nil {
		return 0, fmt.Errorf("reading the database's clock: %w", err)
	}
	return now, nil
}

// Enqueue writes one job and returns its identifier; see jobs.Queue.
func (q *queue) Enqueue(ctx context.Context, t jobs.Task, opts ...jobs.EnqueueOption) (jobs.JobID, error) {
	if !q.running.Load() {
		return "", fmt.Errorf("%w: the queue has not started or has stopped", jobs.ErrNotRunning)
	}
	if strings.TrimSpace(t.Type) == "" {
		return "", fmt.Errorf("%w: the task names no type", jobs.ErrInvalidTask)
	}
	if _, ok := q.handlerOf(t.Type); !ok {
		return "", fmt.Errorf("%w: no handler is registered for %q", jobs.ErrNoHandler, t.Type)
	}
	options := jobs.NewEnqueueOptions()
	for _, opt := range opts {
		opt(options)
	}
	now, err := q.nowMilli(ctx)
	if err != nil {
		return "", fmt.Errorf("%s: writing a %q job: %w", ModuleName, t.Type, err)
	}
	// A job scheduled for a later instant is due then; one with no scheduled
	// time is due as soon as the row lands, on the database's clock.
	due := now
	if !options.ScheduledAt.IsZero() {
		due = options.ScheduledAt.UnixMilli()
	}
	row := job{
		ID:              uuid.NewString(),
		Type:            t.Type,
		Status:          string(jobs.StatusPending),
		Priority:        int(options.Priority),
		Payload:         t.Payload,
		MaxRetries:      options.MaxRetries,
		TimeoutMS:       options.Timeout.Milliseconds(),
		IdempotencyKey:  keyPtr(t.IdempotencyKey),
		ScheduledAt:     due,
		AvailableAt:     due,
		CancelRequested: false,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	// Insert and let the unique index judge the idempotency key: a second
	// submission carrying a key already held writes nothing, and the row
	// already there is read back below. The tenant column is filled from the
	// context by the model's own filter, and a context that carries no tenant
	// is refused here with nothing written.
	written := q.handle.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if written.Error != nil {
		return "", fmt.Errorf("%s: writing a %q job: %w", ModuleName, t.Type, written.Error)
	}
	if written.RowsAffected > 0 {
		return jobs.JobID(row.ID), nil
	}
	if t.IdempotencyKey == "" {
		// Nothing conflicted and nothing was written, which a freshly minted
		// identifier cannot produce.
		return "", fmt.Errorf("%s: writing a %q job matched no row and reported no conflict", ModuleName, t.Type)
	}
	return q.existingByKey(ctx, t.IdempotencyKey)
}

// existingByKey reads back the job an idempotency key already belongs to,
// within the tenant of the context.
func (q *queue) existingByKey(ctx context.Context, key string) (jobs.JobID, error) {
	var held job
	err := q.handle.WithContext(ctx).Where("idempotency_key = ?", key).First(&held).Error
	if err != nil {
		return "", fmt.Errorf("%s: reading the job the idempotency key already belongs to: %w", ModuleName, err)
	}
	return jobs.JobID(held.ID), nil
}

// Get returns one job visible to the tenant of ctx; see jobs.Queue.
func (q *queue) Get(ctx context.Context, id jobs.JobID) (*jobs.Job, error) {
	if !q.running.Load() {
		return nil, fmt.Errorf("%w: the queue has not started or has stopped", jobs.ErrNotRunning)
	}
	var row job
	err := q.handle.WithContext(ctx).First(&row, "id = ?", string(id)).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		// An unknown identifier, another tenant's identifier and one that is
		// no longer visible are all answered alike: the filter reaches no row
		// and the caller learns nothing about whose it is.
		return nil, fmt.Errorf("%w: %s", jobs.ErrJobNotFound, id)
	case err != nil:
		return nil, fmt.Errorf("%s: reading the job %s: %w", ModuleName, id, err)
	}
	return row.view(), nil
}

// Cancel asks for a job to be cancelled; see jobs.Queue.
func (q *queue) Cancel(ctx context.Context, id jobs.JobID) error {
	if !q.running.Load() {
		return fmt.Errorf("%w: the queue has not started or has stopped", jobs.ErrNotRunning)
	}
	var row job
	err := q.handle.WithContext(ctx).First(&row, "id = ?", string(id)).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return fmt.Errorf("%w: %s", jobs.ErrJobNotFound, id)
	case err != nil:
		return fmt.Errorf("%s: reading the job %s: %w", ModuleName, id, err)
	}
	if jobs.Status(row.Status).Terminal() {
		// A job already in a terminal status is an idempotent no-op: there is
		// nothing left for it to leave.
		return nil
	}
	now, err := q.nowMilli(ctx)
	if err != nil {
		return fmt.Errorf("%s: cancelling the job %s: %w", ModuleName, id, err)
	}
	if row.ClaimedBy == nil {
		// An unclaimed job is moved straight to cancelled in one conditional
		// statement. A claim that landed between the read above and this
		// statement takes the row out of its reach, and the request below then
		// stands in its place.
		settled := q.handle.WithContext(ctx).Model(&job{}).
			Where("id = ? AND claimed_by IS NULL AND status IN ?", string(id),
				[]string{string(jobs.StatusPending), string(jobs.StatusRetrying)}).
			Updates(map[string]any{
				"status":       string(jobs.StatusCancelled),
				"completed_at": now,
				"updated_at":   now,
			})
		if settled.Error != nil {
			return fmt.Errorf("%s: cancelling the job %s: %w", ModuleName, id, settled.Error)
		}
		if settled.RowsAffected > 0 {
			return nil
		}
	}
	// A claimed job only has a cancellation requested: the claimant observes
	// it before it executes the job and settles it as cancelled. A row that
	// reached a terminal status in the meantime is left alone.
	requested := q.handle.WithContext(ctx).Model(&job{}).
		Where("id = ? AND claimed_by IS NOT NULL", string(id)).
		Updates(map[string]any{"cancel_requested": true, "updated_at": now})
	if requested.Error != nil {
		return fmt.Errorf("%s: recording a cancellation request for the job %s: %w", ModuleName, id, requested.Error)
	}
	return nil
}
