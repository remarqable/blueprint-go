// Package jobs is a durable queue on the application's own database, so a job
// is enqueued in the same transaction as the write that caused it.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type Job struct {
	ID             int64  `gorm:"primaryKey"`
	TenantID       *int64 `gorm:"index"` // NULL = platform-level work
	Queue          string `gorm:"not null;default:default;index:idx_job_claim,priority:1"`
	Kind           string `gorm:"not null"`
	Payload        []byte `gorm:"not null"`
	IdempotencyKey *string

	Status      string `gorm:"not null;default:pending"`
	Priority    int16  `gorm:"not null;default:0"`
	Attempts    int16  `gorm:"not null;default:0"`
	MaxAttempts int16  `gorm:"not null;default:5"`

	RunAt       time.Time `gorm:"not null"`
	LeasedUntil *time.Time
	LeasedBy    *string

	LastError string
	TraceID   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Job) TableName() string { return "job" }

const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusDead    = "dead"
)

// Handler processes one job. It takes its transaction from the worker via ctx
// and never opens its own connection.
type Handler func(ctx context.Context, tx *gorm.DB, j *Job) error

var registry = map[string]Handler{}

// Register wires a handler at import time. Duplicate kinds are a programming
// error, not a runtime condition.
func Register(kind string, h Handler) {
	if _, dup := registry[kind]; dup {
		panic("jobs: duplicate handler " + kind)
	}
	registry[kind] = h
}

// fatal marks an error as terminal: retrying cannot help.
type fatal struct{ error }

func Fatal(err error) error { return fatal{err} }

func IsFatal(err error) bool {
	var f fatal
	return errors.As(err, &f)
}

type Options struct {
	Queue          string
	Priority       int16
	MaxAttempts    int16
	RunAt          *time.Time
	IdempotencyKey string
	TraceID        string
}

// Enqueue writes a job inside the caller's transaction. There is deliberately
// no variant taking the shared handle: a job that can commit independently of
// the work that caused it will eventually do exactly that.
func Enqueue(ctx context.Context, tx *gorm.DB, tenantID int64, kind string,
	payload any, opt Options) (int64, error) {

	if _, known := registry[kind]; !known {
		return 0, fmt.Errorf("jobs: no handler registered for %q", kind)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	if opt.Queue == "" {
		opt.Queue = "default"
	}
	if opt.MaxAttempts == 0 {
		opt.MaxAttempts = 5
	}
	runAt := time.Now()
	if opt.RunAt != nil {
		runAt = *opt.RunAt
	}

	j := Job{
		Queue: opt.Queue, Kind: kind, Payload: body,
		Status: StatusPending, Priority: opt.Priority,
		MaxAttempts: opt.MaxAttempts, RunAt: runAt, TraceID: opt.TraceID,
	}
	if tenantID != 0 {
		j.TenantID = &tenantID
	}
	if opt.IdempotencyKey != "" {
		j.IdempotencyKey = &opt.IdempotencyKey
	}

	if err := tx.WithContext(ctx).Create(&j).Error; err != nil {
		return 0, err
	}
	return j.ID, nil
}

// Pending counts queued work. Test and metrics helper.
func Pending(g *gorm.DB, queue string) (int64, error) {
	var n int64
	q := g.Model(&Job{}).Where("status = ?", StatusPending)
	if queue != "" {
		q = q.Where("queue = ?", queue)
	}
	return n, q.Count(&n).Error
}

func backoff(attempt int16) time.Duration {
	d := time.Duration(1<<uint(attempt)) * time.Second
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	return d
}
