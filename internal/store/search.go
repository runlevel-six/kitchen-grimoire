// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"strings"
	"unicode"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// indexRecipe rewrites a recipe's search row from its stored data. It runs
// inside the save transaction, after the children are written, so the index
// never disagrees with the recipe.
func indexRecipe(ctx context.Context, tx *sql.Tx, r *recipe.Recipe) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM recipes_fts WHERE recipe_id = ?`, r.ID); err != nil {
		return err
	}
	var ingredients, steps []string
	for _, g := range r.IngredientGroups {
		if g.Name != "" {
			ingredients = append(ingredients, g.Name)
		}
		for _, in := range g.Ingredients {
			ingredients = append(ingredients, in.Raw)
		}
	}
	for _, g := range r.StepGroups {
		if g.Name != "" {
			steps = append(steps, g.Name)
		}
		steps = append(steps, g.Steps...)
	}
	tags := append([]string{}, r.Tags...)
	for _, c := range r.Categories {
		tags = append(tags, c...)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO recipes_fts (recipe_id, title, ingredients, tags, contributor, body)
		VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, r.Title, strings.Join(ingredients, "\n"), strings.Join(tags, " "),
		strings.Join([]string{r.ContributedBy, r.Author, r.Collection}, " "),
		strings.Join([]string{r.Description, strings.Join(steps, "\n"), r.Notes}, "\n"))
	return err
}

// Reindex rebuilds the whole search index (grimoire migrate --reindex).
// It loads every recipe first, then rewrites the index in one transaction.
func (s *Store) Reindex(ctx context.Context) (int, error) {
	ids, err := s.stringList(ctx, `SELECT id FROM recipes`)
	if err != nil {
		return 0, err
	}
	all := make([]*recipe.Recipe, 0, len(ids))
	for _, id := range ids {
		r, err := s.GetRecipe(ctx, id)
		if err != nil {
			return 0, err
		}
		all = append(all, r)
	}
	err = s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM recipes_fts`); err != nil {
			return err
		}
		for _, r := range all {
			if err := indexRecipe(ctx, tx, r); err != nil {
				return err
			}
		}
		return nil
	})
	return len(all), err
}

// SearchQuery is a search with optional filters. An empty Text lists the
// filtered recipes alphabetically.
type SearchQuery struct {
	Text        string
	Category    string // category path slug, e.g. "desserts/cookies"; includes sub-categories
	Tag         string // tag slug
	Contributor string // exact name, case-insensitive
	Collection  string // exact name, case-insensitive
	NeedsReview bool
	HasPhoto    bool
	Limit       int
	Offset      int
}

// Search returns matching live recipes, best match first, and the total.
func (s *Store) Search(ctx context.Context, q SearchQuery) ([]recipe.Summary, int, error) {
	if q.Limit <= 0 || q.Limit > 200 {
		q.Limit = 50
	}
	where := []string{"r.deleted_at IS NULL"}
	var args []any
	match := FTSQuery(q.Text)
	if match != "" {
		where = append(where, "r.id IN (SELECT recipe_id FROM recipes_fts WHERE recipes_fts MATCH ?)")
		args = append(args, match)
	}
	if q.Category != "" {
		where = append(where, `r.id IN (SELECT rc.recipe_id FROM recipe_categories rc JOIN categories c ON c.id = rc.category_id
			WHERE c.path = ? OR c.path LIKE ? ESCAPE '\')`)
		args = append(args, q.Category, likeEscape(q.Category)+"/%")
	}
	if q.Tag != "" {
		where = append(where, `r.id IN (SELECT rt.recipe_id FROM recipe_tags rt JOIN tags t ON t.id = rt.tag_id WHERE t.slug = ?)`)
		args = append(args, q.Tag)
	}
	if q.Contributor != "" {
		where = append(where, "r.contributed_by = ? COLLATE NOCASE")
		args = append(args, q.Contributor)
	}
	if q.Collection != "" {
		where = append(where, "r.collection = ? COLLATE NOCASE")
		args = append(args, q.Collection)
	}
	if q.NeedsReview {
		where = append(where, "r.needs_review = 1")
	}
	if q.HasPhoto {
		where = append(where, "EXISTS (SELECT 1 FROM images i WHERE i.recipe_id = r.id)")
	}
	w := strings.Join(where, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM recipes r WHERE `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	var rows *sql.Rows
	var err error
	if match != "" {
		// Rank by bm25 with the column weights from §10 (recipe_id is
		// unindexed and weighted 0).
		rows, err = s.db.QueryContext(ctx, `SELECT `+summaryColumns+` FROM recipes r
			JOIN (SELECT recipe_id, bm25(recipes_fts, 0, 10.0, 5.0, 5.0, 3.0, 1.0) AS rank
			      FROM recipes_fts WHERE recipes_fts MATCH ?) m ON m.recipe_id = r.id
			WHERE `+w+` ORDER BY m.rank, r.title COLLATE NOCASE LIMIT ? OFFSET ?`,
			append(append([]any{match}, args...), q.Limit, q.Offset)...)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT `+summaryColumns+` FROM recipes r WHERE `+w+
			` ORDER BY r.title COLLATE NOCASE, r.id LIMIT ? OFFSET ?`, append(args, q.Limit, q.Offset)...)
	}
	if err != nil {
		return nil, 0, err
	}
	out, err := scanSummaries(rows)
	return out, total, err
}

