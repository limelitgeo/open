// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "limelit.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesDirectoryAndMigrates(t *testing.T) {
	// A fresh install must not need a mkdir step of its own.
	path := filepath.Join(t.TempDir(), "nested", "deeper", "limelit.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open into a missing directory: %v", err)
	}
	defer db.Close()

	applied, err := db.AppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("AppliedMigrations: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("no migrations were applied")
	}
	if applied[0] != "0001_init.sql" {
		t.Errorf("first migration = %q", applied[0])
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "limelit.db")
	ctx := context.Background()

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	before, err := first.AppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	after, err := second.AppliedMigrations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Errorf("reopening re-ran migrations: %v then %v", before, after)
	}
}

func TestSchemaHasEveryTable(t *testing.T) {
	db := openTemp(t)
	want := []string{
		"property", "competitor", "prompt", "target", "evaluation",
		"chat", "mention", "citation", "usage_day", "settings",
	}
	for _, table := range want {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestForeignKeysCascadeFromChat(t *testing.T) {
	// Deleting an answer must leave no orphaned mention or citation, which is
	// what makes `limelit recompute` safe to run against stored rows.
	db := openTemp(t)
	ctx := context.Background()

	mustExec(t, db, `INSERT INTO prompt (id, text) VALUES (1, 'best crm for startups')`)
	mustExec(t, db, `INSERT INTO target (id, spec, engine, provider, access) VALUES (1, 'chatgpt:openai', 'chatgpt', 'openai', 'api')`)
	mustExec(t, db, `INSERT INTO chat (id, prompt_id, target_id, status, text) VALUES (1, 1, 1, 'ok', 'Acme is good')`)
	mustExec(t, db, `INSERT INTO mention (chat_id, brand_key, brand_name, offset_start, offset_end) VALUES (1, 'acme', 'Acme', 0, 4)`)
	mustExec(t, db, `INSERT INTO citation (chat_id, url, host, site, source_type) VALUES (1, 'https://acme.com/x', 'acme.com', 'acme.com', 'own')`)

	mustExec(t, db, `DELETE FROM chat WHERE id = 1`)

	for _, table := range []string{"mention", "citation"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s still has %d rows after its chat was deleted", table, n)
		}
	}
}

func TestChatStatusIsConstrained(t *testing.T) {
	// 'no_answer_surface' is a distinct status because it is not a miss for
	// the brand; the constraint stops a fourth spelling appearing later.
	db := openTemp(t)
	mustExec(t, db, `INSERT INTO prompt (id, text) VALUES (1, 'q')`)
	mustExec(t, db, `INSERT INTO target (id, spec, engine, provider, access) VALUES (1, 'ai_overview:dataforseo', 'ai_overview', 'dataforseo', 'scraped')`)

	if _, err := db.Exec(`INSERT INTO chat (prompt_id, target_id, status) VALUES (1, 1, 'weird')`); err == nil {
		t.Error("an unknown chat status was accepted")
	}
	for _, ok := range []string{"ok", "failed", "no_answer_surface"} {
		if _, err := db.Exec(`INSERT INTO chat (prompt_id, target_id, status) VALUES (1, 1, ?)`, ok); err != nil {
			t.Errorf("status %q rejected: %v", ok, err)
		}
	}
}

func TestTargetAccessIsConstrained(t *testing.T) {
	db := openTemp(t)
	if _, err := db.Exec(`INSERT INTO target (spec, engine, provider, access) VALUES ('x:y', 'x', 'y', 'guessed')`); err == nil {
		t.Error("an unknown access mode was accepted")
	}
}

func TestCompetitorDomainIsUnique(t *testing.T) {
	db := openTemp(t)
	mustExec(t, db, `INSERT INTO competitor (name, domain) VALUES ('Globex', 'globex.com')`)
	if _, err := db.Exec(`INSERT INTO competitor (name, domain) VALUES ('Globex Corp', 'globex.com')`); err == nil {
		t.Error("two competitors with the same domain were accepted")
	}
}

func TestUsageIsPerTargetPerDay(t *testing.T) {
	// Usage is stored daily and summed for a month, never stored twice, so the
	// two grains cannot disagree.
	db := openTemp(t)
	mustExec(t, db, `INSERT INTO target (id, spec, engine, provider, access) VALUES (1, 'chatgpt:openai', 'chatgpt', 'openai', 'api')`)
	mustExec(t, db, `INSERT INTO usage_day (target_id, day, calls) VALUES (1, '2026-09-11', 3)`)
	mustExec(t, db, `INSERT INTO usage_day (target_id, day, calls) VALUES (1, '2026-09-12', 4)`)

	if _, err := db.Exec(`INSERT INTO usage_day (target_id, day, calls) VALUES (1, '2026-09-11', 9)`); err == nil {
		t.Error("a duplicate (target, day) row was accepted")
	}

	var month int
	if err := db.QueryRow(`SELECT SUM(calls) FROM usage_day WHERE target_id = 1 AND day LIKE '2026-09-%'`).Scan(&month); err != nil {
		t.Fatal(err)
	}
	if month != 7 {
		t.Errorf("month total = %d, want 7", month)
	}
}

func TestPropertyIsSingleton(t *testing.T) {
	// v0.1 tracks one brand per instance; multi-brand is a hosted feature.
	db := openTemp(t)
	mustExec(t, db, `INSERT INTO property (id, name, domain) VALUES (1, 'Acme', 'acme.com')`)
	if _, err := db.Exec(`INSERT INTO property (id, name, domain) VALUES (2, 'Globex', 'globex.com')`); err == nil {
		t.Error("a second property row was accepted")
	}
}

func mustExec(t *testing.T, db *DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
