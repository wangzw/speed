// Package jobs declares the job-execution capability for a distributed
// deployment: work handed in as a durable job record and run by whichever
// replica claims it.
package jobs

import (
	"context"
	"errors"
	"time"
)

// JobID identifies one job, generated when the job is enqueued.
type JobID string

// Status is the state of a job: pending, running and retrying are not terminal,
// while succeeded, dead_letter and cancelled are. The persistent form is the
// same string as the constant below.
type Status string

const (
	// StatusPending is a job that has not been claimed yet.
	StatusPending Status = "pending"
	// StatusRunning is a job a replica has claimed and is executing.
	StatusRunning Status = "running"
	// StatusRetrying is a job that failed and is waiting for its backoff.
	StatusRetrying Status = "retrying"
	// StatusSucceeded is a job whose handler returned without error. It is
	// terminal.
	StatusSucceeded Status = "succeeded"
	// StatusDeadLetter is a job whose handler kept failing past its retry limit.
	// It is terminal.
	StatusDeadLetter Status = "dead_letter"
	// StatusCancelled is a job that was cancelled. It is terminal.
	StatusCancelled Status = "cancelled"
)

// Terminal reports whether s is a status a job never leaves.
func (s Status) Terminal() bool {
	switch s {
	case StatusSucceeded, StatusDeadLetter, StatusCancelled:
		return true
	default:
		return false
	}
}

// Priority orders the claim: PriorityHigh first, then PriorityNormal, then
// PriorityLow. A job takes PriorityNormal unless the caller says otherwise.
type Priority int

const (
	// PriorityLow is the lowest claim priority.
	PriorityLow Priority = 0
	// PriorityNormal is the priority a job takes when Enqueue is given no
	// WithPriority.
	PriorityNormal Priority = 5
	// PriorityHigh is the highest claim priority.
	PriorityHigh Priority = 10
)

// Task is one piece of work a caller hands in. It carries no tenant: the tenant
// is read from the calling context.
type Task struct {
	// Type names the kind of job and must match a registered Handler's Type.
	Type string
	// Payload is the job's data, stored and never interpreted.
	Payload []byte
	// IdempotencyKey, when non-empty, makes Enqueue idempotent for the same key
	// and tenant.
	IdempotencyKey string
}

// Result is what a handler hands back. The capability only stores it and reports
// it back through Get.
type Result struct {
	// Data is the bytes the handler returned.
	Data []byte
}

// Job is the read-only view of one job. Its fields mirror the job table's
// columns.
type Job struct {
	// ID identifies the job.
	ID JobID
	// Type names the kind of job, matching the registered handler's Type.
	Type string
	// Status is the job's state.
	Status Status
	// Priority is the job's claim priority.
	Priority Priority
	// Payload is the data the caller submitted.
	Payload []byte
	// Result is what the handler returned on success, and nil until then.
	Result *Result
	// Error is the text of the job's most recent failure.
	Error string
	// Attempts is how many times the job has been handed to a handler.
	Attempts int
	// MaxRetries is the job's retry limit; past it a further failure sends the
	// job to dead_letter.
	MaxRetries int
	// Timeout is the limit on a single execution.
	Timeout time.Duration
	// ProgressPct is the last integer percentage the handler reported.
	ProgressPct int
	// ProgressMsg is the last text the handler reported.
	ProgressMsg string
	// ScheduledAt is when the caller asked the job to run.
	ScheduledAt time.Time
	// StartedAt is when the most recent execution began, and nil before the
	// first one.
	StartedAt *time.Time
	// CompletedAt is when the job reached a terminal status, and nil until then.
	CompletedAt *time.Time
	// CreatedAt is when the row was written.
	CreatedAt time.Time
	// UpdatedAt is when the row was last changed.
	UpdatedAt time.Time
}

// ProgressFn reports execution progress from inside a handler: an integer
// percentage and a message.
type ProgressFn func(pct int, msg string)

// Handler executes one kind of job. A module registers one with the Handlers
// face in its Init. A handler must be safe to call concurrently.
type Handler interface {
	// Type reports the job type this handler executes.
	Type() string
	// Handle runs one job. A non-nil error is a failed execution.
	Handle(ctx context.Context, job *Job, progress ProgressFn) (Result, error)
}

