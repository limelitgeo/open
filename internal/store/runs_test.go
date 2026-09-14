// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package store

import (
	"context"
	"strings"
	"testing"
)

// seeded gives a property, one prompt and one target, so a chat has
// something to hang off.
func seeded(t *testing.T) *DB {
	t.Helper()
	db := openTemp(t)
	ctx := context.Background()
	if err := db.SaveProperty(ctx, Property{Name: "Acme", Domain: "acme.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPrompt(ctx, Prompt{Text: "p", Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTarget(ctx, Target{Spec: "chatgpt:openai", Engine: "chatgpt", Provider: "openai", Access: "api"}); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestRecordChatHonoursAnImportedTimestamp(t *testing.T) {
	db := seeded(t)
	ctx := context.Background()

	id, err := db.RecordChat(ctx, ChatRecord{
		PromptID: 1, TargetID: 1, Status: ChatOK, Text: "x", Calls: 1,
		CreatedAt: "2026-07-13 09:00:00",
	})
	if err != nil {
		t.Fatal(err)
	}
	var created, usageDay string
	if err := db.QueryRowContext(ctx, `SELECT created_at FROM chat WHERE id = ?`, id).Scan(&created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created, "2026-07-13") {
		t.Errorf("created_at = %q, want the imported day", created)
	}
	if err := db.QueryRowContext(ctx, `SELECT day FROM usage_day WHERE target_id = 1`).Scan(&usageDay); err != nil {
		t.Fatal(err)
	}
	if usageDay != "2026-07-13" {
		t.Errorf("usage day = %q, want the imported day, not today", usageDay)
	}

	// And the runner's path, with no timestamp, still takes now.
	id2, err := db.RecordChat(ctx, ChatRecord{PromptID: 1, TargetID: 1, Status: ChatOK, Text: "y"})
	if err != nil {
		t.Fatal(err)
	}
	var created2 string
	db.QueryRowContext(ctx, `SELECT created_at FROM chat WHERE id = ?`, id2).Scan(&created2)
	if strings.HasPrefix(created2, "2026-07-13") {
		t.Error("an unset CreatedAt inherited the imported date")
	}
}
