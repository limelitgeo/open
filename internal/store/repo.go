package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned when a row a caller named does not exist.
var ErrNotFound = errors.New("store: not found")

// Property is the brand this instance tracks. One per instance in v0.1.
type Property struct {
	Name    string
	Domain  string
	Aliases []string
}

// Names returns every string that counts as this brand in an answer: its
// name, its aliases and its domain. The mention matcher searches for these
// and nothing else, which is what makes the count reproducible.
func (p Property) Names() []string {
	out := make([]string, 0, len(p.Aliases)+2)
	if p.Name != "" {
		out = append(out, p.Name)
	}
	out = append(out, p.Aliases...)
	if p.Domain != "" {
		out = append(out, p.Domain)
	}
	return out
}

// Competitor is one tracked rival. Domain is the identity key.
type Competitor struct {
	ID       int64
	Name     string
	Domain   string
	Category string
}

// Prompt is one tracked question.
type Prompt struct {
	ID              int64
	Text            string
	Category        string
	LocationCountry string
	// Branded is set when the prompt names the property itself. Those
	// prompts are excluded from the headline number, because asking an
	// engine about yourself and counting the answer proves nothing.
	Branded bool
	Tags    []string
	Active  bool
}

// Target is a stored target row.
type Target struct {
	ID       int64
	Spec     string
	Engine   string
	Provider string
	Model    string
	Online   bool
	Access   string
	Enabled  bool
}

// SaveProperty upserts the single property row.
func (db *DB) SaveProperty(ctx context.Context, p Property) error {
	aliases, err := json.Marshal(nonEmpty(p.Aliases))
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO property (id, name, domain, aliases, updated_at)
		VALUES (1, ?, ?, ?, datetime('now'))
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name,
			domain = excluded.domain,
			aliases = excluded.aliases,
			updated_at = datetime('now')`,
		strings.TrimSpace(p.Name), normalizeDomain(p.Domain), string(aliases))
	return err
}

// Property returns the configured property, or ErrNotFound before setup.
func (db *DB) Property(ctx context.Context) (Property, error) {
	var (
		p       Property
		aliases string
	)
	err := db.QueryRowContext(ctx, `SELECT name, domain, aliases FROM property WHERE id = 1`).
		Scan(&p.Name, &p.Domain, &aliases)
	if errors.Is(err, sql.ErrNoRows) {
		return Property{}, ErrNotFound
	}
	if err != nil {
		return Property{}, err
	}
	if err := json.Unmarshal([]byte(aliases), &p.Aliases); err != nil {
		return Property{}, fmt.Errorf("decode aliases: %w", err)
	}
	return p, nil
}

// AddCompetitor inserts a competitor. The domain is the identity key, so a
// repeated domain updates the existing row rather than creating a second one
// that would split every count between them.
func (db *DB) AddCompetitor(ctx context.Context, c Competitor) (int64, error) {
	domain := normalizeDomain(c.Domain)
	if domain == "" {
		return 0, errors.New("competitor needs a domain, which is its identity key")
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO competitor (name, domain, category) VALUES (?, ?, ?)
		ON CONFLICT (domain) DO UPDATE SET
			name = excluded.name,
			category = excluded.category`,
		strings.TrimSpace(c.Name), domain, strings.TrimSpace(c.Category))
	if err != nil {
		return 0, err
	}
	if id, err := res.LastInsertId(); err == nil && id > 0 {
		return id, nil
	}
	var id int64
	err = db.QueryRowContext(ctx, `SELECT id FROM competitor WHERE domain = ?`, domain).Scan(&id)
	return id, err
}