// Queue is the portable submission face. It is delivered exclusively, its
// methods may be called from more than one goroutine at once, and every method
// returns ErrNotRunning outside the queue's running stages.
type Queue interface {
	// Enqueue writes one job and returns its identifier. It returns ErrInvalidTask
	// or ErrNoHandler and writes nothing; the tenant comes from ctx, and a
	// non-empty IdempotencyKey returns the existing job's identifier.
	Enqueue(ctx context.Context, t Task, opts ...EnqueueOption) (JobID, error)
	// Get returns one job visible to the tenant of ctx. An unknown identifier,
	// another tenant's identifier and an identifier no longer visible are all
	// reported as ErrJobNotFound.
	Get(ctx context.Context, id JobID) (*Job, error)
	// Cancel asks for a job to be cancelled, by the job's current status: an
	// unclaimed job moves straight to cancelled, a claimed one records the request,
	// a terminal one is an idempotent no-op. An invisible job is ErrJobNotFound.
	Cancel(ctx context.Context, id JobID) error
}

// Handlers is the registration face: a module registers its handler here in its
// Init, before the queue starts. It is delivered by the same module as Queue,
// and exclusively with it.
type Handlers interface {
	// Register registers h under its Type. Registering a type twice, an empty
	// Type, nil, or after the queue has started are all refused and leave the
	// handler set unchanged.
	Register(h Handler) error
	// Registered lists the registered types.
	Registered() []string
}

type enqueueOptions struct {
	Priority    Priority
	ScheduledAt time.Time
	MaxRetries  int
	Timeout     time.Duration
}

// EnqueueOption sets one parameter of an Enqueue call.
type EnqueueOption func(*enqueueOptions)

// NewEnqueueOptions returns the parameters an Enqueue call starts from:
// PriorityNormal, DefaultMaxRetries and DefaultTimeout, with no scheduled time.
func NewEnqueueOptions() *enqueueOptions {
	return &enqueueOptions{
		Priority:   PriorityNormal,
		MaxRetries: DefaultMaxRetries,
		Timeout:    DefaultTimeout,
	}
}

// WithPriority sets the job's Priority.
func WithPriority(p Priority) EnqueueOption {
	return func(o *enqueueOptions) { o.Priority = p }
}

// WithDelay makes the job eligible to be claimed a duration from now. It sets
// the same parameter as WithScheduledAt, so the two are mutually exclusive.
func WithDelay(d time.Duration) EnqueueOption {
	return func(o *enqueueOptions) { o.ScheduledAt = time.Now().Add(d) }
}

// WithScheduledAt makes the job eligible to be claimed no earlier than t. It
// sets the same parameter as WithDelay, so the two are mutually exclusive.
func WithScheduledAt(t time.Time) EnqueueOption {
	return func(o *enqueueOptions) { o.ScheduledAt = t }
}

// WithMaxRetries sets how many times the job may fail before it is sent to
// dead_letter.
func WithMaxRetries(n int) EnqueueOption {
	return func(o *enqueueOptions) { o.MaxRetries = n }
}

// WithTimeout sets the limit on a single execution.
func WithTimeout(d time.Duration) EnqueueOption {
	return func(o *enqueueOptions) { o.Timeout = d }
}

const (
	// DefaultMaxRetries is the retry limit a job takes when Enqueue is given no
	// WithMaxRetries: the initial attempt and three retries.
	DefaultMaxRetries = 3
	// DefaultTimeout is the limit on a single execution a job takes when Enqueue
	// is given no WithTimeout.
	DefaultTimeout = 5 * time.Minute
)

var (
	// ErrJobNotFound reports a Get or Cancel that named a job this tenant cannot
	// see; an unknown identifier, another tenant's and one no longer visible are
	// reported alike.
	ErrJobNotFound = errors.New("jobs: no job with that identifier is visible to this tenant")
	// ErrInvalidTask reports a Task whose shape is not valid, such as an empty
	// Type.
	ErrInvalidTask = errors.New("jobs: the task is not a valid shape")
	// ErrNoHandler reports an Enqueue whose task Type has no registered handler.
	ErrNoHandler = errors.New("jobs: no handler is registered for the task's type")
	// ErrNotRunning reports a queue call made outside the queue's service.
	ErrNotRunning = errors.New("jobs: the queue is not running")
)
