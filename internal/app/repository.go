package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (a *App) listRecipes(ctx context.Context) ([]Recipe, error) {
	return a.repo.ListRecipes(ctx)
}

func (a *App) recipeExists(ctx context.Context, id string) (bool, error) {
	return a.repo.RecipeExists(ctx, id)
}

func (a *App) getRecipeByID(ctx context.Context, id string) (Recipe, error) {
	return a.repo.GetRecipeByID(ctx, id)
}

func (a *App) insertRecipe(ctx context.Context, recipe Recipe) (bool, error) {
	return a.repo.InsertRecipe(ctx, recipe)
}

func (a *App) softDeleteRecipeByID(ctx context.Context, id string) (bool, error) {
	return a.repo.SoftDeleteRecipeByID(ctx, id)
}

func (a *App) updateRecipeIngredients(ctx context.Context, id string, ingredients []string) error {
	return a.repo.UpdateRecipeIngredients(ctx, id, ingredients)
}

func (a *App) updateRecipeImagePath(ctx context.Context, id, imagePath string) error {
	return a.repo.UpdateRecipeImagePath(ctx, id, imagePath)
}

func (a *App) appendIngredientToRecipe(ctx context.Context, id, ingredient string) error {
	recipe, err := a.getRecipeByID(ctx, id)
	if err != nil {
		return err
	}

	ingredient = strings.TrimSpace(ingredient)
	if ingredient == "" {
		return errors.New("ingredient is empty")
	}

	recipe.Ingredients = append(recipe.Ingredients, ingredient)
	return a.updateRecipeIngredients(ctx, id, recipe.Ingredients)
}

func (a *App) removeIngredientFromRecipe(ctx context.Context, id string, idx int) (bool, error) {
	recipe, err := a.getRecipeByID(ctx, id)
	if err != nil {
		return false, err
	}
	if idx < 0 || idx >= len(recipe.Ingredients) {
		return false, nil
	}

	ingredients := append([]string{}, recipe.Ingredients[:idx]...)
	ingredients = append(ingredients, recipe.Ingredients[idx+1:]...)
	if err := a.updateRecipeIngredients(ctx, id, ingredients); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) updateIngredientInRecipe(ctx context.Context, id string, idx int, ingredient string) (bool, error) {
	recipe, err := a.getRecipeByID(ctx, id)
	if err != nil {
		return false, err
	}
	if idx < 0 || idx >= len(recipe.Ingredients) {
		return false, nil
	}

	ingredient = strings.TrimSpace(ingredient)
	if ingredient == "" {
		return false, nil
	}

	ingredients := append([]string{}, recipe.Ingredients...)
	ingredients[idx] = ingredient
	if err := a.updateRecipeIngredients(ctx, id, ingredients); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) listRegularLists(ctx context.Context) ([]RegularList, error) {
	return a.repo.ListRegularLists(ctx)
}

func (a *App) getRegularListByID(ctx context.Context, id string) (RegularList, error) {
	return a.repo.GetRegularListByID(ctx, id)
}

func (a *App) insertRegularList(ctx context.Context, list RegularList) (bool, error) {
	return a.repo.InsertRegularList(ctx, list)
}

func (a *App) softDeleteRegularListByID(ctx context.Context, id string) (bool, error) {
	return a.repo.SoftDeleteRegularListByID(ctx, id)
}

func (a *App) updateRegularListItems(ctx context.Context, id string, items []string) error {
	return a.repo.UpdateRegularListItems(ctx, id, items)
}

func (a *App) createRegularList(ctx context.Context, name string, items []string) error {
	baseID := makeRecipeID(name)
	list := RegularList{ID: baseID, Name: name, Items: items}

	inserted, err := a.insertRegularList(ctx, list)
	if err != nil {
		return err
	}
	if inserted {
		return nil
	}

	list.ID = fmt.Sprintf("%s_%d", baseID, time.Now().UnixNano())
	inserted, err = a.insertRegularList(ctx, list)
	if err != nil {
		return err
	}
	if !inserted {
		return errors.New("failed to save regular list")
	}
	return nil
}
