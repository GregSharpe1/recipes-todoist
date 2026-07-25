package app

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"todoist-recipes/internal/db"
)

func TestCreateRecipePersistsMethodWithAndWithoutSteps(t *testing.T) {
	database, err := sql.Open("sqlite", "file:app-recipes-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := db.EnsureSchema(context.Background(), database); err != nil {
		t.Fatalf("EnsureSchema() error = %v", err)
	}
	app := &App{repo: db.NewRepository(database)}

	tests := []struct {
		name   string
		method []string
		want   []string
	}{
		{name: "with method", method: []string{"  Chop vegetables  ", "", "Chop vegetables", " Roast slowly "}, want: []string{"Chop vegetables", "Chop vegetables", "Roast slowly"}},
		{name: "without method", method: nil, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := new(bytes.Buffer)
			form := multipart.NewWriter(body)
			fields := map[string]string{"name": "Recipe " + tt.name, "ingredient_name[]": "1 onion"}
			for key, value := range fields {
				if err := form.WriteField(key, value); err != nil {
					t.Fatalf("WriteField() error = %v", err)
				}
			}
			for _, step := range tt.method {
				if err := form.WriteField("method_step[]", step); err != nil {
					t.Fatalf("WriteField() error = %v", err)
				}
			}
			if err := form.Close(); err != nil {
				t.Fatalf("form.Close() error = %v", err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/recipes", body)
			req.Header.Set("Content-Type", form.FormDataContentType())
			response := httptest.NewRecorder()
			app.createRecipeHandler(response, req)
			if response.Code != http.StatusSeeOther {
				t.Fatalf("createRecipeHandler() status = %d, want %d", response.Code, http.StatusSeeOther)
			}

			recipes, err := app.listRecipes(context.Background())
			if err != nil {
				t.Fatalf("listRecipes() error = %v", err)
			}
			var got []string
			for _, recipe := range recipes {
				if recipe.Name == "Recipe "+tt.name {
					got = recipe.Method
					break
				}
			}
			if len(got) != len(tt.want) {
				t.Fatalf("persisted method = %#v, want %#v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("persisted method[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSaveIngredientsHandlerUpdatesIngredientsAndMethodTogether(t *testing.T) {
	database, err := sql.Open("sqlite", "file:app-recipe-edit-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	ctx := context.Background()
	if err := db.EnsureSchema(ctx, database); err != nil {
		t.Fatalf("EnsureSchema() error = %v", err)
	}
	app := &App{repo: db.NewRepository(database)}
	if inserted, err := app.insertRecipe(ctx, Recipe{ID: "edit-me", Name: "Edit me", Ingredients: []string{"1 onion"}, Method: []string{"Chop", "Cook"}}); err != nil || !inserted {
		t.Fatalf("insert recipe = (%v, %v)", inserted, err)
	}

	postEdit := func(values url.Values) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/recipes/edit-me/ingredients/save", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetPathValue("id", "edit-me")
		response := httptest.NewRecorder()
		app.saveIngredientsHandler(response, req)
		return response
	}

	response := postEdit(url.Values{
		"ingredient_text[]": {" 2 onions ", ""},
		"method_step[]":     {"  Mix  ", "", "Serve"},
	})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("successful save status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	got, err := app.getRecipeByID(ctx, "edit-me")
	if err != nil {
		t.Fatalf("get after successful save: %v", err)
	}
	if !reflect.DeepEqual(got.Ingredients, []string{"2 onions"}) || !reflect.DeepEqual(got.Method, []string{"Mix", "Serve"}) {
		t.Fatalf("saved recipe = ingredients %#v, method %#v", got.Ingredients, got.Method)
	}

	response = postEdit(url.Values{"ingredient_text[]": {"3 onions"}, "method_step[]": {""}})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("clear method status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	got, err = app.getRecipeByID(ctx, "edit-me")
	if err != nil {
		t.Fatalf("get after clearing method: %v", err)
	}
	if !reflect.DeepEqual(got.Ingredients, []string{"3 onions"}) || len(got.Method) != 0 {
		t.Fatalf("cleared recipe = ingredients %#v, method %#v", got.Ingredients, got.Method)
	}

	response = postEdit(url.Values{"ingredient_text[]": {"4 onions"}, "method_step[]": {"Keep this"}})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("method restore status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	response = postEdit(url.Values{"ingredient_text[]": {"5 onions"}})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("legacy ingredient-only save status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	got, err = app.getRecipeByID(ctx, "edit-me")
	if err != nil {
		t.Fatalf("get after legacy save: %v", err)
	}
	if !reflect.DeepEqual(got.Ingredients, []string{"5 onions"}) || !reflect.DeepEqual(got.Method, []string{"Keep this"}) {
		t.Fatalf("legacy save changed method = ingredients %#v, method %#v", got.Ingredients, got.Method)
	}

	response = postEdit(url.Values{"ingredient_text[]": {""}, "method_step[]": {"Should not save"}})
	if response.Code != http.StatusSeeOther {
		t.Fatalf("invalid save status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	got, err = app.getRecipeByID(ctx, "edit-me")
	if err != nil {
		t.Fatalf("get after invalid save: %v", err)
	}
	if !reflect.DeepEqual(got.Ingredients, []string{"5 onions"}) || !reflect.DeepEqual(got.Method, []string{"Keep this"}) {
		t.Fatalf("invalid save changed recipe = ingredients %#v, method %#v", got.Ingredients, got.Method)
	}
}
