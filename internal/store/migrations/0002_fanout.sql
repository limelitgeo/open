-- Query fan-out: the searches an engine actually ran while grounding an
-- answer, in the order it ran them.
--
-- This is separate from citation because the two answer different questions.
-- A citation is a source the engine decided to attribute in its answer; a
-- fan-out query is what it went looking for, which is frequently not the
-- question the user typed. A brand can lose because the engine searched for
-- something it never thought to search for, and nothing else in this schema
-- would show that.
--
-- Not every provider exposes it. An answer with no rows here means the
-- provider did not say, not that the engine searched for nothing.
CREATE TABLE fanout (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id  INTEGER NOT NULL REFERENCES chat (id) ON DELETE CASCADE,
    -- query is stored verbatim. It is the engine's words, not the user's, and
    -- normalising it would destroy the comparison that makes it useful.
    query    TEXT    NOT NULL,
    -- position is 1-based, in the order the engine ran the searches.
    position INTEGER NOT NULL
);

CREATE INDEX idx_fanout_chat ON fanout (chat_id);
