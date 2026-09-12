// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package metrics

// chats.go — the evidence surface: the answers themselves.
//
// Every number in metrics.go is a count of these rows, so this is where a
// user or an agent goes to check one. A headline nobody can drill into is a
// number nobody can act on: "you are cited 40% of the time" only becomes work
// when you can read the three answers that left you out.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"
)

// previewRunes is how much of an answer the list carries. Enough to recognise
// which answer a row is, short enough that a hundred rows are not a hundred
// full answers travelling through an MCP response.
const previewRunes = 220

// ChatFilter narrows the answer list. A zero filter means every answer,
// newest first.
type ChatFilter struct {
	PromptID int64
	TargetID int64
	// Status is 'ok', 'failed' or 'no_answer_surface'; empty means any.
	Status string
	// Mentioned narrows to answers that did or did not name the property.
	// Nil means both, which is the default and the honest one: the answers
	// that left you out are the interesting half.
	Mentioned *bool
	Days      int
	Limit     int
	Offset    int
}

// ChatSummary is one answer without its body.
type ChatSummary struct {
	ID        int64
	PromptID  int64
	Prompt    string
	TargetID  int64
	Target    string
	Engine    string
	Access    string
	Status    string
	Model     string
	Error     string
	CreatedAt string
	Preview   string
	Mentioned bool
	Mentions  int
	Citations int
	// Position is the property's rank in this answer, zero when it is absent
	// or unranked.
	Position float64
}

// ChatDetail is one answer in full, with everything derived from it.
type ChatDetail struct {
	ChatSummary
	Text    string
	Brands  []ChatBrand
	Sources []ChatSource
	// FanOut is what the engine searched for on the way to this answer, in
	// its order. Often it is not the question that was asked, and that gap is
	// the most actionable thing on the screen: it names the query you would
	// have to win.
	FanOut    []string
	InputTok  int
	OutputTok int
	Calls     int
}

// ChatBrand is one brand found in one answer.
type ChatBrand struct {
	Name     string
	IsOwn    bool
	Position int
	Offset   int
}

// ChatSource is one citation from one answer.
type ChatSource struct {
	URL        string
	Site       string
	Title      string
	Position   int
	SourceType string
}

// where turns a filter into SQL. Shared by the list and the count so a row
// can never appear in one and not the other.
func (f ChatFilter) where() (string, []any) {
	clauses := []string{"1 = 1"}
	var args []any
	if f.PromptID > 0 {
		clauses, args = append(clauses, "c.prompt_id = ?"), append(args, f.PromptID)
	}
	if f.TargetID > 0 {
		clauses, args = append(clauses, "c.target_id = ?"), append(args, f.TargetID)
	}
	if f.Status != "" {
		clauses, args = append(clauses, "c.status = ?"), append(args, f.Status)
	}
	if f.Days > 0 {
		clauses = append(clauses, "c.created_at >= datetime('now', ?)")
		args = append(args, fmt.Sprintf("-%d days", f.Days))
	}
	if f.Mentioned != nil {
		if *f.Mentioned {
			clauses = append(clauses, "own.n > 0")
		} else {
			clauses = append(clauses, "COALESCE(own.n, 0) = 0")
		}
	}
	return strings.Join(clauses, " AND "), args
}

// ownMentionJoin counts the property's own mentions per answer. Both the list
// and the count join it, because the Mentioned filter reads from it.
const ownMentionJoin = `
	LEFT JOIN (
		SELECT chat_id, COUNT(*) AS n, AVG(list_rank) AS pos
		FROM mention WHERE competitor_id IS NULL GROUP BY chat_id
	) own ON own.chat_id = c.id`

