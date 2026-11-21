-- SPDX-License-Identifier: Apache-2.0
--
-- 0002_search: full-text search (docs/DESIGN.md §10).
--
-- One FTS5 row per recipe. recipe_id is stored but not indexed; it links a
-- row back to recipes (whose key is text, so it can't be the FTS rowid).
-- The store rewrites a recipe's row on every save, in the same transaction;
-- the trigger below removes it when a recipe is purged. Trashed recipes
-- stay indexed and are filtered out by the query.

CREATE VIRTUAL TABLE recipes_fts USING fts5(
    recipe_id UNINDEXED,
    title,
    ingredients,
    tags,          -- tags and category names
    contributor,   -- contributed_by, author, collection
    body,          -- description, steps, notes
    tokenize = 'unicode61 remove_diacritics 2',
    prefix = '2 3'
);

CREATE TRIGGER recipes_fts_purge AFTER DELETE ON recipes BEGIN
    DELETE FROM recipes_fts WHERE recipe_id = old.id;
END;

-- Index the recipes that already exist.
INSERT INTO recipes_fts (recipe_id, title, ingredients, tags, contributor, body)
SELECT r.id,
       r.title,
       COALESCE((SELECT group_concat(i.raw_text, char(10))
                 FROM ingredient_groups g JOIN ingredients i ON i.group_id = g.id
                 WHERE g.recipe_id = r.id), ''),
       COALESCE((SELECT group_concat(t.name, ' ') FROM recipe_tags rt JOIN tags t ON t.id = rt.tag_id
                 WHERE rt.recipe_id = r.id), '') || ' ' ||
       COALESCE((SELECT group_concat(c.name, ' ') FROM recipe_categories rc JOIN categories c ON c.id = rc.category_id
                 WHERE rc.recipe_id = r.id), ''),
       r.contributed_by || ' ' || r.author || ' ' || r.collection,
       r.description || char(10) ||
       COALESCE((SELECT group_concat(s.text, char(10))
                 FROM step_groups g JOIN steps s ON s.group_id = g.id
                 WHERE g.recipe_id = r.id), '') || char(10) || r.notes
FROM recipes r;
