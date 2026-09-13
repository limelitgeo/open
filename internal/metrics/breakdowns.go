// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

package metrics

// Breakdowns: the same answers cut by brand, by engine, by day and by source
// type, which is what turns one number into a picture.
//
// Everything here is a query over the rows the runner wrote and shares the
// two denominator rules in metrics.go. The competitor race, the engine grid
// and the citation mix are all aggregations a reader can re-derive by hand
// from the answers screen.

import (
	"context"
	"sort"
	"strings"
)

// BrandDay is one brand on one day.
type BrandDay struct {
	Day        string
	Name       string
	IsOwn      bool
	Visibility float64
	Answers    int
	Mentions   int
}

// BrandSeries is every tracked brand's visibility per day: the competitor
// race. The property is always present, even at zero, because a race chart
// without your own line on it does not answer the question you opened it for.
func (s *Service) BrandSeries(ctx context.Context, w Window) ([]BrandDay, error) {
	filter, args := w.where("c")

	// Answers per day are the shared denominator for every brand that day.
	dayTotals := map[string]int{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT date(c.created_at) AS day, COUNT(*)
		FROM chat c JOIN prompt p ON p.id = c.prompt_id
		WHERE `+filter+`
		GROUP BY day`, args...)
	if err != nil {
		return nil, err
	}
	var days []string
	for rows.Next() {
		var day string
		var n int
		if err := rows.Scan(&day, &n); err != nil {
			rows.Close()
			return nil, err
		}
		dayTotals[day] = n
		days = append(days, day)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(days)

	// Mentions per brand per day, counting answers not occurrences.
	type key struct{ day, name string }
	byKey := map[key]*BrandDay{}
	names := map[string]bool{}

	mrows, err := s.db.QueryContext(ctx, `
		SELECT date(c.created_at) AS day, m.brand_name, m.competitor_id IS NULL,
		       COUNT(DISTINCT c.id), COUNT(*)
		FROM mention m
		JOIN chat c ON c.id = m.chat_id
		JOIN prompt p ON p.id = c.prompt_id
		WHERE `+filter+`
		GROUP BY day, m.brand_name, m.competitor_id IS NULL`, args...)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var b BrandDay
		if err := mrows.Scan(&b.Day, &b.Name, &b.IsOwn, &b.Answers, &b.Mentions); err != nil {
			return nil, err
		}
		names[b.Name] = true
		b.Visibility = ratio(b.Answers, dayTotals[b.Day])
		byKey[key{b.Day, b.Name}] = &b
	}
	if err := mrows.Err(); err != nil {
		return nil, err
	}

	// The property must appear even with no mentions.
	property, err := s.db.Property(ctx)
	if err == nil && property.Name != "" {
		names[property.Name] = true
	}
	ownName := property.Name

	// Every brand on every day, with zeros filled in only for days that
	// actually ran: a brand absent on a measured day is a real zero, and a
	// day nobody measured is not drawn at all.
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	out := make([]BrandDay, 0, len(days)*len(sorted))
	for _, day := range days {
		for _, name := range sorted {
			if b, ok := byKey[key{day, name}]; ok {
				b.Answers = dayTotals[day]
				out = append(out, *b)
				continue
			}
			out = append(out, BrandDay{
				Day: day, Name: name, IsOwn: name == ownName,
				Visibility: 0, Answers: dayTotals[day],
			})
		}
	}
	return out, nil
}

// EngineCell is one brand on one engine.
type EngineCell struct {
	Engine     string
	Access     string
	Name       string
	IsOwn      bool
	Visibility float64
	Answers    int
	Mentions   int
}

// EngineBreakdown is every tracked brand's visibility on every engine: where
// each of you wins. It needs no time depth, so it is rich on the first day.
func (s *Service) EngineBreakdown(ctx context.Context, w Window) ([]EngineCell, error) {
	filter, args := w.where("c")

	type ekey struct{ engine, access string }
	totals := map[ekey]int{}
	var engines []ekey

	rows, err := s.db.QueryContext(ctx, `
		SELECT t.engine, t.access, COUNT(*)
		FROM chat c
		JOIN prompt p ON p.id = c.prompt_id
		JOIN target t ON t.id = c.target_id
		WHERE `+filter+`
		GROUP BY t.engine, t.access
		ORDER BY t.access, t.engine`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k ekey
		var n int
		if err := rows.Scan(&k.engine, &k.access, &n); err != nil {
			rows.Close()
			return nil, err
		}
		totals[k] = n
		engines = append(engines, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type bkey struct {
		engine, access, name string
	}
	byKey := map[bkey]*EngineCell{}
	names := map[string]bool{}

	mrows, err := s.db.QueryContext(ctx, `
		SELECT t.engine, t.access, m.brand_name, m.competitor_id IS NULL,
		       COUNT(DISTINCT c.id), COUNT(*)
		FROM mention m
		JOIN chat c ON c.id = m.chat_id
		JOIN prompt p ON p.id = c.prompt_id
		JOIN target t ON t.id = c.target_id
		WHERE `+filter+`
		GROUP BY t.engine, t.access, m.brand_name, m.competitor_id IS NULL`, args...)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var c EngineCell
		if err := mrows.Scan(&c.Engine, &c.Access, &c.Name, &c.IsOwn, &c.Answers, &c.Mentions); err != nil {
			return nil, err
		}
		names[c.Name] = true
		byKey[bkey{c.Engine, c.Access, c.Name}] = &c
	}
	if err := mrows.Err(); err != nil {
		return nil, err
	}

	property, _ := s.db.Property(ctx)
	if property.Name != "" {
		names[property.Name] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	out := make([]EngineCell, 0, len(engines)*len(sorted))
	for _, e := range engines {
		total := totals[e]
		for _, name := range sorted {
			if c, ok := byKey[bkey{e.engine, e.access, name}]; ok {
				c.Visibility = ratio(c.Answers, total)
				c.Answers = total
				out = append(out, *c)
				continue
			}
			out = append(out, EngineCell{
				Engine: e.engine, Access: e.access, Name: name,
				IsOwn: name == property.Name, Answers: total,
			})
		}
	}
	return out, nil
}

// SourceTypeDay is one source class on one day.
type SourceTypeDay struct {
	Day        string
	SourceType string
	Citations  int
}

// CitationMix is citations per source type per day: who the engines trust,
// and whether that is changing.
func (s *Service) CitationMix(ctx context.Context, w Window) ([]SourceTypeDay, error) {
	filter, args := w.where("c")
	rows, err := s.db.QueryContext(ctx, `
		SELECT date(c.created_at) AS day, ct.source_type, COUNT(*)
		FROM citation ct
		JOIN chat c ON c.id = ct.chat_id
		JOIN prompt p ON p.id = c.prompt_id
		WHERE `+filter+`
		GROUP BY day, ct.source_type
		ORDER BY day, ct.source_type`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SourceTypeDay
	for rows.Next() {
		var d SourceTypeDay
		if err := rows.Scan(&d.Day, &d.SourceType, &d.Citations); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Word is one term from the engines' own searches.
type Word struct {
	Term  string
	Count int
}

// FanOutWords counts the terms the engines searched for while grounding,
// across every answer in the window. It is the vocabulary of the questions
// the engines actually asked on your behalf, which is often not the
// vocabulary you used.
func (s *Service) FanOutWords(ctx context.Context, w Window, limit int) ([]Word, error) {
	if limit <= 0 {
		limit = 40
	}
	filter, args := w.where("c")
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.query FROM fanout f
		JOIN chat c ON c.id = f.chat_id
		JOIN prompt p ON p.id = c.prompt_id
		WHERE `+filter, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			return nil, err
		}
		for _, term := range strings.Fields(strings.ToLower(q)) {
			term = strings.Trim(term, `.,;:"'()[]?!`)
			if len(term) < 3 || stopword[term] {
				continue
			}
			counts[term]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]Word, 0, len(counts))
	for term, n := range counts {
		out = append(out, Word{Term: term, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Term < out[j].Term
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// stopword lists the terms that carry no signal in a search query.
var stopword = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "are": true, "what": true,
	"which": true, "best": true, "how": true, "does": true, "your": true, "you": true,
	"from": true, "that": true, "this": true, "into": true, "top": true, "vs": true,
	"tools": true, "tool": true, "2025": true, "2026": true, "official": true,
}
