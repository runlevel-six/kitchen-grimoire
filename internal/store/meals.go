// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/runlevel-six/kitchen-grimoire/internal/recipe"
)

// Meals (FR-16, D-22). Meals are shared by the household: any signed-in
// user can see and change any meal. Deleted meals stay restorable until the
// trash purge removes them.

// CreateMeal adds a meal and returns its ID.
func (s *Store) CreateMeal(ctx context.Context, name, userID string) (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	now := Now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO meals (id, name, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`, id.String(), name, nullStr(userID), now, now)
	return id.String(), err
}

// ListMeals lists live meals, most recently changed first.
func (s *Store) ListMeals(ctx context.Context) ([]recipe.MealSummary, error) {
	return s.mealSummaries(ctx, `SELECT m.id, m.name, m.updated_at,
		(SELECT count(*) FROM meal_recipes mr WHERE mr.meal_id = m.id), 0
		FROM meals m WHERE m.deleted_at IS NULL ORDER BY m.updated_at DESC, m.id DESC`)
}

// MealsForRecipe lists live meals like ListMeals, with Factor set to the
// recipe's scale in each meal that holds it.
func (s *Store) MealsForRecipe(ctx context.Context, recipeID string) ([]recipe.MealSummary, error) {
	return s.mealSummaries(ctx, `SELECT m.id, m.name, m.updated_at,
		(SELECT count(*) FROM meal_recipes mr WHERE mr.meal_id = m.id),
		COALESCE((SELECT factor FROM meal_recipes mr WHERE mr.meal_id = m.id AND mr.recipe_id = ?), 0)
		FROM meals m WHERE m.deleted_at IS NULL ORDER BY m.updated_at DESC, m.id DESC`, recipeID)
}

func (s *Store) mealSummaries(ctx context.Context, q string, args ...any) ([]recipe.MealSummary, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recipe.MealSummary
	for rows.Next() {
		var m recipe.MealSummary
		var updated string
		if err := rows.Scan(&m.ID, &m.Name, &updated, &m.Recipes, &m.Factor); err != nil {
			return nil, err
		}
		m.UpdatedAt = parseTime(updated)
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetMeal returns a meal with its entries in order, including deleted
// meals (so the page can offer to restore) and recipes in the trash (so the
// page can say so). A missing meal is recipe.ErrNotFound.
func (s *Store) GetMeal(ctx context.Context, id string) (*recipe.Meal, error) {
	var m recipe.Meal
	var created, updated string
	var deleted sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, name, notes, created_at, updated_at, deleted_at
		FROM meals WHERE id = ?`, id).Scan(&m.ID, &m.Name, &m.Notes, &created, &updated, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, recipe.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.CreatedAt, m.UpdatedAt, m.DeletedAt = parseTime(created), parseTime(updated), parseNullTime(deleted)
	rows, err := s.db.QueryContext(ctx, `SELECT `+summaryColumns+`, mr.factor
		FROM meal_recipes mr JOIN recipes r ON r.id = mr.recipe_id
		WHERE mr.meal_id = ? ORDER BY mr.position, mr.added_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e recipe.MealEntry
		if e.Recipe, err = scanSummary(rows, &e.Factor); err != nil {
			return nil, err
		}
		m.Entries = append(m.Entries, e)
	}
	return &m, rows.Err()
}

// UpdateMeal renames a live meal and replaces its notes.
func (s *Store) UpdateMeal(ctx context.Context, id, name, notes string) error {
	return s.touch(ctx, `UPDATE meals SET name = ?, notes = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL`,
		name, notes, Now(), id)
}

// DeleteMeal moves a meal to the trash; RestoreMeal brings it back.
func (s *Store) DeleteMeal(ctx context.Context, id string) error {
	return s.touch(ctx, `UPDATE meals SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, Now(), id)
}

// RestoreMeal undoes DeleteMeal.
func (s *Store) RestoreMeal(ctx context.Context, id string) error {
	return s.touch(ctx, `UPDATE meals SET deleted_at = NULL, updated_at = ? WHERE id = ? AND deleted_at IS NOT NULL`,
		Now(), id)
}

// touch runs an UPDATE on one meal and maps "no row changed" to
// recipe.ErrNotFound.
func (s *Store) touch(ctx context.Context, q string, args ...any) error {
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return recipe.ErrNotFound
	}
	return nil
}

