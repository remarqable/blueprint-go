// Package models holds the domain. Fat models: business logic and queries live
// here, controllers stay thin. See patterns/mvc.md.
package models

import "time"

type Tenant struct {
	ID        int64  `gorm:"primaryKey"`
	Name      string `gorm:"not null"`
	CreatedAt time.Time
}

func (Tenant) TableName() string { return "tenant" }

// User is not tenant-scoped: a person can belong to several tenants.
type User struct {
	ID        int64  `gorm:"primaryKey"`
	Email     string `gorm:"not null;uniqueIndex"`
	Name      string `gorm:"not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (User) TableName() string { return "user" }

// Post is tenant-scoped. tenant_id leads every composite index because it is
// in the predicate of every query. See patterns/tenancy.md.
type Post struct {
	ID        int64  `gorm:"primaryKey"`
	TenantID  int64  `gorm:"not null;index:idx_post_tenant_created,priority:1"`
	UserID    int64  `gorm:"not null"`
	Title     string `gorm:"not null"`
	Body      string
	CreatedAt time.Time `gorm:"index:idx_post_tenant_created,priority:2,sort:desc"`
	UpdatedAt time.Time
}

func (Post) TableName() string { return "post" }