// Chats lists answers, newest first.
func (s *Service) Chats(ctx context.Context, f ChatFilter) ([]ChatSummary, error) {
	filter, args := f.where()

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	args = append(args, limit, f.Offset)

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.prompt_id, p.text, c.target_id, t.spec, t.engine, t.access,
		       c.status, c.model, c.error, c.created_at, c.text,
		       COALESCE(own.n, 0), COALESCE(all_m.n, 0), COALESCE(cites.n, 0), own.pos
		FROM chat c
		JOIN prompt p ON p.id = c.prompt_id
		JOIN target t ON t.id = c.target_id`+ownMentionJoin+`
		LEFT JOIN (SELECT chat_id, COUNT(*) AS n FROM mention GROUP BY chat_id) all_m ON all_m.chat_id = c.id
		LEFT JOIN (SELECT chat_id, COUNT(*) AS n FROM citation GROUP BY chat_id) cites ON cites.chat_id = c.id
		WHERE `+filter+`
		ORDER BY c.created_at DESC, c.id DESC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ChatSummary
	for rows.Next() {
		var (
			c       ChatSummary
			text    string
			ownN    int
			meanPos sql.NullFloat64
		)
		if err := rows.Scan(&c.ID, &c.PromptID, &c.Prompt, &c.TargetID, &c.Target, &c.Engine, &c.Access,
			&c.Status, &c.Model, &c.Error, &c.CreatedAt, &text,
			&ownN, &c.Mentions, &c.Citations, &meanPos); err != nil {
			return nil, err
		}
		c.Mentioned = ownN > 0
		c.Preview = preview(text)
		if meanPos.Valid {
			c.Position = round2(meanPos.Float64)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountChats returns how many answers match a filter, for paging.
//
// A real count, not the length of a capped page: paging off a number that
// stops at the page size hides every answer past it.
func (s *Service) CountChats(ctx context.Context, f ChatFilter) (int, error) {
	filter, args := f.where()
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM chat c`+ownMentionJoin+`
		WHERE `+filter, args...).Scan(&n)
	return n, err
}

// Chat returns one answer in full.
func (s *Service) Chat(ctx context.Context, id int64) (ChatDetail, error) {
	var d ChatDetail
	err := s.db.QueryRowContext(ctx, `
		SELECT c.id, c.prompt_id, p.text, c.target_id, t.spec, t.engine, t.access,
		       c.status, c.model, c.error, c.created_at, c.text,
		       c.input_tokens, c.output_tokens, c.calls
		FROM chat c
		JOIN prompt p ON p.id = c.prompt_id
		JOIN target t ON t.id = c.target_id
		WHERE c.id = ?`, id).Scan(
		&d.ID, &d.PromptID, &d.Prompt, &d.TargetID, &d.Target, &d.Engine, &d.Access,
		&d.Status, &d.Model, &d.Error, &d.CreatedAt, &d.Text,
		&d.InputTok, &d.OutputTok, &d.Calls)
	if err != nil {
		return d, err
	}
	d.Preview = preview(d.Text)

	brands, err := s.db.QueryContext(ctx, `
		SELECT brand_name, competitor_id IS NULL, COALESCE(list_rank, 0), offset_start
		FROM mention WHERE chat_id = ? ORDER BY offset_start`, id)
	if err != nil {
		return d, err
	}
	defer brands.Close()
	for brands.Next() {
		var b ChatBrand
		if err := brands.Scan(&b.Name, &b.IsOwn, &b.Position, &b.Offset); err != nil {
			return d, err
		}
		if b.IsOwn {
			d.Mentioned = true
		}
		d.Brands = append(d.Brands, b)
	}
	if err := brands.Err(); err != nil {
		return d, err
	}
	d.Mentions = len(d.Brands)

	sources, err := s.db.QueryContext(ctx, `
		SELECT url, site, title, position, source_type
		FROM citation WHERE chat_id = ? ORDER BY position`, id)
	if err != nil {
		return d, err
	}
	defer sources.Close()
	for sources.Next() {
		var c ChatSource
		if err := sources.Scan(&c.URL, &c.Site, &c.Title, &c.Position, &c.SourceType); err != nil {
			return d, err
		}
		d.Sources = append(d.Sources, c)
	}
	if err := sources.Err(); err != nil {
		return d, err
	}
	d.Citations = len(d.Sources)

	queries, err := s.db.QueryContext(ctx, `
		SELECT query FROM fanout WHERE chat_id = ? ORDER BY position`, id)
	if err != nil {
		return d, err
	}
	defer queries.Close()
	for queries.Next() {
		var q string
		if err := queries.Scan(&q); err != nil {
			return d, err
		}
		d.FanOut = append(d.FanOut, q)
	}
	return d, queries.Err()
}

// SourceURL is one cited page on a site.
type SourceURL struct {
	URL       string
	Title     string
	Citations int
	Answers   int
}

// SourceURLs lists the individual pages cited on one site, most cited first.
//
// This is the drill-down from Sources: the site tells you who is being cited,
// the URLs tell you what to write against.
func (s *Service) SourceURLs(ctx context.Context, site string, w Window, limit int) ([]SourceURL, error) {
	if limit <= 0 {
		limit = 50
	}
	filter, args := w.where("c")
	args = append([]any{strings.ToLower(strings.TrimSpace(site))}, args...)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, `
		SELECT ct.url, MAX(ct.title), COUNT(*), COUNT(DISTINCT c.id)
		FROM citation ct
		JOIN chat c ON c.id = ct.chat_id
		JOIN prompt p ON p.id = c.prompt_id
		WHERE (ct.site = ?1 OR ct.host = ?1) AND `+filter+`
		GROUP BY ct.url
		ORDER BY COUNT(*) DESC, ct.url
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SourceURL
	for rows.Next() {
		var u SourceURL
		if err := rows.Scan(&u.URL, &u.Title, &u.Citations, &u.Answers); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// preview clips an answer to a recognisable opening.
//
// Clipped on runes rather than bytes, because a byte cut lands mid-character
// on any answer containing an accent or an em dash and renders as a
// replacement glyph.
func preview(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= previewRunes {
		return text
	}
	cut := []rune(text)[:previewRunes]
	// Back up to a word boundary so the preview does not end mid-word.
	if i := strings.LastIndexByte(string(cut), ' '); i > previewRunes/2 {
		return string(cut)[:i] + "..."
	}
	return string(cut) + "..."
}
