package app

import (
	"bytes"
	"context"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
