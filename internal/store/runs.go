// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Evaluation statuses.
const (
	EvaluationRunning   = "running"
	EvaluationDone      = "done"
	EvaluationFailed    = "failed"
	EvaluationCancelled = "cancelled"
)

// Chat statuses. NoAnswerSurface is not a miss for the brand: the surface
// itself did not render, so these rows stay out of every metric denominator.
const (
	ChatOK              = "ok"
	ChatFailed          = "failed"
	ChatNoAnswerSurface = "no_answer_surface"
)

// Evaluation is one pass over prompts and targets.
type Evaluation struct {
	ID         int64
	Status     string
	Planned    int
	Completed  int
	Failed     int
	StartedAt  string
	FinishedAt string
}

// Done reports whether this evaluation has stopped, whatever the outcome.
func (e Evaluation) Done() bool { return e.Status != EvaluationRunning }

// Mention is one brand found in one answer.
type Mention struct {
	// CompetitorID is nil for the property's own mentions.
	CompetitorID *int64
	BrandKey     string
	BrandName    string
	OffsetStart  int
	OffsetEnd    int
	// ListRank is the 1-based rank of the enclosing list item, nil when the
	// mention is not inside a ranked list.
	ListRank *int
}

// Citation is one classified source an answer cited.
type Citation struct {
	URL        string
	Host       string
	Site       string
	Title      string
	Position   int
	SourceType string
}

// ChatRecord is one answer and everything derived from it, written together.
type ChatRecord struct {
	EvaluationID int64
	PromptID     int64
	TargetID     int64
	Status       string
	Text         string
	Model        string
	Error        string
	InputTokens  int
	OutputTokens int
	Calls        int
	Mentions     []Mention
	Citations    []Citation
	// FanOut is the searches the engine ran while grounding, in its order.
	// Empty means the provider did not report any, which is not the same as
	// the engine having searched for nothing.
	FanOut []string
}

