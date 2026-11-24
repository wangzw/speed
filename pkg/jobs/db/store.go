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

func (job) TableName() string { return tableName }

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

func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

func msTimePtr(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := msTime(*ms)
	return &t
}

func keyPtr(key string) *string {
	if key == "" {
		return nil
	}
	return &key
}

func (q *queue) nowMilli(ctx context.Context) (int64, error) {
	var now int64
	if err := q.handle.WithContext(ctx).Raw("SELECT " + q.sql.now).Row().Scan(&now); err != nil {
		return 0, fmt.Errorf("reading the database's clock: %w", err)
	}
	return now, nil
}

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
	written := q.handle.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if written.Error != nil {
		return "", fmt.Errorf("%s: writing a %q job: %w", ModuleName, t.Type, written.Error)
	}
	if written.RowsAffected > 0 {
		return jobs.JobID(row.ID), nil
	}
	if t.IdempotencyKey == "" {
		return "", fmt.Errorf("%s: writing a %q job matched no row and reported no conflict", ModuleName, t.Type)
	}
	return q.existingByKey(ctx, t.IdempotencyKey)
}

func (q *queue) existingByKey(ctx context.Context, key string) (jobs.JobID, error) {
	var held job
	err := q.handle.WithContext(ctx).Where("idempotency_key = ?", key).First(&held).Error
	if err != nil {
		return "", fmt.Errorf("%s: reading the job the idempotency key already belongs to: %w", ModuleName, err)
	}
	return jobs.JobID(held.ID), nil
}

func (q *queue) Get(ctx context.Context, id jobs.JobID) (*jobs.Job, error) {
	if !q.running.Load() {
		return nil, fmt.Errorf("%w: the queue has not started or has stopped", jobs.ErrNotRunning)
	}
	var row job
	err := q.handle.WithContext(ctx).First(&row, "id = ?", string(id)).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, fmt.Errorf("%w: %s", jobs.ErrJobNotFound, id)
	case err != nil:
		return nil, fmt.Errorf("%s: reading the job %s: %w", ModuleName, id, err)
	}
	return row.view(), nil
}

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
		return nil
	}
	now, err := q.nowMilli(ctx)
	if err != nil {
		return fmt.Errorf("%s: cancelling the job %s: %w", ModuleName, id, err)
	}
	if row.ClaimedBy == nil {
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
	requested := q.handle.WithContext(ctx).Model(&job{}).
		Where("id = ? AND claimed_by IS NOT NULL", string(id)).
		Updates(map[string]any{"cancel_requested": true, "updated_at": now})
	if requested.Error != nil {
		return fmt.Errorf("%s: recording a cancellation request for the job %s: %w", ModuleName, id, requested.Error)
	}
	return nil
}
