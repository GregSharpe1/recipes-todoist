package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
)

func TestOpenFromEnvCreatesSQLiteDatabase(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "data", "recipes.db")
	t.Setenv("DATABASE_PATH", databasePath)

	db, err := OpenFromEnv()
	if err != nil {
		t.Fatalf("OpenFromEnv() error = %v", err)
	}
	defer db.Close()

	if err := EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("EnsureSchema() error = %v", err)
	}

	if _, err := db.Exec(`SELECT 1 FROM recipes`); err != nil {
		t.Fatalf("recipes table was not created: %v", err)
	}
}

func TestEnsureSchemaMigratesRecipesWithoutMethod(t *testing.T) {
	db, err := sql.Open("sqlite", "file:recipes-legacy-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	_, err = db.Exec(`CREATE TABLE recipes (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, image_path TEXT NOT NULL,
		source_url TEXT NOT NULL DEFAULT '', ingredients_json TEXT NOT NULL,
		deleted_at TEXT, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := EnsureSchema(context.Background(), db); err != nil {
		t.Fatalf("EnsureSchema() error = %v", err)
	}
	if _, err := db.Exec(`INSERT INTO recipes (id, name, image_path, ingredients_json) VALUES ('legacy', 'Legacy', '', '[]')`); err != nil {
		t.Fatalf("insert legacy recipe: %v", err)
	}

	var method string
	if err := db.QueryRow(`SELECT method_json FROM recipes`).Scan(&method); err != nil {
		t.Fatalf("read migrated method: %v", err)
	}
	if method != "[]" {
		t.Fatalf("migrated method = %q, want []", method)
	}
	repo := NewRepository(db)
	recipe, err := repo.GetRecipeByID(context.Background(), "legacy")
	if err != nil {
		t.Fatalf("read migrated recipe: %v", err)
	}
	if len(recipe.Method) != 0 {
		t.Fatalf("migrated recipe method = %#v, want empty", recipe.Method)
	}
}

func TestRepositoryRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite", "file:recipes-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatalf("EnsureSchema() error = %v", err)
	}
	repo := NewRepository(db)

	recipe := Recipe{
		ID:          "chicken-pie",
		Name:        "Chicken Pie",
		ImagePath:   "/uploads/pie.jpg",
		SourceURL:   "https://example.test/recipe",
		Ingredients: []string{"500g chicken", "1 onion"},
		Method:      []string{"  Fry the chicken.  ", "", "Fry the chicken."},
	}
	inserted, err := repo.InsertRecipe(ctx, recipe)
	if err != nil || !inserted {
		t.Fatalf("InsertRecipe() = (%v, %v), want (true, nil)", inserted, err)
	}

	got, err := repo.GetRecipeByID(ctx, recipe.ID)
	if err != nil {
		t.Fatalf("GetRecipeByID() error = %v", err)
	}
	want := recipe
	want.Method = []string{"Fry the chicken.", "Fry the chicken."}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetRecipeByID() = %#v, want %#v", got, want)
	}

	updatedIngredients := []string{"600g chicken", "2 onions"}
	if err := repo.UpdateRecipeIngredients(ctx, recipe.ID, updatedIngredients); err != nil {
		t.Fatalf("UpdateRecipeIngredients() error = %v", err)
	}
	got, err = repo.GetRecipeByID(ctx, recipe.ID)
	if err != nil {
		t.Fatalf("GetRecipeByID() after update error = %v", err)
	}
	if !reflect.DeepEqual(got.Ingredients, updatedIngredients) {
		t.Fatalf("updated ingredients = %#v, want %#v", got.Ingredients, updatedIngredients)
	}

	list := RegularList{ID: "store-cupboard", Name: "Store cupboard", Items: []string{"salt", "pepper"}}
	inserted, err = repo.InsertRegularList(ctx, list)
	if err != nil || !inserted {
		t.Fatalf("InsertRegularList() = (%v, %v), want (true, nil)", inserted, err)
	}

	exists, err := repo.RecipeExists(ctx, recipe.ID)
	if err != nil || !exists {
		t.Fatalf("RecipeExists() = (%v, %v), want (true, nil)", exists, err)
	}
	got, err = repo.GetRecipeByID(ctx, recipe.ID)
	if err != nil {
		t.Fatalf("GetRecipeByID() before archive = %v", err)
	}
	if !reflect.DeepEqual(got.Method, []string{"Fry the chicken.", "Fry the chicken."}) {
		t.Fatalf("method before archive = %#v", got.Method)
	}
	deleted, err := repo.SoftDeleteRecipeByID(ctx, recipe.ID)
	if err != nil || !deleted {
		t.Fatalf("SoftDeleteRecipeByID() = (%v, %v), want (true, nil)", deleted, err)
	}
	exists, err = repo.RecipeExists(ctx, recipe.ID)
	if err != nil || exists {
		t.Fatalf("RecipeExists() after delete = (%v, %v), want (false, nil)", exists, err)
	}
	restored, err := repo.RestoreRecipeByID(ctx, recipe.ID)
	if err != nil || !restored {
		t.Fatalf("RestoreRecipeByID() = (%v, %v), want (true, nil)", restored, err)
	}
	got, err = repo.GetRecipeByID(ctx, recipe.ID)
	if err != nil || !reflect.DeepEqual(got.Method, []string{"Fry the chicken.", "Fry the chicken."}) {
		t.Fatalf("restored method = %#v, error = %v", got.Method, err)
	}
}