// Competitors lists tracked competitors, newest last.
func (db *DB) Competitors(ctx context.Context) ([]Competitor, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, domain, category FROM competitor ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Competitor
	for rows.Next() {
		var c Competitor
		if err := rows.Scan(&c.ID, &c.Name, &c.Domain, &c.Category); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteCompetitor removes a competitor. Past mentions cascade with it.
func (db *DB) DeleteCompetitor(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM competitor WHERE id = ?`, id)
	return err
}

// AddPrompt inserts a prompt, deriving the branded tag from the property.
func (db *DB) AddPrompt(ctx context.Context, p Prompt) (int64, error) {
	text := strings.TrimSpace(p.Text)
	if text == "" {
		return 0, errors.New("prompt text is empty")
	}
	tags, err := json.Marshal(nonEmpty(p.Tags))
	if err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO prompt (text, category, location_country, branded, tags, active)
		VALUES (?, ?, ?, ?, ?, ?)`,
		text, strings.TrimSpace(p.Category), strings.TrimSpace(p.LocationCountry),
		boolToInt(p.Branded), string(tags), boolToInt(p.Active))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Prompts lists prompts. Inactive ones are included only when asked for,
// matching list_prompts in the MCP catalog.
func (db *DB) Prompts(ctx context.Context, includeInactive bool) ([]Prompt, error) {
	query := `SELECT id, text, category, location_country, branded, tags, active FROM prompt`
	if !includeInactive {
		query += ` WHERE active = 1`
	}
	query += ` ORDER BY id`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Prompt
	for rows.Next() {
		var (
			p       Prompt
			branded int
			active  int
			tags    string
		)
		if err := rows.Scan(&p.ID, &p.Text, &p.Category, &p.LocationCountry, &branded, &tags, &active); err != nil {
			return nil, err
		}
		p.Branded, p.Active = branded == 1, active == 1
		if err := json.Unmarshal([]byte(tags), &p.Tags); err != nil {
			return nil, fmt.Errorf("decode tags for prompt %d: %w", p.ID, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetPromptActive toggles a prompt.
func (db *DB) SetPromptActive(ctx context.Context, id int64, active bool) error {
	_, err := db.ExecContext(ctx, `UPDATE prompt SET active = ?, updated_at = datetime('now') WHERE id = ?`, boolToInt(active), id)
	return err
}

// DeletePrompt removes a prompt. Its chats cascade with it.
func (db *DB) DeletePrompt(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM prompt WHERE id = ?`, id)
	return err
}

// AddTarget inserts a target, or re-enables the one already stored under the
// same spec.
func (db *DB) AddTarget(ctx context.Context, t Target) (int64, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO target (spec, engine, provider, model, online, access, enabled)
		VALUES (?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT (spec) DO UPDATE SET enabled = 1`,
		t.Spec, t.Engine, t.Provider, t.Model, boolToInt(t.Online), t.Access)
	if err != nil {
		return 0, err
	}
	if id, err := res.LastInsertId(); err == nil && id > 0 {
		return id, nil
	}
	var id int64
	err = db.QueryRowContext(ctx, `SELECT id FROM target WHERE spec = ?`, t.Spec).Scan(&id)
	return id, err
}

// Targets lists stored targets.
func (db *DB) Targets(ctx context.Context, enabledOnly bool) ([]Target, error) {
	query := `SELECT id, spec, engine, provider, model, online, access, enabled FROM target`
	if enabledOnly {
		query += ` WHERE enabled = 1`
	}
	query += ` ORDER BY id`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Target
	for rows.Next() {
		var (
			t              Target
			online, active int
		)
		if err := rows.Scan(&t.ID, &t.Spec, &t.Engine, &t.Provider, &t.Model, &online, &t.Access, &active); err != nil {
			return nil, err
		}
		t.Online, t.Enabled = online == 1, active == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteTarget removes a target. Its chats cascade with it.
func (db *DB) DeleteTarget(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM target WHERE id = ?`, id)
	return err
}

// Setting reads one setting, returning "" when unset.
func (db *DB) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting writes one setting.
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = datetime('now')`,
		key, value)
	return err
}

// Counts is the cheap summary the shell needs on every page.
type Counts struct {
	Prompts     int
	Competitors int
	Targets     int
	Chats       int
	LastChatAt  string
}

// Counts returns the row counts behind the sidebar and the empty states.
func (db *DB) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	var last sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM prompt WHERE active = 1),
			(SELECT COUNT(*) FROM competitor),
			(SELECT COUNT(*) FROM target WHERE enabled = 1),
			(SELECT COUNT(*) FROM chat),
			(SELECT MAX(created_at) FROM chat)`).
		Scan(&c.Prompts, &c.Competitors, &c.Targets, &c.Chats, &last)
	if err != nil {
		return Counts{}, err
	}
	if last.Valid {
		c.LastChatAt = last.String
	}
	return c, nil
}

// RunsToday counts chats recorded today, which is what runs_per_day caps.
func (db *DB) RunsToday(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chat WHERE date(created_at) = date('now')`).Scan(&n)
	return n, err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// normalizeDomain strips the scheme, a leading www and any path, so
// "https://www.acme.com/pricing" and "acme.com" are one identity. Without
// this the competitor table would hold several rows for one company and
// split every count between them.
func normalizeDomain(raw string) string {
	d := strings.TrimSpace(strings.ToLower(raw))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	d = strings.TrimPrefix(d, "www.")
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	return strings.TrimSuffix(d, ".")
}

// NormalizeDomain is the exported form, used by the UI so what a user typed
// and what is stored agree on screen.
func NormalizeDomain(raw string) string { return normalizeDomain(raw) }

// Now is the clock the store stamps rows with, exposed so tests can assert
// against a fixed time without reaching into SQLite's own datetime('now').
var Now = func() time.Time { return time.Now().UTC() }
