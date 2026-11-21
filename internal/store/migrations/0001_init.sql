-- SPDX-License-Identifier: Apache-2.0
--
-- 0001_init: the core schema (docs/DESIGN.md §7.2).
--
-- Conventions:
--   * STRICT tables: SQLite enforces column types.
--   * Text IDs are UUIDv7 strings; child rows use INTEGER rowids.
--   * Timestamps are UTC RFC 3339 text with milliseconds, e.g. 2026-11-20T21:04:05.123Z.
--   * Optional text is '' rather than NULL, except where NULL means "absent"
--     (import_key, original_text, raw_source, deleted_at, ...).
--   * Recipes are soft-deleted (deleted_at); a hard delete (trash purge)
--     cascades to every child row.
-- The full-text index (recipes_fts) arrives with search in P1.7.

CREATE TABLE users (
    id           TEXT PRIMARY KEY,
    remote_user  TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    email        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
) STRICT;

CREATE TABLE recipes (
    id              TEXT PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,
    title           TEXT NOT NULL CHECK (title <> ''),
    description     TEXT NOT NULL DEFAULT '',

    yield_text      TEXT NOT NULL DEFAULT '',
    servings        REAL CHECK (servings IS NULL OR servings > 0),
    prep_min        INTEGER CHECK (prep_min IS NULL OR prep_min >= 0),
    cook_min        INTEGER CHECK (cook_min IS NULL OR cook_min >= 0),
    total_min       INTEGER CHECK (total_min IS NULL OR total_min >= 0),

    notes           TEXT NOT NULL DEFAULT '',

    -- Provenance (§7.1).
    source_kind     TEXT NOT NULL CHECK (source_kind IN ('manual', 'url', 'capture', 'bundle', 'legacy')),
    source_url      TEXT NOT NULL DEFAULT '',
    canonical_url   TEXT,                 -- dedupe key for URL imports (§8.2 step 10)
    source_site     TEXT NOT NULL DEFAULT '',
    author          TEXT NOT NULL DEFAULT '',
    contributed_by  TEXT NOT NULL DEFAULT '',
    collection      TEXT NOT NULL DEFAULT '',

    -- Idempotent bulk/legacy import (§8.4).
    import_key      TEXT UNIQUE,
    import_revision INTEGER,              -- revision written by the last import

    -- Never destroy (§7.1).
    original_text   TEXT,
    raw_source      TEXT CHECK (raw_source IS NULL OR json_valid(raw_source)),

    needs_review    INTEGER NOT NULL DEFAULT 0 CHECK (needs_review IN (0, 1)),
    review_reasons  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(review_reasons) AND json_type(review_reasons) = 'array'),

    created_by      TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    deleted_at      TEXT,
    revision        INTEGER NOT NULL DEFAULT 1 CHECK (revision >= 1)
) STRICT;