// CreateEvaluation opens a pass.
func (db *DB) CreateEvaluation(ctx context.Context, planned int) (int64, error) {
	res, err := db.ExecContext(ctx,
		`INSERT INTO evaluation (status, planned) VALUES (?, ?)`, EvaluationRunning, planned)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishEvaluation closes a pass.
func (db *DB) FinishEvaluation(ctx context.Context, id int64, status string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE evaluation SET status = ?, finished_at = datetime('now') WHERE id = ?`, status, id)
	return err
}

// Evaluation reads one pass.
func (db *DB) Evaluation(ctx context.Context, id int64) (Evaluation, error) {
	return db.scanEvaluation(db.QueryRowContext(ctx, `
		SELECT id, status, planned, completed, failed, started_at, COALESCE(finished_at, '')
		FROM evaluation WHERE id = ?`, id))
}

// LatestEvaluation reads the most recent pass, or ErrNotFound before any.
func (db *DB) LatestEvaluation(ctx context.Context) (Evaluation, error) {
	return db.scanEvaluation(db.QueryRowContext(ctx, `
		SELECT id, status, planned, completed, failed, started_at, COALESCE(finished_at, '')
		FROM evaluation ORDER BY id DESC LIMIT 1`))
}

func (db *DB) scanEvaluation(row *sql.Row) (Evaluation, error) {
	var e Evaluation
	err := row.Scan(&e.ID, &e.Status, &e.Planned, &e.Completed, &e.Failed, &e.StartedAt, &e.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Evaluation{}, ErrNotFound
	}
	return e, err
}

// SweepOrphanedEvaluations closes passes left running by a process that died.
//
// A running evaluation can only be advanced by the process that started it,
// so one that survives a restart will never finish. Left alone it would show
// as in flight forever, and the dashboard would keep saying a run was
// happening. Its chat rows are real and are kept.
func (db *DB) SweepOrphanedEvaluations(ctx context.Context) (int, error) {
	res, err := db.ExecContext(ctx, `
		UPDATE evaluation SET status = ?, finished_at = datetime('now')
		WHERE status = ?`, EvaluationFailed, EvaluationRunning)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// RecordChat writes one answer, its mentions, its citations, its usage and
// the evaluation's counters in a single transaction.
//
// One transaction because a process killed between these writes would leave
// an answer with no mentions, which reads as a brand that was not talked
// about rather than as a run that did not finish. The counters live on the
// evaluation row rather than in memory for the same reason.
func (db *DB) RecordChat(ctx context.Context, rec ChatRecord) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO chat (evaluation_id, prompt_id, target_id, status, text, model, error, input_tokens, output_tokens, calls)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullableID(rec.EvaluationID), rec.PromptID, rec.TargetID, rec.Status,
		rec.Text, rec.Model, rec.Error, rec.InputTokens, rec.OutputTokens, rec.Calls)
	if err != nil {
		return 0, fmt.Errorf("insert chat: %w", err)
	}
	chatID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	for _, m := range rec.Mentions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO mention (chat_id, competitor_id, brand_key, brand_name, offset_start, offset_end, list_rank)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			chatID, m.CompetitorID, m.BrandKey, m.BrandName, m.OffsetStart, m.OffsetEnd, m.ListRank); err != nil {
			return 0, fmt.Errorf("insert mention: %w", err)
		}
	}
	for _, c := range rec.Citations {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO citation (chat_id, url, host, site, title, position, source_type)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			chatID, c.URL, c.Host, c.Site, c.Title, c.Position, c.SourceType); err != nil {
			return 0, fmt.Errorf("insert citation: %w", err)
		}
	}

	for i, q := range rec.FanOut {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO fanout (chat_id, query, position) VALUES (?, ?, ?)`,
			chatID, q, i+1); err != nil {
			return 0, fmt.Errorf("insert fanout: %w", err)
		}
	}

	// Usage is per target per day. Monthly totals are a query over this, so
	// the two grains cannot disagree.
	if rec.Calls > 0 || rec.InputTokens > 0 || rec.OutputTokens > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO usage_day (target_id, day, calls, input_tokens, output_tokens)
			VALUES (?, date('now'), ?, ?, ?)
			ON CONFLICT (target_id, day) DO UPDATE SET
				calls = calls + excluded.calls,
				input_tokens = input_tokens + excluded.input_tokens,
				output_tokens = output_tokens + excluded.output_tokens`,
			rec.TargetID, rec.Calls, rec.InputTokens, rec.OutputTokens); err != nil {
			return 0, fmt.Errorf("record usage: %w", err)
		}
	}

	if rec.EvaluationID > 0 {
		column := "completed"
		if rec.Status == ChatFailed {
			column = "failed"
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE evaluation SET `+column+` = `+column+` + 1 WHERE id = ?`, rec.EvaluationID); err != nil {
			return 0, fmt.Errorf("bump evaluation counter: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return chatID, nil
}

// UsageDay is one target's usage on one day.
type UsageDay struct {
	TargetID     int64
	TargetSpec   string
	Day          string
	Calls        int
	InputTokens  int
	OutputTokens int
}

// Usage returns per-target-per-day usage over a trailing window, newest first.
// There is no currency column anywhere: you reconcile these against your own
// provider bill.
func (db *DB) Usage(ctx context.Context, days int) ([]UsageDay, error) {
	if days <= 0 {
		days = 30
	}
	rows, err := db.QueryContext(ctx, `
		SELECT u.target_id, COALESCE(t.spec, ''), u.day, u.calls, u.input_tokens, u.output_tokens
		FROM usage_day u
		LEFT JOIN target t ON t.id = u.target_id
		WHERE u.day >= date('now', ?)
		ORDER BY u.day DESC, u.target_id`, fmt.Sprintf("-%d days", days-1))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UsageDay
	for rows.Next() {
		var u UsageDay
		if err := rows.Scan(&u.TargetID, &u.TargetSpec, &u.Day, &u.Calls, &u.InputTokens, &u.OutputTokens); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func nullableID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}