// AddToMeal puts a recipe in a live meal at factor, at the end. If it is
// already there, only its factor changes: a recipe is in a meal at most
// once (D-22).
func (s *Store) AddToMeal(ctx context.Context, mealID, recipeID string, factor float64) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := liveMeal(ctx, tx, mealID); err != nil {
			return err
		}
		now := Now()
		if _, err := tx.ExecContext(ctx, `INSERT INTO meal_recipes (meal_id, recipe_id, factor, position, added_at)
			VALUES (?, ?, ?, (SELECT COALESCE(MAX(position) + 1, 0) FROM meal_recipes WHERE meal_id = ?), ?)
			ON CONFLICT (meal_id, recipe_id) DO UPDATE SET factor = excluded.factor`,
			mealID, recipeID, factor, mealID, now); err != nil {
			return err
		}
		return bumpMeal(ctx, tx, mealID, now)
	})
}

// SetMealFactor changes a recipe's scale in a meal.
func (s *Store) SetMealFactor(ctx context.Context, mealID, recipeID string, factor float64) error {
	return s.entryChange(ctx, mealID, `UPDATE meal_recipes SET factor = ? WHERE meal_id = ? AND recipe_id = ?`,
		factor, mealID, recipeID)
}

// RemoveFromMeal takes a recipe out of a meal.
func (s *Store) RemoveFromMeal(ctx context.Context, mealID, recipeID string) error {
	return s.entryChange(ctx, mealID, `DELETE FROM meal_recipes WHERE meal_id = ? AND recipe_id = ?`,
		mealID, recipeID)
}

func (s *Store) entryChange(ctx context.Context, mealID, q string, args ...any) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := liveMeal(ctx, tx, mealID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return recipe.ErrNotFound
		}
		return bumpMeal(ctx, tx, mealID, Now())
	})
}

// MoveInMeal moves a recipe one place up (delta < 0) or down (delta > 0).
// Moving past either end does nothing.
func (s *Store) MoveInMeal(ctx context.Context, mealID, recipeID string, delta int) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if err := liveMeal(ctx, tx, mealID); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT recipe_id FROM meal_recipes WHERE meal_id = ?
			ORDER BY position, added_at`, mealID)
		if err != nil {
			return err
		}
		var order []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			order = append(order, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		i := -1
		for k, id := range order {
			if id == recipeID {
				i = k
			}
		}
		if i < 0 {
			return recipe.ErrNotFound
		}
		j := i + 1
		if delta < 0 {
			j = i - 1
		}
		if j < 0 || j >= len(order) {
			return nil
		}
		order[i], order[j] = order[j], order[i]
		// Renumber everything, which also repairs any duplicate positions.
		for pos, id := range order {
			if _, err := tx.ExecContext(ctx, `UPDATE meal_recipes SET position = ? WHERE meal_id = ? AND recipe_id = ?`,
				pos, mealID, id); err != nil {
				return err
			}
		}
		return bumpMeal(ctx, tx, mealID, Now())
	})
}

func liveMeal(ctx context.Context, tx *sql.Tx, id string) error {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM meals WHERE id = ? AND deleted_at IS NULL`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return recipe.ErrNotFound
	}
	return err
}

func bumpMeal(ctx context.Context, tx *sql.Tx, id, now string) error {
	_, err := tx.ExecContext(ctx, `UPDATE meals SET updated_at = ? WHERE id = ?`, now, id)
	return err
}
