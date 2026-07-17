package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type Recipe struct {
	ID          string
	Name        string
	ImagePath   string
	SourceURL   string
	Ingredients []string
}

type RegularList struct {
	ID    string
	Name  string
	Items []string
}

func (r *Repository) ListRecipes(ctx context.Context) ([]Recipe, error) {
	const q = `
SELECT id, name, image_path, source_url, ingredients_json
FROM recipes
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	recipes := make([]Recipe, 0)
	for rows.Next() {
		var recipe Recipe
		var rawIngredients []byte
		if err := rows.Scan(&recipe.ID, &recipe.Name, &recipe.ImagePath, &recipe.SourceURL, &rawIngredients); err != nil {
			return nil, err
		}
		ingredients, err := decodeIngredients(rawIngredients)
		if err != nil {
			return nil, err
		}
		recipe.Ingredients = ingredients
		recipes = append(recipes, recipe)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return recipes, nil
}

func (r *Repository) RecipeExists(ctx context.Context, id string) (bool, error) {
	const q = `SELECT 1 FROM recipes WHERE id = $1 AND deleted_at IS NULL LIMIT 1`
	var exists int
	err := r.db.QueryRowContext(ctx, q, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) GetRecipeByID(ctx context.Context, id string) (Recipe, error) {
	const q = `
SELECT id, name, image_path, source_url, ingredients_json
FROM recipes
WHERE id = $1 AND deleted_at IS NULL`

	var recipe Recipe
	var rawIngredients []byte
	err := r.db.QueryRowContext(ctx, q, id).Scan(&recipe.ID, &recipe.Name, &recipe.ImagePath, &recipe.SourceURL, &rawIngredients)
	if errors.Is(err, sql.ErrNoRows) {
		return Recipe{}, errors.New("recipe not found")
	}
	if err != nil {
		return Recipe{}, err
	}
	ingredients, err := decodeIngredients(rawIngredients)
	if err != nil {
		return Recipe{}, err
	}
	recipe.Ingredients = ingredients

	return recipe, nil
}

func (r *Repository) InsertRecipe(ctx context.Context, recipe Recipe) (bool, error) {
	rawIngredients, err := json.Marshal(normalizeIngredientStrings(recipe.Ingredients))
	if err != nil {
		return false, err
	}

	const q = `
INSERT INTO recipes (id, name, image_path, source_url, ingredients_json)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (id) DO NOTHING`

	result, err := r.db.ExecContext(ctx, q, recipe.ID, recipe.Name, recipe.ImagePath, recipe.SourceURL, rawIngredients)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rows == 1, nil
}

func (r *Repository) SoftDeleteRecipeByID(ctx context.Context, id string) (bool, error) {
	const q = `
UPDATE recipes
SET deleted_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL`

	result, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rows == 1, nil
}

func (r *Repository) UpdateRecipeIngredients(ctx context.Context, id string, ingredients []string) error {
	rawIngredients, err := json.Marshal(normalizeIngredientStrings(ingredients))
	if err != nil {
		return err
	}

	const q = `
UPDATE recipes
SET ingredients_json = $2
WHERE id = $1 AND deleted_at IS NULL`

	result, err := r.db.ExecContext(ctx, q, id, rawIngredients)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("recipe not found")
	}

	return nil
}

func (r *Repository) UpdateRecipeImagePath(ctx context.Context, id, imagePath string) error {
	const q = `
UPDATE recipes
SET image_path = $2
WHERE id = $1 AND deleted_at IS NULL`

	result, err := r.db.ExecContext(ctx, q, id, imagePath)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("recipe not found")
	}
	return nil
}

func decodeIngredients(raw []byte) ([]string, error) {
	var legacyIngredients []string
	if err := json.Unmarshal(raw, &legacyIngredients); err == nil {
		return normalizeIngredientStrings(legacyIngredients), nil
	}

	var withSettings []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &withSettings); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(withSettings))
	for _, ingredient := range withSettings {
		text := strings.TrimSpace(ingredient.Text)
		if text == "" {
			continue
		}
		out = append(out, text)
	}
	return out, nil
}

func normalizeIngredientStrings(ingredients []string) []string {
	out := make([]string, 0, len(ingredients))
	for _, ingredient := range ingredients {
		text := strings.TrimSpace(ingredient)
		if text == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

func (r *Repository) ListRegularLists(ctx context.Context) ([]RegularList, error) {
	const q = `
SELECT id, name, items_json
FROM regular_lists
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC`

	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	lists := make([]RegularList, 0)
	for rows.Next() {
		var list RegularList
		var rawItems []byte
		if err := rows.Scan(&list.ID, &list.Name, &rawItems); err != nil {
			return nil, err
		}
		items, err := decodeIngredients(rawItems)
		if err != nil {
			return nil, err
		}
		list.Items = items
		lists = append(lists, list)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return lists, nil
}

func (r *Repository) GetRegularListByID(ctx context.Context, id string) (RegularList, error) {
	const q = `
SELECT id, name, items_json
FROM regular_lists
WHERE id = $1 AND deleted_at IS NULL`

	var list RegularList
	var rawItems []byte
	err := r.db.QueryRowContext(ctx, q, id).Scan(&list.ID, &list.Name, &rawItems)
	if errors.Is(err, sql.ErrNoRows) {
		return RegularList{}, errors.New("regular list not found")
	}
	if err != nil {
		return RegularList{}, err
	}
	items, err := decodeIngredients(rawItems)
	if err != nil {
		return RegularList{}, err
	}
	list.Items = items
	return list, nil
}

func (r *Repository) InsertRegularList(ctx context.Context, list RegularList) (bool, error) {
	rawItems, err := json.Marshal(normalizeIngredientStrings(list.Items))
	if err != nil {
		return false, err
	}

	const q = `
INSERT INTO regular_lists (id, name, items_json)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO NOTHING`

	result, err := r.db.ExecContext(ctx, q, list.ID, list.Name, rawItems)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (r *Repository) SoftDeleteRegularListByID(ctx context.Context, id string) (bool, error) {
	const q = `
UPDATE regular_lists
SET deleted_at = CURRENT_TIMESTAMP
WHERE id = $1 AND deleted_at IS NULL`

	result, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (r *Repository) UpdateRegularListItems(ctx context.Context, id string, items []string) error {
	rawItems, err := json.Marshal(normalizeIngredientStrings(items))
	if err != nil {
		return err
	}

	const q = `
UPDATE regular_lists
SET items_json = $2
WHERE id = $1 AND deleted_at IS NULL`

	result, err := r.db.ExecContext(ctx, q, id, rawItems)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return errors.New("regular list not found")
	}
	return nil
}