// FTSQuery turns what a person typed into a safe FTS5 query (§10):
// words are ANDed, the last bare word also matches as a prefix ("butter"
// finds "buttermilk"), "quoted phrases" stay together, and -word excludes.
// Every term is re-quoted, so user input can never be FTS5 syntax and never
// causes a syntax error. It returns "" when nothing searchable remains.
func FTSQuery(input string) string {
	type term struct {
		text   string
		phrase bool
		not    bool
	}
	var terms []term
	rs := []rune(input)
	for i := 0; i < len(rs); {
		switch {
		case unicode.IsSpace(rs[i]):
			i++
			continue
		}
		not := false
		if rs[i] == '-' && i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) {
			not = true
			i++
		}
		if i < len(rs) && rs[i] == '"' {
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				j++
			}
			if t := cleanTerm(string(rs[i+1 : min(j, len(rs))])); t != "" {
				terms = append(terms, term{text: t, phrase: true, not: not})
			}
			i = j + 1
			continue
		}
		j := i
		for j < len(rs) && !unicode.IsSpace(rs[j]) && rs[j] != '"' {
			j++
		}
		if t := cleanTerm(string(rs[i:j])); t != "" {
			terms = append(terms, term{text: t, not: not})
		}
		i = j
	}

	var pos, neg []string
	// Only the final term gets a prefix match, and only if it's a plain
	// word: that's the one still being typed.
	lastBare := -1
	if n := len(terms); n > 0 && !terms[n-1].not && !terms[n-1].phrase {
		lastBare = n - 1
	}
	for i, t := range terms {
		q := `"` + strings.ReplaceAll(t.text, `"`, `""`) + `"`
		if i == lastBare && len([]rune(t.text)) >= 2 && !strings.Contains(t.text, " ") {
			q += "*"
		}
		if t.not {
			neg = append(neg, q)
		} else {
			pos = append(pos, q)
		}
	}
	if len(pos) == 0 {
		return "" // FTS5 can't express "everything except"; ignore lone exclusions
	}
	out := "(" + strings.Join(pos, " AND ") + ")"
	for _, n := range neg {
		out += " NOT " + n
	}
	return out
}

// cleanTerm keeps letters, digits, and inner spaces (for phrases); the
// tokenizer would drop everything else anyway.
func cleanTerm(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) {
			return r
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// --- facets for browsing ---

// CategoryCount is a category with how many live recipes it (and its
// sub-categories) hold.
type CategoryCount struct {
	ID       int64
	ParentID int64 // 0 for top level
	Name     string
	Path     string
	Count    int // including sub-categories
	Depth    int
}

// CategoryTree lists categories depth-first, A–Z within each level, with
// counts that include sub-categories. Empty categories are omitted.
func (s *Store) CategoryTree(ctx context.Context) ([]CategoryCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, COALESCE(c.parent_id, 0), c.name, c.path,
		       (SELECT count(DISTINCT rc.recipe_id) FROM recipe_categories rc
		          JOIN categories d ON d.id = rc.category_id
		          JOIN recipes r ON r.id = rc.recipe_id AND r.deleted_at IS NULL
		         WHERE d.path = c.path OR d.path LIKE replace(replace(replace(c.path, '\', '\\'), '%', '\%'), '_', '\_') || '/%' ESCAPE '\')
		FROM categories c`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	children := map[int64][]CategoryCount{}
	for rows.Next() {
		var c CategoryCount
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Path, &c.Count); err != nil {
			return nil, err
		}
		if c.Count > 0 {
			children[c.ParentID] = append(children[c.ParentID], c)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []CategoryCount
	var walk func(parent int64, depth int)
	walk = func(parent int64, depth int) {
		list := children[parent]
		sortByName(list)
		for _, c := range list {
			c.Depth = depth
			out = append(out, c)
			walk(c.ID, depth+1)
		}
	}
	walk(0, 0)
	return out, nil
}

func sortByName(list []CategoryCount) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && strings.ToLower(list[j].Name) < strings.ToLower(list[j-1].Name); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// NameCount is a value with a recipe count (contributors, collections, tags).
type NameCount struct {
	Name  string
	Slug  string // set for tags
	Count int
}

// Contributors lists who contributed live recipes, most first.
func (s *Store) Contributors(ctx context.Context) ([]NameCount, error) {
	return s.nameCounts(ctx, `SELECT contributed_by, '', count(*) FROM recipes
		WHERE deleted_at IS NULL AND contributed_by <> ''
		GROUP BY contributed_by COLLATE NOCASE ORDER BY count(*) DESC, contributed_by COLLATE NOCASE`)
}

// Collections lists collections with live recipes.
func (s *Store) Collections(ctx context.Context) ([]NameCount, error) {
	return s.nameCounts(ctx, `SELECT collection, '', count(*) FROM recipes
		WHERE deleted_at IS NULL AND collection <> ''
		GROUP BY collection COLLATE NOCASE ORDER BY count(*) DESC, collection COLLATE NOCASE`)
}

// Tags lists tags used by live recipes.
func (s *Store) Tags(ctx context.Context) ([]NameCount, error) {
	return s.nameCounts(ctx, `SELECT t.name, t.slug, count(*) FROM tags t
		JOIN recipe_tags rt ON rt.tag_id = t.id JOIN recipes r ON r.id = rt.recipe_id AND r.deleted_at IS NULL
		GROUP BY t.id ORDER BY count(*) DESC, t.name COLLATE NOCASE`)
}

func (s *Store) nameCounts(ctx context.Context, q string) ([]NameCount, error) {
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NameCount
	for rows.Next() {
		var n NameCount
		if err := rows.Scan(&n.Name, &n.Slug, &n.Count); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