CREATE INDEX recipes_canonical_url ON recipes (canonical_url) WHERE canonical_url IS NOT NULL;
CREATE INDEX recipes_deleted_at    ON recipes (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX recipes_needs_review  ON recipes (needs_review) WHERE needs_review = 1;
CREATE INDEX recipes_contributor   ON recipes (contributed_by) WHERE contributed_by <> '';
CREATE INDEX recipes_collection    ON recipes (collection) WHERE collection <> '';
CREATE INDEX recipes_updated_at    ON recipes (updated_at);

CREATE TABLE ingredient_groups (
    id        INTEGER PRIMARY KEY,
    recipe_id TEXT NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    position  INTEGER NOT NULL CHECK (position >= 0),
    name      TEXT NOT NULL DEFAULT '',       -- '' for the unnamed group
    UNIQUE (recipe_id, position)
) STRICT;

-- Raw text is canonical; parsed fields are derived and may be recomputed (D-06).
CREATE TABLE ingredients (
    id        INTEGER PRIMARY KEY,
    group_id  INTEGER NOT NULL REFERENCES ingredient_groups (id) ON DELETE CASCADE,
    position  INTEGER NOT NULL CHECK (position >= 0),
    raw_text  TEXT NOT NULL,
    qty_min   REAL,
    qty_max   REAL,
    unit      TEXT,
    size_text TEXT,                            -- "8-oz" in "1 8-oz pkg cream cheese"
    item      TEXT,
    note      TEXT,
    parse_ok  INTEGER NOT NULL DEFAULT 0 CHECK (parse_ok IN (0, 1)),
    UNIQUE (group_id, position),
    CHECK (qty_max IS NULL OR qty_min IS NULL OR qty_max >= qty_min)
) STRICT;

CREATE TABLE step_groups (
    id        INTEGER PRIMARY KEY,
    recipe_id TEXT NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    position  INTEGER NOT NULL CHECK (position >= 0),
    name      TEXT NOT NULL DEFAULT '',
    UNIQUE (recipe_id, position)
) STRICT;

CREATE TABLE steps (
    id       INTEGER PRIMARY KEY,
    group_id INTEGER NOT NULL REFERENCES step_groups (id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0),
    text     TEXT NOT NULL,
    UNIQUE (group_id, position)
) STRICT;

-- Hierarchical categories ("Desserts" > "Cookies"). The same name can appear
-- at different levels (a top-level "Fruits" and "Desserts" > "Fruits"), so
-- uniqueness is on the full slug path, not the slug.
CREATE TABLE categories (
    id        INTEGER PRIMARY KEY,
    parent_id INTEGER REFERENCES categories (id) ON DELETE RESTRICT,
    name      TEXT NOT NULL CHECK (name <> ''),
    slug      TEXT NOT NULL CHECK (slug <> ''),
    path      TEXT NOT NULL UNIQUE             -- "desserts/cookies"
) STRICT;

CREATE TABLE recipe_categories (
    recipe_id   TEXT NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    category_id INTEGER NOT NULL REFERENCES categories (id) ON DELETE RESTRICT,
    PRIMARY KEY (recipe_id, category_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX recipe_categories_category ON recipe_categories (category_id);

CREATE TABLE tags (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL CHECK (name <> ''),
    slug TEXT NOT NULL UNIQUE
) STRICT;

CREATE TABLE recipe_tags (
    recipe_id TEXT NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    tag_id    INTEGER NOT NULL REFERENCES tags (id) ON DELETE RESTRICT,
    PRIMARY KEY (recipe_id, tag_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX recipe_tags_tag ON recipe_tags (tag_id);

-- Files live on disk at images/<aa>/<bb>/<sha256>.<ext> (§7.2, §8.5).
CREATE TABLE images (
    id         TEXT PRIMARY KEY,
    recipe_id  TEXT NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    sha256     TEXT NOT NULL CHECK (length(sha256) = 64),
    mime       TEXT NOT NULL CHECK (mime IN ('image/jpeg', 'image/png')),
    width      INTEGER NOT NULL CHECK (width > 0),
    height     INTEGER NOT NULL CHECK (height > 0),
    position   INTEGER NOT NULL CHECK (position >= 0),
    is_primary INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
    source_url TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE (recipe_id, position)
) STRICT;

CREATE UNIQUE INDEX images_one_primary ON images (recipe_id) WHERE is_primary = 1;
CREATE INDEX images_sha256 ON images (sha256);

-- A full snapshot on every save (FR-10).
CREATE TABLE revisions (
    recipe_id TEXT NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    revision  INTEGER NOT NULL CHECK (revision >= 1),
    snapshot  TEXT NOT NULL CHECK (json_valid(snapshot)),
    edited_by TEXT REFERENCES users (id) ON DELETE SET NULL,
    edited_at TEXT NOT NULL,
    PRIMARY KEY (recipe_id, revision)
) STRICT, WITHOUT ROWID;

CREATE TABLE favorites (
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    recipe_id  TEXT NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, recipe_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX favorites_recipe ON favorites (recipe_id);

-- Capture-scoped bearer tokens (§8.3); only a hash is stored.
CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL CHECK (name <> ''),
    hash         TEXT NOT NULL UNIQUE,
    scope        TEXT NOT NULL CHECK (scope IN ('capture')),
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
) STRICT;

CREATE INDEX api_tokens_user ON api_tokens (user_id);
