package jobs_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"blueprintexample/internal/models"
	"blueprintexample/internal/platform/db"
	"blueprintexample/internal/platform/jobs"
)

var (
	ran      atomic.Int64
	failing  atomic.Bool
	terminal atomic.Bool
)

func init() {
	jobs.Register("noop", func(_ context.Context, _ *gorm.DB, _ *jobs.Job) error {
		ran.Add(1)
		if terminal.Load() {
			return jobs.Fatal(errors.New("malformed payload"))
		}
		if failing.Load() {
			return errors.New("upstream timeout")
		}
		return nil
	})
	jobs.Register("tenant_work", func(_ context.Context, tx *gorm.DB, j *jobs.Job) error {
		return tx.Create(&models.Post{TenantID: *j.TenantID, UserID: 1, Title: "from job"}).Error
	})
}

func setup(t *testing.T) (*gorm.DB, *jobs.Worker) {
	t.Helper()
	ran.Store(0)
	failing.Store(false)
	terminal.Store(false)

	g := db.SetupTest(t, func(g *gorm.DB) error {
		return g.AutoMigrate(&models.Tenant{}, &models.User{}, &models.Post{}, &jobs.Job{})
	})
	require.NoError(t, g.Create(&models.Tenant{ID: 1, Name: "Acme"}).Error)
	require.NoError(t, g.Create(&models.User{ID: 1, Email: "a@example.com", Name: "Alice"}).Error)

	w := jobs.NewWorker(db.Unscoped(), db.WithTenant, []string{"default"}, time.Minute, "test")
	return g, w
}

// The whole point of a database-backed queue: a rolled-back transaction
// leaves no job behind.
func TestRollbackLeavesNoJob(t *testing.T) {
	g, _ := setup(t)
	sentinel := errors.New("boom")

	err := db.WithTx(context.Background(), func(tx *gorm.DB) error {
		if _, err := jobs.Enqueue(context.Background(), tx, 0, "noop", nil, jobs.Options{}); err != nil {
			return err
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	n, err := jobs.Pending(g, "")
	require.NoError(t, err)
	assert.Zero(t, n, "job must not survive a rolled-back transaction")
}

func TestEnqueueAndWorkOff(t *testing.T) {
	g, w := setup(t)
	ctx := context.Background()

	require.NoError(t, db.WithTx(ctx, func(tx *gorm.DB) error {
		_, err := jobs.Enqueue(ctx, tx, 0, "noop", map[string]int{"n": 1}, jobs.Options{})
		return err
	}))

	n, _ := jobs.Pending(g, "default")
	require.EqualValues(t, 1, n)

	require.NoError(t, w.WorkOff(ctx))
	assert.EqualValues(t, 1, ran.Load())

	n, _ = jobs.Pending(g, "default")
	assert.Zero(t, n)
}

// An unregistered kind is a programming error, caught at enqueue rather than
// becoming a dead job hours later.
func TestUnknownKindIsRefused(t *testing.T) {
	_, _ = setup(t)
	err := db.WithTx(context.Background(), func(tx *gorm.DB) error {
		_, err := jobs.Enqueue(context.Background(), tx, 0, "nope", nil, jobs.Options{})
		return err
	})
	assert.Error(t, err)
}

// Retryable failures go back to pending; terminal ones die immediately rather
// than burning five attempts against a certainty.
func TestRetryableVersusTerminalFailure(t *testing.T) {
	g, w := setup(t)
	ctx := context.Background()

	failing.Store(true)
	require.NoError(t, db.WithTx(ctx, func(tx *gorm.DB) error {
		_, err := jobs.Enqueue(ctx, tx, 0, "noop", nil, jobs.Options{})
		return err
	}))
	j, err := w.Claim(ctx, "default")
	require.NoError(t, err)
	require.NoError(t, w.Execute(ctx, j))

	var got jobs.Job
	require.NoError(t, g.First(&got, j.ID).Error)
	assert.Equal(t, jobs.StatusPending, got.Status, "retryable failure must requeue")
	assert.EqualValues(t, 1, got.Attempts)
	assert.Contains(t, got.LastError, "upstream timeout")

	// Backoff defers the retry: the job is pending but not yet claimable.
	assert.True(t, got.RunAt.After(time.Now()), "retry must be deferred, not immediate")
	_, err = w.Claim(ctx, "default")
	assert.Error(t, err, "a backed-off job must not be claimable yet")

	// A terminal failure dies on the first attempt rather than burning five.
	terminal.Store(true)
	require.NoError(t, db.WithTx(ctx, func(tx *gorm.DB) error {
		_, err := jobs.Enqueue(ctx, tx, 0, "noop", nil, jobs.Options{})
		return err
	}))
	j2, err := w.Claim(ctx, "default")
	require.NoError(t, err)
	require.NoError(t, w.Execute(ctx, j2))

	// Fresh destination: reusing `got` would carry its primary key into the
	// conditions and silently find nothing. See patterns/database.md.
	var dead jobs.Job
	require.NoError(t, g.First(&dead, j2.ID).Error)
	assert.Equal(t, jobs.StatusDead, dead.Status, "terminal failure must not retry")
	assert.EqualValues(t, 1, dead.Attempts, "terminal failure must die on attempt 1")
}

// A crashed worker leaves a running row; the reaper returns it.
func TestExpiredLeaseIsReclaimed(t *testing.T) {
	g, w := setup(t)
	ctx := context.Background()

	require.NoError(t, db.WithTx(ctx, func(tx *gorm.DB) error {
		_, err := jobs.Enqueue(ctx, tx, 0, "noop", nil, jobs.Options{})
		return err
	}))
	j, err := w.Claim(ctx, "default")
	require.NoError(t, err)

	// Simulate the worker dying mid-job.
	past := time.Now().Add(-time.Hour)
	require.NoError(t, g.Model(&jobs.Job{}).Where("id = ?", j.ID).
		Update("leased_until", past).Error)

	n, err := w.Reap(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	var got jobs.Job
	require.NoError(t, g.First(&got, j.ID).Error)
	assert.Equal(t, jobs.StatusPending, got.Status)
}

// A tenant job re-enters its scope: the handler writes as that tenant, and
// another tenant cannot see the result.
func TestTenantJobRunsInScope(t *testing.T) {
	g, w := setup(t)
	ctx := context.Background()
	require.NoError(t, g.Create(&models.Tenant{ID: 2, Name: "Globex"}).Error)

	require.NoError(t, db.WithTenant(ctx, 1, func(tx *gorm.DB) error {
		_, err := jobs.Enqueue(ctx, tx, 1, "tenant_work", nil, jobs.Options{})
		return err
	}))
	require.NoError(t, w.WorkOff(ctx))

	var count int64
	require.NoError(t, db.WithTenant(ctx, 1, func(tx *gorm.DB) error {
		return tx.Model(&models.Post{}).Count(&count).Error
	}))
	assert.EqualValues(t, 1, count, "tenant 1 sees the row its job wrote")

	require.NoError(t, db.WithTenant(ctx, 2, func(tx *gorm.DB) error {
		return tx.Model(&models.Post{}).Count(&count).Error
	}))
	assert.Zero(t, count, "tenant 2 must not see it")
}
