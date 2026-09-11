-- Initial schema.
--
-- One property per instance in v0.1. Every table that hangs off an answer
-- cascades from `chat`, so deleting a chat leaves nothing orphaned.
--
-- What is deliberately NOT here: raw provider JSON, and any currency column.
-- The answer text, its citations and the usage counts are the record.

CREATE TABLE property (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    name          TEXT    NOT NULL,
    domain        TEXT    NOT NULL,
    aliases       TEXT    NOT NULL DEFAULT '[]', -- JSON array of strings
    created_at    TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE competitor (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT    NOT NULL,
    domain        TEXT    NOT NULL,
    category      TEXT    NOT NULL DEFAULT '',
    created_at    TEXT    NOT NULL DEFAULT (datetime('now'))
);
-- Domain is the identity key, so it is unique rather than the name.
CREATE UNIQUE INDEX competitor_domain_idx ON competitor (domain);

CREATE TABLE prompt (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    text             TEXT    NOT NULL,
    category         TEXT    NOT NULL DEFAULT '',
    location_country TEXT    NOT NULL DEFAULT '',
    -- `branded` is system-assigned: the prompt names the property itself, so
    -- an answer mentioning it proves nothing and the headline excludes it.
    branded          INTEGER NOT NULL DEFAULT 0 CHECK (branded IN (0, 1)),
    tags             TEXT    NOT NULL DEFAULT '[]', -- JSON array of strings
    active           INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    created_at       TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX prompt_active_idx ON prompt (active);

CREATE TABLE target (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    spec        TEXT    NOT NULL, -- engine:provider[:model][:online], as written
    engine      TEXT    NOT NULL,
    provider    TEXT    NOT NULL,
    model       TEXT    NOT NULL DEFAULT '',
    online      INTEGER NOT NULL DEFAULT 0 CHECK (online IN (0, 1)),
    -- An api answer and a scraped answer measure different surfaces, so the
    -- mode travels with every row that descends from this target.
    access      TEXT    NOT NULL CHECK (access IN ('api', 'scraped')),
    enabled     INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX target_spec_idx ON target (spec);

CREATE TABLE evaluation (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    status       TEXT    NOT NULL CHECK (status IN ('running', 'done', 'failed', 'cancelled')),
    planned      INTEGER NOT NULL DEFAULT 0,
    completed    INTEGER NOT NULL DEFAULT 0,
    failed       INTEGER NOT NULL DEFAULT 0,
    started_at   TEXT    NOT NULL DEFAULT (datetime('now')),
    finished_at  TEXT
);

CREATE TABLE chat (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    evaluation_id INTEGER REFERENCES evaluation (id) ON DELETE SET NULL,
    prompt_id     INTEGER NOT NULL REFERENCES prompt (id) ON DELETE CASCADE,
    target_id     INTEGER NOT NULL REFERENCES target (id) ON DELETE CASCADE,
    -- 'no_answer_surface' is not a miss for the brand: the surface itself did
    -- not render, so these rows are excluded from every metric denominator.
    status        TEXT    NOT NULL CHECK (status IN ('ok', 'failed', 'no_answer_surface')),
    text          TEXT    NOT NULL DEFAULT '',
    model         TEXT    NOT NULL DEFAULT '',
    error         TEXT    NOT NULL DEFAULT '',
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    calls         INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX chat_prompt_target_idx ON chat (prompt_id, target_id);
CREATE INDEX chat_created_idx ON chat (created_at);
CREATE INDEX chat_evaluation_idx ON chat (evaluation_id);

CREATE TABLE mention (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id       INTEGER NOT NULL REFERENCES chat (id) ON DELETE CASCADE,
    -- Exactly one of these identifies the brand: competitor_id is NULL for the
    -- property's own mentions.
    competitor_id INTEGER REFERENCES competitor (id) ON DELETE CASCADE,
    brand_key     TEXT    NOT NULL, -- normalized matching key
    brand_name    TEXT    NOT NULL, -- as matched in the answer
    offset_start  INTEGER NOT NULL,
    offset_end    INTEGER NOT NULL,
    list_rank     INTEGER, -- 1-based rank of the enclosing list item, NULL if not in a list
    created_at    TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX mention_chat_idx ON mention (chat_id);
CREATE INDEX mention_brand_idx ON mention (brand_key);

CREATE TABLE citation (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id     INTEGER NOT NULL REFERENCES chat (id) ON DELETE CASCADE,
    url         TEXT    NOT NULL, -- as the engine gave it
    host        TEXT    NOT NULL, -- normalized, www dropped
    site        TEXT    NOT NULL, -- registrable site
    title       TEXT    NOT NULL DEFAULT '',
    position    INTEGER NOT NULL DEFAULT 0,
    source_type TEXT    NOT NULL CHECK (source_type IN ('own', 'competitor', 'social', 'informational', 'other')),
    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX citation_chat_idx ON citation (chat_id);
CREATE INDEX citation_host_idx ON citation (host);

-- Usage is stored per target per DAY. Monthly totals are a query over this,
-- never a second table, so the two grains cannot disagree.
CREATE TABLE usage_day (
    target_id     INTEGER NOT NULL REFERENCES target (id) ON DELETE CASCADE,
    day           TEXT    NOT NULL, -- YYYY-MM-DD
    calls         INTEGER NOT NULL DEFAULT 0,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (target_id, day)
);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
