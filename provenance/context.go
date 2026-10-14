package provenance

import (
	"context"

	"gorm.io/gorm"
)

type ctxKey struct{}

// WithContext returns a child context carrying p (normalized). A zero p
// returns ctx unchanged.
func WithContext(ctx context.Context, p Provenance) context.Context {
	if p.IsZero() {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, p.Normalize())
}

// FromContext returns the provenance carried by ctx, if any.
func FromContext(ctx context.Context) (Provenance, bool) {
	if ctx == nil {
		return Provenance{}, false
	}
	p, ok := ctx.Value(ctxKey{}).(Provenance)
	if !ok || p.IsZero() {
		return Provenance{}, false
	}
	return p, true
}

// Ensure returns ctx unchanged when it already carries a provenance, else a
// child carrying fallback. Use it where a process has a sensible default (an
// importer, a seeder) but a caller higher up may already know better.
func Ensure(ctx context.Context, fallback Provenance) context.Context {
	if _, ok := FromContext(ctx); ok {
		return ctx
	}
	return WithContext(ctx, fallback)
}

// Update derives the ctx provenance with fn (e.g. to add a tool-call ref).
// No provenance in ctx → ctx unchanged.
func Update(ctx context.Context, fn func(Provenance) Provenance) context.Context {
	p, ok := FromContext(ctx)
	if !ok {
		return ctx
	}
	return WithContext(ctx, fn(p))
}

// Without returns a child context that carries no provenance (writes under it
// are not stamped). Used by the store itself and by hosts writing technical
// rows inside a stamped request.
func Without(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ctxKey{}, Provenance{})
}

// DB returns db bound to a context carrying p, for code that writes with GORM
// directly (seeders, importers, services without a ctx of their own):
//
//	provenance.DB(db, provenance.System(provenance.ViaSeed, "Datos semilla")).Create(&rows)
func DB(db *gorm.DB, p Provenance) *gorm.DB {
	ctx := context.Background()
	if db.Statement != nil && db.Statement.Context != nil {
		ctx = db.Statement.Context
	}
	return db.WithContext(WithContext(ctx, p))
}
