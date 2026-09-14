package db_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"blueprintexample/internal/models"
	"blueprintexample/internal/platform/db"
)

func migrate(g *gorm.DB) error {
	return g.AutoMigrate(&models.Tenant{}, &models.User{}, &models.Post{})
}

func setup(t *testing.T) {
	t.Helper()
	g := db.SetupTest(t, migrate)
	require.NoError(t, g.Create(&[]models.Tenant{{ID: 1, Name: "Acme"}, {ID: 2, Name: "Globex"}}).Error)
	require.NoError(t, g.Create(&models.User{ID: 1, Email: "a@example.com", Name: "Alice"}).Error)
}

// A tenant cannot read another tenant's rows.
func TestTenantCannotReadAcrossBoundary(t *testing.T) {
	setup(t)
	ctx := context.Background()

	require.NoError(t, db.WithTenant(ctx, 1, func(tx *gorm.DB) error {
		return tx.Create(&models.Post{TenantID: 1, UserID: 1, Title: "tenant one"}).Error
	}))

	var count int64
	require.NoError(t, db.WithTenant(ctx, 2, func(tx *gorm.DB) error {
		return tx.Model(&models.Post{}).Count(&count).Error
	}))
	assert.Zero(t, count, "tenant 2 must not see tenant 1 rows")

	require.NoError(t, db.WithTenant(ctx, 1, func(tx *gorm.DB) error {
		return tx.Model(&models.Post{}).Count(&count).Error
	}))
	assert.EqualValues(t, 1, count, "tenant 1 must see its own row")
}

// The WITH CHECK half: a tenant cannot write into another tenant.
func TestTenantCannotWriteAcrossBoundary(t *testing.T) {
	setup(t)

	err := db.WithTenant(context.Background(), 2, func(tx *gorm.DB) error {
		return tx.Create(&models.Post{TenantID: 1, UserID: 1, Title: "forged"}).Error
	})
	assert.Error(t, err, "cross-tenant INSERT must be rejected")
}

// Unset means zero rows, never all rows.
func TestNoTenantIsRefused(t *testing.T) {
	setup(t)

	err := db.WithTenant(context.Background(), 0, func(tx *gorm.DB) error {
		t.Fatal("callback must not run without a tenant")
		return nil
	})
	assert.ErrorIs(t, err, db.ErrNoTenant)
}

// The shared handle fails closed rather than leaking.
func TestSharedHandleFailsClosed(t *testing.T) {
	setup(t)

	require.NoError(t, db.WithTenant(context.Background(), 1, func(tx *gorm.DB) error {
		return tx.Create(&models.Post{TenantID: 1, UserID: 1, Title: "tenant one"}).Error
	}))

	var leaked int64
	err := db.Get().Model(&models.Post{}).Count(&leaked).Error
	assert.Error(t, err, "unscoped read of a tenant table must fail, not leak")
	assert.Zero(t, leaked)
}

// Update and delete are scoped too, not just select.
func TestTenantCannotUpdateAcrossBoundary(t *testing.T) {
	setup(t)
	ctx := context.Background()

	require.NoError(t, db.WithTenant(ctx, 1, func(tx *gorm.DB) error {
		return tx.Create(&models.Post{TenantID: 1, UserID: 1, Title: "original"}).Error
	}))

	require.NoError(t, db.WithTenant(ctx, 2, func(tx *gorm.DB) error {
		res := tx.Model(&models.Post{}).Where("1 = 1").Update("title", "hijacked")
		assert.Zero(t, res.RowsAffected, "tenant 2 must not update tenant 1 rows")
		return nil
	}))

	var p models.Post
	require.NoError(t, db.WithTenant(ctx, 1, func(tx *gorm.DB) error {
		return tx.First(&p).Error
	}))
	assert.Equal(t, "original", p.Title)
}

// Tables without a tenant_id are reachable through the shared handle.
func TestNonTenantTableIsUnaffected(t *testing.T) {
	setup(t)

	var users int64
	require.NoError(t, db.Get().Model(&models.User{}).Count(&users).Error)
	assert.EqualValues(t, 1, users)
}
