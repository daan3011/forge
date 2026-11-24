package store

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
	"time"
)

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	r := s.Settings()
	if _, err := r.Get(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: %v", err)
	}
	noErr(t, r.Set(ctx, "x", "1"))
	noErr(t, r.Set(ctx, "x", "2"))
	if v, err := r.Get(ctx, "x"); err != nil || v != "2" {
		t.Errorf("Get = %q, %v", v, err)
	}
	noErr(t, r.Delete(ctx, "x"))
	noErr(t, r.Delete(ctx, "x"))
	if _, err := r.Get(ctx, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}

	if _, ok, err := r.SetupCompleted(ctx); ok || err != nil {
		t.Errorf("fresh db: setup completed %v, %v", ok, err)
	}
	noErr(t, r.MarkSetupCompleted(ctx))
	at, ok, err := r.SetupCompleted(ctx)
	if !ok || err != nil || at.IsZero() {
		t.Errorf("after mark: %v %v %v", at, ok, err)
	}

	// Clearing the cache leaves settings alone.
	noErr(t, s.Cache().Put(ctx, "k", "kind", "", []byte("v"), 0))
	_, err = s.w.ExecContext(ctx, `DELETE FROM kv_cache`)
	noErr(t, err)
	if _, ok, _ := r.SetupCompleted(ctx); !ok {
		t.Error("setup flag lost with the cache")
	}
}

// TestMigration0011Settings upgrades a version-10 database.
func TestMigration0011Settings(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	all, err := loadMigrations(migrationsFS)
	noErr(t, err)
	v10 := fstest.MapFS{}
	for _, m := range all[:10] {
		v10[m.name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	noErr(t, migrate(ctx, db, v10, time.Now))
	if _, err := db.ExecContext(ctx, `SELECT 1 FROM settings`); err == nil {
		t.Fatal("v10 has settings")
	}
	noErr(t, migrate(ctx, db, migrationsFS, time.Now))
	_, err = db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ('setup_completed', 'x', 1)`)
	noErr(t, err)
}
