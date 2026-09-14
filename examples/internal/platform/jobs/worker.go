package jobs

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Scoper re-enters a tenant scope for a job. The worker connects as the owner
// role so it can see the queue; tenant jobs are scoped exactly like a request.
type Scoper func(ctx context.Context, tenantID int64, fn func(tx *gorm.DB) error) error

type Worker struct {
	owner  *gorm.DB // owner handle: sees every tenant's jobs
	scope  Scoper
	queues []string
	lease  time.Duration
	id     string
}

func NewWorker(owner *gorm.DB, scope Scoper, queues []string, lease time.Duration, id string) *Worker {
	if lease == 0 {
		lease = 5 * time.Minute
	}
	return &Worker{owner: owner, scope: scope, queues: queues, lease: lease, id: id}
}

// Claim takes one job and leases it. The lease is what makes this survive a
// worker being killed: the reaper returns expired leases to pending.
func (w *Worker) Claim(ctx context.Context, queue string) (*Job, error) {
	var j Job
	err := w.owner.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := tx.Where("status = ? AND queue = ? AND run_at <= ?",
			StatusPending, queue, time.Now()).
			Order("priority DESC, run_at")

		// SELECT ... FOR UPDATE SKIP LOCKED lets N workers poll the same table
		// without blocking each other. SQLite has no such clause and no
		// concurrent writers to protect against.
		if w.owner.Dialector.Name() == "postgres" {
			q = q.Clauses(skipLocked())
		}
		if err := q.First(&j).Error; err != nil {
			return err
		}

		until := time.Now().Add(w.lease)
		return tx.Model(&j).Updates(map[string]any{
			"status":       StatusRunning,
			"attempts":     j.Attempts + 1,
			"leased_until": until,
			"leased_by":    w.id,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// Execute runs one job's handler and records the outcome. Delivery is
// at-least-once, so handlers must be idempotent.
func (w *Worker) Execute(ctx context.Context, j *Job) error {
	h, ok := registry[j.Kind]
	if !ok {
		// A deploy removed a handler while jobs were queued. Retrying cannot
		// fix that and would burn every attempt against a certainty.
		return w.fail(ctx, j, Fatal(errNoHandler(j.Kind)))
	}

	var err error
	if j.TenantID == nil {
		err = h(ctx, w.owner.WithContext(ctx), j) // platform-level work
	} else {
		err = w.scope(ctx, *j.TenantID, func(tx *gorm.DB) error {
			return h(ctx, tx, j)
		})
	}

	if err != nil {
		return w.fail(ctx, j, err)
	}
	return w.owner.WithContext(ctx).Model(j).
		Updates(map[string]any{"status": StatusDone, "leased_until": nil, "leased_by": nil}).Error
}

func (w *Worker) fail(ctx context.Context, j *Job, cause error) error {
	status, runAt := StatusPending, time.Now().Add(backoff(j.Attempts))
	if IsFatal(cause) || j.Attempts >= j.MaxAttempts {
		status = StatusDead
	}
	return w.owner.WithContext(ctx).Model(j).Updates(map[string]any{
		"status":       status,
		"run_at":       runAt,
		"last_error":   cause.Error(),
		"leased_until": nil,
		"leased_by":    nil,
	}).Error
}

// Reap returns jobs whose lease expired to the pending pool. Run it on a
// ticker; it is idempotent and cheap.
func (w *Worker) Reap(ctx context.Context) (int64, error) {
	res := w.owner.WithContext(ctx).Model(&Job{}).
		Where("status = ? AND leased_until < ?", StatusRunning, time.Now()).
		Updates(map[string]any{"status": StatusPending, "leased_until": nil, "leased_by": nil})
	return res.RowsAffected, res.Error
}

// WorkOff drains the queue once, in order. Test helper: controller tests assert
// on the effect of a job without running a worker process.
func (w *Worker) WorkOff(ctx context.Context) error {
	for _, queue := range w.queues {
		for {
			j, err := w.Claim(ctx, queue)
			if err != nil {
				break // no more work in this queue
			}
			if err := w.Execute(ctx, j); err != nil {
				return err
			}
		}
	}
	return nil
}
