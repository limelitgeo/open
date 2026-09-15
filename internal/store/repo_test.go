// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package store

import (
	"context"
	"errors"
	"testing"
)

func TestNormalizeDomainCollapsesSpellings(t *testing.T) {
	// Two spellings of one company would be two competitor rows splitting
	// every count between them, which is why the domain is normalised before
	// it becomes the identity key.
	for raw, want := range map[string]string{
		"acme.com":                     "acme.com",
		"ACME.com":                     "acme.com",
		"www.acme.com":                 "acme.com",
		"https://acme.com":             "acme.com",
		"https://www.acme.com/pricing": "acme.com",
		"http://acme.com?utm=x":        "acme.com",
		"  acme.com.  ":                "acme.com",
		"":                             "",
	} {
		if got := NormalizeDomain(raw); got != want {
			t.Errorf("NormalizeDomain(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestPropertyRoundTrip(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	if _, err := db.Property(ctx); err != ErrNotFound {
		t.Errorf("an unconfigured instance returned %v, want ErrNotFound", err)
	}

	want := Property{Name: "Acme", Domain: "https://www.acme.com/", Aliases: []string{"Acme Inc", "", "  acme.io  "}}
	if err := db.SaveProperty(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := db.Property(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Domain != "acme.com" {
		t.Errorf("domain = %q", got.Domain)
	}
	if len(got.Aliases) != 2 || got.Aliases[1] != "acme.io" {
		t.Errorf("aliases = %v, want the blanks dropped and the rest trimmed", got.Aliases)
	}

	// Saving again updates rather than failing on the singleton constraint.
	if err := db.SaveProperty(ctx, Property{Name: "Acme Corp", Domain: "acme.com"}); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, _ = db.Property(ctx)
	if got.Name != "Acme Corp" {
		t.Errorf("name after update = %q", got.Name)
	}
}

func TestPropertyNamesIsWhatTheMatcherSearchesFor(t *testing.T) {
	p := Property{Name: "Acme", Domain: "acme.com", Aliases: []string{"Acme Inc"}}
	got := p.Names()
	if len(got) != 3 {
		t.Fatalf("Names() = %v", got)
	}
	if got[0] != "Acme" || got[len(got)-1] != "acme.com" {
		t.Errorf("Names() = %v, want name, aliases, then the domain", got)
	}
}

func TestCompetitorDomainIsTheIdentityKey(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	first, err := db.AddCompetitor(ctx, Competitor{Name: "Globex", Domain: "https://www.globex.com/"})
	if err != nil {
		t.Fatal(err)
	}
	// The same company, spelled differently, must land on the same row.
	second, err := db.AddCompetitor(ctx, Competitor{Name: "Globex Corporation", Domain: "globex.com"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("two spellings of one domain produced rows %d and %d", first, second)
	}
	comps, _ := db.Competitors(ctx)
	if len(comps) != 1 {
		t.Fatalf("stored %d competitors", len(comps))
	}
	if comps[0].Name != "Globex Corporation" {
		t.Errorf("the name was not updated: %q", comps[0].Name)
	}

	if _, err := db.AddCompetitor(ctx, Competitor{Name: "Nameless"}); err == nil {
		t.Error("a competitor with no domain was accepted")
	}
}

func TestPromptsAndActiveFilter(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	active, err := db.AddPrompt(ctx, Prompt{Text: "best crm", Category: "discovery", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddPrompt(ctx, Prompt{Text: "What is Acme?", Branded: true, Active: false}); err != nil {
		t.Fatal(err)
	}

	if got, _ := db.Prompts(ctx, false); len(got) != 1 || got[0].ID != active {
		t.Errorf("the active-only list = %v", got)
	}
	all, _ := db.Prompts(ctx, true)
	if len(all) != 2 {
		t.Fatalf("the full list has %d", len(all))
	}
	if !all[1].Branded {
		t.Error("the branded flag did not survive a round trip")
	}

	if err := db.SetPromptActive(ctx, all[1].ID, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.Prompts(ctx, false); len(got) != 2 {
		t.Error("reactivating a prompt did not bring it back")
	}

	if _, err := db.AddPrompt(ctx, Prompt{Text: "   "}); err == nil {
		t.Error("an empty prompt was accepted")
	}
}

func TestTargetsUpsertOnSpec(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	spec := Target{Spec: "chatgpt:openai:online", Engine: "chatgpt", Provider: "openai", Online: true, Access: "api"}
	first, err := db.AddTarget(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.AddTarget(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Errorf("adding one spec twice produced rows %d and %d", first, second)
	}
	got, _ := db.Targets(ctx, true)
	if len(got) != 1 || !got[0].Online || got[0].Access != "api" {
		t.Errorf("targets = %+v", got)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	if v, err := db.Setting(ctx, "absent"); err != nil || v != "" {
		t.Errorf("an unset setting returned %q, %v", v, err)
	}
	if err := db.SetSetting(ctx, "category", "CRM"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(ctx, "category", "AI visibility"); err != nil {
		t.Fatal(err)
	}
	if v, _ := db.Setting(ctx, "category"); v != "AI visibility" {
		t.Errorf("setting = %q after overwrite", v)
	}
}

func TestCountsAndRunsToday(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	mustExec(t, db, `INSERT INTO prompt (id, text, active) VALUES (1, 'q', 1), (2, 'r', 0)`)
	mustExec(t, db, `INSERT INTO competitor (name, domain) VALUES ('Globex', 'globex.com')`)
	mustExec(t, db, `INSERT INTO target (id, spec, engine, provider, access) VALUES (1, 'chatgpt:openai', 'chatgpt', 'openai', 'api')`)
	mustExec(t, db, `INSERT INTO chat (prompt_id, target_id, status) VALUES (1, 1, 'ok')`)

	c, err := db.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Counts drive the sidebar, so an inactive prompt must not inflate it.
	if c.Prompts != 1 || c.Competitors != 1 || c.Targets != 1 || c.Chats != 1 {
		t.Errorf("counts = %+v", c)
	}
	if c.LastChatAt == "" {
		t.Error("no last chat time")
	}

	// runs_per_day caps answers recorded today, so this has to count today's.
	n, err := db.RunsToday(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("RunsToday = %d", n)
	}
	mustExec(t, db, `INSERT INTO chat (prompt_id, target_id, status, created_at) VALUES (1, 1, 'ok', datetime('now', '-2 days'))`)
	if n, _ := db.RunsToday(ctx); n != 1 {
		t.Errorf("RunsToday counted an older chat: %d", n)
	}
}

func TestPausingATargetKeepsItsHistory(t *testing.T) {
	// Pause is the middle ground the runner needed: stop paying for an
	// engine without deleting what it already said. Delete cascades the
	// chats; pause must not.
	db := openTemp(t)
	ctx := context.Background()

	id, err := db.AddTarget(ctx, Target{Spec: "chatgpt:openai:online", Engine: "chatgpt", Provider: "openai", Online: true, Access: "api"})
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO prompt (id, text, active) VALUES (1, 'q', 1)`)
	mustExec(t, db, `INSERT INTO chat (prompt_id, target_id, status) VALUES (1, ?, 'ok')`, id)

	if err := db.SetTargetEnabled(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := db.Targets(ctx, true); len(enabled) != 0 {
		t.Errorf("a paused target is still offered to the runner: %+v", enabled)
	}
	all, _ := db.Targets(ctx, false)
	if len(all) != 1 || all[0].Enabled {
		t.Errorf("paused target = %+v", all)
	}
	if c, _ := db.Counts(ctx); c.Chats != 1 {
		t.Errorf("pausing changed the chat count to %d", c.Chats)
	}

	if err := db.SetTargetEnabled(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := db.Targets(ctx, true); len(enabled) != 1 {
		t.Error("resuming did not put the target back")
	}
	if err := db.SetTargetEnabled(ctx, 999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("pausing a target that does not exist = %v, want ErrNotFound", err)
	}
}

func TestTargetHealthIsDerivedFromChats(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	mustExec(t, db, `INSERT INTO prompt (id, text, active) VALUES (1, 'q', 1)`)
	mustExec(t, db, `INSERT INTO target (id, spec, engine, provider, access) VALUES
		(1, 'chatgpt:openai', 'chatgpt', 'openai', 'api'),
		(2, 'claude:anthropic', 'claude', 'anthropic', 'api'),
		(3, 'gemini:google', 'gemini', 'google', 'api')`)
	mustExec(t, db, `INSERT INTO chat (prompt_id, target_id, status, error, created_at) VALUES
		(1, 1, 'ok', '', '2026-09-01 10:00:00'),
		(1, 1, 'failed', 'older failure', '2026-09-02 10:00:00'),
		(1, 1, 'failed', 'no credit or quota left', '2026-09-03 10:00:00'),
		(1, 1, 'ok', '', '2026-09-04 10:00:00'),
		(1, 2, 'failed', 'credentials rejected', '2026-09-05 10:00:00')`)

	health, err := db.TargetHealths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	one := health[1]
	if one.LastOK != "2026-09-04 10:00:00" {
		t.Errorf("target 1 last ok = %q", one.LastOK)
	}
	if one.LastFailed != "2026-09-03 10:00:00" || one.LastError != "no credit or quota left" {
		t.Errorf("target 1 last failure = %q %q, want the newest failure and its message", one.LastFailed, one.LastError)
	}
	two := health[2]
	if two.LastOK != "" || two.LastError != "credentials rejected" {
		t.Errorf("target 2 = %+v, want no ok and the auth failure", two)
	}
	if _, ever := health[3]; ever {
		t.Error("a target that never ran has a health row")
	}
}

func TestDeleteSettingForgets(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if err := db.SetSetting(ctx, "k", "v"); err != nil {
		t.Fatal(err)
	}
	if at, _ := db.SettingUpdatedAt(ctx, "k"); at == "" {
		t.Error("no updated_at for a written setting")
	}
	if err := db.DeleteSetting(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if v, _ := db.Setting(ctx, "k"); v != "" {
		t.Errorf("setting survived deletion: %q", v)
	}
	if at, _ := db.SettingUpdatedAt(ctx, "k"); at != "" {
		t.Errorf("updated_at survived deletion: %q", at)
	}
	if err := db.DeleteSetting(ctx, "k"); err != nil {
		t.Errorf("deleting twice = %v, want no error", err)
	}
}
