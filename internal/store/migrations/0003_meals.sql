-- SPDX-License-Identifier: Apache-2.0
--
-- 0003_meals: meals and their recipes (docs/DESIGN.md §7.2, D-22).
--
-- A meal is a named, household-shared group of recipes ("Thanksgiving").
-- Each entry keeps the scale it is cooked at; that factor is the only
-- stored scaling (§9.2). The shopping list is computed, never stored.
-- Meals are soft-deleted (undo), and purged with the recipe trash.

CREATE TABLE meals (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL CHECK (name <> ''),
    notes      TEXT NOT NULL DEFAULT '',
    created_by TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    deleted_at TEXT
) STRICT;

CREATE INDEX meals_updated_at ON meals (updated_at);

-- One row per recipe per meal. Positions order the meal page; they are not
-- unique, so a reorder can swap two rows without a temporary value.
CREATE TABLE meal_recipes (
    meal_id   TEXT NOT NULL REFERENCES meals (id) ON DELETE CASCADE,
    recipe_id TEXT NOT NULL REFERENCES recipes (id) ON DELETE CASCADE,
    factor    REAL NOT NULL DEFAULT 1 CHECK (factor > 0 AND factor <= 100),
    position  INTEGER NOT NULL CHECK (position >= 0),
    added_at  TEXT NOT NULL,
    PRIMARY KEY (meal_id, recipe_id)
) STRICT, WITHOUT ROWID;

CREATE INDEX meal_recipes_recipe ON meal_recipes (recipe_id);
