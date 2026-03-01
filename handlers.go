package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	qrcode "github.com/skip2/go-qrcode"
	"todoist-recipes/importers"
)

type Recipe struct {
	ID          string
	Name        string
	ImagePath   string
	SourceURL   string
	Ingredients []string
}

type recipeCard struct {
	ID                   string
	Name                 string
	ImagePath            string
	SourceURL            string
	SourceLabel          string
	Ingredients          []string
	QRPath               string
	QRPagePath           string
	PushPath             string
	DeletePath           string
	UpdatePhotoPath      string
	RemovePhotoPath      string
	AddIngredientPath    string
	RemoveIngredientPath string
	UpdateIngredientPath string
	SaveIngredientsPath  string
}

type indexData struct {
	Recipes            []recipeCard
	Error              string
	Notice             string
	Warnings           []string
	PrefillName        string
	PrefillIngredients []string
	PrefillRows        []prefillRow
	PrefillSourceURL   string
	PrefillImageURL    string
	ImportURL          string
}

type prefillRow struct {
	Name        string
	Measurement string
}

type App struct {
	uploadDir        string
	tmpl             *template.Template
	httpClient       *http.Client
	baseURL          string
	projectID        string
	apiBaseURL       string
	db               *sql.DB
	importerRegistry *importers.Registry
}

func (a *App) indexHandler(w http.ResponseWriter, r *http.Request) {
	data, err := a.buildIndexData(r.Context())
	if err != nil {
		http.Error(w, "failed to load recipes", http.StatusInternalServerError)
		return
	}

	data.Error = r.URL.Query().Get("error")
	data.Notice = r.URL.Query().Get("notice")

	if err := a.tmpl.Execute(w, data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (a *App) importRecipeHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderIndexWithMessages(w, r, "invalid import form", "", nil, "", nil)
		return
	}

	importURL := strings.TrimSpace(r.FormValue("import_url"))
	if importURL == "" {
		a.renderIndexWithMessages(w, r, "import URL is required", "", nil, "", nil)
		return
	}

	imported, err := a.importerRegistry.Import(r.Context(), importers.ImportRequest{URL: importURL, TargetServings: 2})
	if err != nil {
		msg := "import failed"
		switch {
		case errors.Is(err, importers.ErrUnsupportedSource):
			msg = "unsupported source URL (currently gousto.co.uk and bbcgoodfood.com)"
		case errors.Is(err, importers.ErrFetchFailed):
			msg = "failed to fetch recipe page"
		case errors.Is(err, importers.ErrParseFailed):
			msg = "could not extract ingredients from this recipe page"
		}
		a.renderIndexWithMessages(w, r, msg, "", nil, importURL, nil)
		return
	}
	imported.ImageURL = resolveImportedImageURL(imported.SourceURL, imported.ImageURL)

	notice := "recipe imported from " + imported.SourceName + " - review and save"
	a.renderIndexWithMessages(w, r, "", notice, imported.Warnings, imported.SourceURL, &imported)
}

func (a *App) buildIndexData(ctx context.Context) (indexData, error) {
	recipes, err := a.listRecipes(ctx)
	if err != nil {
		return indexData{}, err
	}

	data := indexData{}
	for _, recipe := range recipes {
		data.Recipes = append(data.Recipes, recipeCard{
			ID:                   recipe.ID,
			Name:                 recipe.Name,
			ImagePath:            recipe.ImagePath,
			SourceURL:            recipe.SourceURL,
			SourceLabel:          sourceLabelForURL(recipe.SourceURL),
			Ingredients:          recipe.Ingredients,
			QRPath:               "/qr/" + recipe.ID,
			QRPagePath:           "/recipes/" + recipe.ID + "/qr",
			PushPath:             "/api/push/" + recipe.ID,
			DeletePath:           "/api/recipes/" + recipe.ID + "/delete",
			UpdatePhotoPath:      "/api/recipes/" + recipe.ID + "/photo",
			RemovePhotoPath:      "/api/recipes/" + recipe.ID + "/photo/remove",
			AddIngredientPath:    "/api/recipes/" + recipe.ID + "/ingredients/add",
			RemoveIngredientPath: "/api/recipes/" + recipe.ID + "/ingredients/remove",
			UpdateIngredientPath: "/api/recipes/" + recipe.ID + "/ingredients/update",
			SaveIngredientsPath:  "/api/recipes/" + recipe.ID + "/ingredients/save",
		})
	}

	return data, nil
}

func (a *App) renderIndexWithMessages(w http.ResponseWriter, r *http.Request, errMsg, notice string, warnings []string, importURL string, imported *importers.ImportedRecipe) {
	data, err := a.buildIndexData(r.Context())
	if err != nil {
		http.Error(w, "failed to load recipes", http.StatusInternalServerError)
		return
	}

	data.Error = errMsg
	data.Notice = notice
	data.Warnings = warnings
	data.ImportURL = importURL
	if imported != nil {
		data.PrefillName = imported.Name
		data.PrefillIngredients = imported.Ingredients
		data.PrefillSourceURL = imported.SourceURL
		data.PrefillImageURL = imported.ImageURL
		data.PrefillRows = make([]prefillRow, 0, len(imported.Ingredients))
		for _, ingredient := range imported.Ingredients {
			name, measurement := splitIngredientForPrefill(ingredient)
			if strings.TrimSpace(name) == "" {
				continue
			}
			data.PrefillRows = append(data.PrefillRows, prefillRow{Name: name, Measurement: measurement})
		}
	}

	if err := a.tmpl.Execute(w, data); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (a *App) createRecipeHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		a.redirectError(w, r, "invalid multipart form")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	sourceURL := strings.TrimSpace(r.FormValue("source_url"))
	importedImageURL := strings.TrimSpace(r.FormValue("imported_image_url"))
	if sourceURL != "" {
		u, err := url.Parse(sourceURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			a.redirectError(w, r, "source URL must be a valid absolute URL")
			return
		}
	}
	if importedImageURL != "" {
		u, err := url.Parse(importedImageURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			a.redirectError(w, r, "imported image URL must be a valid absolute URL")
			return
		}
	}
	ingredients := parseIngredientFields(r)
	if name == "" {
		a.redirectError(w, r, "recipe name is required")
		return
	}
	if len(ingredients) == 0 {
		a.redirectError(w, r, "at least one ingredient is required")
		return
	}

	imgPath := ""
	file, header, err := r.FormFile("photo")
	if err != nil {
		if !errors.Is(err, http.ErrMissingFile) {
			a.redirectError(w, r, "failed to read uploaded image")
			return
		}
		if importedImageURL != "" {
			imgPath, err = a.saveImportedImage(importedImageURL)
			if err != nil {
				a.redirectError(w, r, "failed to fetch imported image")
				return
			}
		}
	} else {
		defer file.Close()
		imgPath, err = a.saveUploadedFile(file, header)
		if err != nil {
			a.redirectError(w, r, "failed to save image")
			return
		}
	}

	recipeID := makeRecipeID(name)

	recipe := Recipe{
		ID:          recipeID,
		Name:        name,
		ImagePath:   imgPath,
		SourceURL:   sourceURL,
		Ingredients: ingredients,
	}

	inserted, err := a.insertRecipe(r.Context(), recipe)
	if err != nil {
		a.redirectError(w, r, "failed to save recipe")
		return
	}
	if !inserted {
		recipe.ID = fmt.Sprintf("%s_%d", recipeID, time.Now().UnixNano())
		inserted, err = a.insertRecipe(r.Context(), recipe)
		if err != nil || !inserted {
			a.redirectError(w, r, "failed to save recipe")
			return
		}
	}

	a.redirectNotice(w, r, "recipe added")
}

func (a *App) pushHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.redirectError(w, r, "invalid push request")
		return
	}

	selectionMode := strings.TrimSpace(r.FormValue("selection_mode")) == "1"
	selectedIndexes, err := parseSelectedIngredientIndexes(r.Form["ingredient_idx[]"])
	if err != nil {
		a.redirectError(w, r, "invalid ingredient selection")
		return
	}

	if err := a.PushToTodoist(r.Context(), recipeID, selectedIndexes, selectionMode); err != nil {
		a.redirectError(w, r, err.Error())
		return
	}
	a.redirectNotice(w, r, "ingredients sent to Todoist")
}

func (a *App) deleteRecipeHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}

	deleted, err := a.softDeleteRecipeByID(r.Context(), recipeID)
	if err != nil {
		a.redirectError(w, r, "failed to delete recipe")
		return
	}
	if !deleted {
		a.redirectError(w, r, "recipe not found")
		return
	}

	a.redirectNotice(w, r, "recipe archived")
}

func (a *App) addIngredientHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.redirectError(w, r, "invalid ingredient form")
		return
	}

	name := strings.TrimSpace(r.FormValue("ingredient_name"))
	measurement := strings.TrimSpace(r.FormValue("ingredient_measurement"))
	if name == "" {
		a.redirectError(w, r, "ingredient name is required")
		return
	}

	ingredient := name
	if measurement != "" {
		ingredient += " " + measurement
	}

	if err := a.appendIngredientToRecipe(r.Context(), recipeID, ingredient); err != nil {
		a.redirectError(w, r, "failed to add ingredient")
		return
	}

	a.redirectNotice(w, r, "ingredient added")
}

func (a *App) removeIngredientHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.redirectError(w, r, "invalid ingredient form")
		return
	}

	rawIndex := strings.TrimSpace(r.FormValue("ingredient_idx"))
	idx, err := strconv.Atoi(rawIndex)
	if err != nil {
		a.redirectError(w, r, "invalid ingredient")
		return
	}

	removed, err := a.removeIngredientFromRecipe(r.Context(), recipeID, idx)
	if err != nil {
		a.redirectError(w, r, "failed to remove ingredient")
		return
	}
	if !removed {
		a.redirectError(w, r, "ingredient not found")
		return
	}

	a.redirectNotice(w, r, "ingredient removed")
}

func (a *App) updateIngredientHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.redirectError(w, r, "invalid ingredient form")
		return
	}

	rawIndex := strings.TrimSpace(r.FormValue("ingredient_idx"))
	idx, err := strconv.Atoi(rawIndex)
	if err != nil {
		a.redirectError(w, r, "invalid ingredient")
		return
	}

	name := strings.TrimSpace(r.FormValue("ingredient_name"))
	measurement := strings.TrimSpace(r.FormValue("ingredient_measurement"))
	if name == "" {
		a.redirectError(w, r, "ingredient is required")
		return
	}

	ingredient := name
	if measurement != "" {
		ingredient += " " + measurement
	}

	updated, err := a.updateIngredientInRecipe(r.Context(), recipeID, idx, ingredient)
	if err != nil {
		a.redirectError(w, r, "failed to update ingredient")
		return
	}
	if !updated {
		a.redirectError(w, r, "ingredient not found")
		return
	}

	a.redirectNotice(w, r, "ingredient updated")
}

func (a *App) saveIngredientsHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.redirectError(w, r, "invalid ingredient form")
		return
	}

	ingredients := parseIngredientTextFields(r.Form["ingredient_text[]"])
	if len(ingredients) == 0 {
		a.redirectError(w, r, "at least one ingredient is required")
		return
	}

	if err := a.updateRecipeIngredients(r.Context(), recipeID, ingredients); err != nil {
		a.redirectError(w, r, "failed to save ingredients")
		return
	}

	a.redirectNotice(w, r, "recipe ingredients saved")
}

func (a *App) updateRecipePhotoHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		a.redirectError(w, r, "invalid photo upload form")
		return
	}

	recipe, err := a.getRecipeByID(r.Context(), recipeID)
	if err != nil {
		a.redirectError(w, r, "recipe not found")
		return
	}

	file, header, err := r.FormFile("photo")
	if err != nil {
		a.redirectError(w, r, "photo file is required")
		return
	}
	defer file.Close()

	newPath, err := a.saveUploadedFile(file, header)
	if err != nil {
		a.redirectError(w, r, "failed to save image")
		return
	}

	if err := a.updateRecipeImagePath(r.Context(), recipeID, newPath); err != nil {
		_ = a.deleteUploadedFile(newPath)
		a.redirectError(w, r, "failed to update recipe photo")
		return
	}

	if recipe.ImagePath != "" && recipe.ImagePath != newPath {
		_ = a.deleteUploadedFile(recipe.ImagePath)
	}

	a.redirectNotice(w, r, "recipe photo updated")
}

func (a *App) removeRecipePhotoHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}

	recipe, err := a.getRecipeByID(r.Context(), recipeID)
	if err != nil {
		a.redirectError(w, r, "recipe not found")
		return
	}

	if recipe.ImagePath == "" {
		a.redirectNotice(w, r, "recipe has no photo")
		return
	}

	if err := a.updateRecipeImagePath(r.Context(), recipeID, ""); err != nil {
		a.redirectError(w, r, "failed to remove recipe photo")
		return
	}

	_ = a.deleteUploadedFile(recipe.ImagePath)
	a.redirectNotice(w, r, "recipe photo removed")
}

func (a *App) scanHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}

	if err := a.PushToTodoist(r.Context(), recipeID, nil, false); err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html><body><h1>Failed</h1><p>" + template.HTMLEscapeString(err.Error()) + "</p></body></html>"))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<html><body><h1>Success</h1><p>Ingredients were pushed to Todoist.</p></body></html>"))
}

func (a *App) qrHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}

	exists, err := a.recipeExists(r.Context(), recipeID)
	if err != nil {
		http.Error(w, "failed to load recipes", http.StatusInternalServerError)
		return
	}
	if !exists {
		http.NotFound(w, r)
		return
	}

	target := fmt.Sprintf("%s/scan/%s", strings.TrimSuffix(a.externalBaseURL(r), "/"), recipeID)
	png, err := qrcode.Encode(target, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, "failed to generate qr", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}

func (a *App) qrPageHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}

	recipe, err := a.getRecipeByID(r.Context(), recipeID)
	if err != nil {
		if err.Error() == "recipe not found" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "failed to load recipe", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprintf(w, "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>QR - %s</title><style>body{font-family:Segoe UI,Helvetica Neue,sans-serif;margin:0;padding:24px;background:#f7faf9;color:#1f2937}.wrap{max-width:640px;margin:0 auto;background:#fff;border:1px solid #d1d5db;border-radius:14px;padding:20px;box-shadow:0 10px 24px rgba(0,0,0,.06)}h1{margin:0 0 14px;font-size:1.6rem}.meta{color:#4b5563;margin-bottom:14px}.qr{display:block;width:280px;height:280px;margin:14px auto;border:1px solid #e5e7eb;border-radius:10px}.actions{display:flex;gap:10px;justify-content:center;margin-top:14px}.btn{display:inline-block;text-decoration:none;background:#0f766e;color:#fff;padding:10px 14px;border-radius:10px}button{background:#fff;border:1px solid #d1d5db;padding:10px 14px;border-radius:10px;cursor:pointer}@media print{body{background:#fff;padding:0}.wrap{border:0;box-shadow:none;max-width:none}.actions{display:none}}</style></head><body><main class=\"wrap\"><h1>%s</h1><p class=\"meta\">Scan to add ingredients to Todoist</p><img class=\"qr\" src=\"/qr/%s\" alt=\"QR for %s\"><div class=\"actions\"><a class=\"btn\" href=\"/\">Back to recipes</a><button type=\"button\" onclick=\"window.print()\">Print</button></div></main></body></html>", template.HTMLEscapeString(recipe.Name), template.HTMLEscapeString(recipe.Name), template.HTMLEscapeString(recipeID), template.HTMLEscapeString(recipe.Name))
}

func (a *App) PushToTodoist(ctx context.Context, recipeID string, selectedIndexes []int, enforceSelection bool) error {
	token := strings.TrimSpace(os.Getenv("TODOIST_API_TOKEN"))
	if token == "" {
		return errors.New("TODOIST_API_TOKEN is not set")
	}

	recipe, err := a.getRecipeByID(ctx, recipeID)
	if err != nil {
		return err
	}
	if len(recipe.Ingredients) == 0 {
		return errors.New("recipe has no ingredients")
	}

	pushable := recipe.Ingredients
	if enforceSelection {
		if len(selectedIndexes) == 0 {
			return errors.New("select at least one ingredient")
		}
		selectedSet := make(map[int]struct{}, len(selectedIndexes))
		for _, idx := range selectedIndexes {
			if idx >= 0 && idx < len(recipe.Ingredients) {
				selectedSet[idx] = struct{}{}
			}
		}
		filtered := make([]string, 0, len(selectedSet))
		for idx, ingredient := range recipe.Ingredients {
			if _, ok := selectedSet[idx]; !ok {
				continue
			}
			text := strings.TrimSpace(ingredient)
			if text == "" {
				continue
			}
			filtered = append(filtered, text)
		}
		pushable = filtered
	}
	if len(pushable) == 0 {
		return errors.New("select at least one ingredient")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(pushable))

	for _, ingredient := range pushable {
		ingredient := ingredient
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.pushIngredient(token, recipe.Name, ingredient); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	errorsFound := make([]string, 0)
	seen := map[string]struct{}{}
	for err := range errCh {
		msg := err.Error()
		if _, ok := seen[msg]; ok {
			continue
		}
		seen[msg] = struct{}{}
		errorsFound = append(errorsFound, msg)
	}
	if len(errorsFound) > 0 {
		return fmt.Errorf("todoist push failed: %s", strings.Join(errorsFound, "; "))
	}

	return nil
}

func (a *App) pushIngredient(token, recipeName, ingredient string) error {
	payload := map[string]string{
		"content": fmt.Sprintf("%s (%s)", ingredient, recipeName),
	}
	if a.projectID != "" {
		payload["project_id"] = a.projectID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(a.apiBaseURL, "/")+"/tasks", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("todoist status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	return nil
}

func (a *App) validateTodoistProject() error {
	token := strings.TrimSpace(os.Getenv("TODOIST_API_TOKEN"))
	if token == "" {
		return errors.New("TODOIST_API_TOKEN is not set")
	}
	if strings.TrimSpace(a.projectID) == "" {
		return errors.New("project ID is empty")
	}

	url := fmt.Sprintf("%s/projects/%s", strings.TrimRight(a.apiBaseURL, "/"), a.projectID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return fmt.Errorf("status %d from %s: %s", resp.StatusCode, url, strings.TrimSpace(string(body)))
}

func openPostgresFromEnv() (*sql.DB, error) {
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func ensureRecipeSchema(ctx context.Context, db *sql.DB) error {
	const q = `
CREATE TABLE IF NOT EXISTS recipes (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	image_path TEXT NOT NULL,
	source_url TEXT NOT NULL DEFAULT '',
	ingredients_json JSONB NOT NULL,
	deleted_at TIMESTAMPTZ,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`
	if _, err := db.ExecContext(ctx, q); err != nil {
		return err
	}

	const qAlter = `
ALTER TABLE recipes
ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;`
	if _, err := db.ExecContext(ctx, qAlter); err != nil {
		return err
	}

	const qAlterSource = `
ALTER TABLE recipes
ADD COLUMN IF NOT EXISTS source_url TEXT NOT NULL DEFAULT '';`
	_, err := db.ExecContext(ctx, qAlterSource)
	return err
}

func (a *App) listRecipes(ctx context.Context) ([]Recipe, error) {
	const q = `
SELECT id, name, image_path, source_url, ingredients_json
FROM recipes
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC`

	rows, err := a.db.QueryContext(ctx, q)
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

func (a *App) recipeExists(ctx context.Context, id string) (bool, error) {
	const q = `SELECT 1 FROM recipes WHERE id = $1 AND deleted_at IS NULL LIMIT 1`
	var exists int
	err := a.db.QueryRowContext(ctx, q, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) getRecipeByID(ctx context.Context, id string) (Recipe, error) {
	const q = `
SELECT id, name, image_path, source_url, ingredients_json
FROM recipes
WHERE id = $1 AND deleted_at IS NULL`

	var recipe Recipe
	var rawIngredients []byte
	err := a.db.QueryRowContext(ctx, q, id).Scan(&recipe.ID, &recipe.Name, &recipe.ImagePath, &recipe.SourceURL, &rawIngredients)
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

func (a *App) insertRecipe(ctx context.Context, recipe Recipe) (bool, error) {
	rawIngredients, err := json.Marshal(recipe.Ingredients)
	if err != nil {
		return false, err
	}

	const q = `
INSERT INTO recipes (id, name, image_path, source_url, ingredients_json)
VALUES ($1, $2, $3, $4, $5::jsonb)
ON CONFLICT (id) DO NOTHING`

	result, err := a.db.ExecContext(ctx, q, recipe.ID, recipe.Name, recipe.ImagePath, recipe.SourceURL, rawIngredients)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rows == 1, nil
}

func (a *App) softDeleteRecipeByID(ctx context.Context, id string) (bool, error) {
	const q = `
UPDATE recipes
SET deleted_at = NOW()
WHERE id = $1 AND deleted_at IS NULL`

	result, err := a.db.ExecContext(ctx, q, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rows == 1, nil
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

func (a *App) updateRecipeIngredients(ctx context.Context, id string, ingredients []string) error {
	rawIngredients, err := json.Marshal(normalizeIngredientStrings(ingredients))
	if err != nil {
		return err
	}

	const q = `
UPDATE recipes
SET ingredients_json = $2::jsonb
WHERE id = $1 AND deleted_at IS NULL`

	result, err := a.db.ExecContext(ctx, q, id, rawIngredients)
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

func (a *App) updateRecipeImagePath(ctx context.Context, id, imagePath string) error {
	const q = `
UPDATE recipes
SET image_path = $2
WHERE id = $1 AND deleted_at IS NULL`

	result, err := a.db.ExecContext(ctx, q, id, imagePath)
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

func (a *App) deleteUploadedFile(imagePath string) error {
	clean := strings.TrimSpace(imagePath)
	if clean == "" {
		return nil
	}
	const prefix = "/uploads/"
	if !strings.HasPrefix(clean, prefix) {
		return nil
	}
	name := strings.TrimSpace(strings.TrimPrefix(clean, prefix))
	if name == "" {
		return nil
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return nil
	}
	return os.Remove(filepath.Join(a.uploadDir, name))
}

func (a *App) saveUploadedFile(file multipart.File, header *multipart.FileHeader) (string, error) {
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".jpg"
	}

	fileName := fmt.Sprintf("recipe_%d%s", time.Now().UnixNano(), ext)
	absPath := filepath.Join(a.uploadDir, fileName)

	out, err := os.Create(absPath)
	if err != nil {
		return "", err
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		return "", err
	}

	return "/uploads/" + fileName, nil
}

func (a *App) saveImportedImage(rawURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("unsupported image URL scheme")
	}
	if !isAllowedImportImageHost(u.Hostname()) {
		return "", errors.New("imported image host is not allowed")
	}

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "todoist-recipes/1.0 (+recipe importer)")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("image fetch status %d", resp.StatusCode)
	}

	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if contentType != "" && !strings.HasPrefix(contentType, "image/") {
		return "", errors.New("imported URL is not an image")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", errors.New("empty image response")
	}

	ext := extensionFromImageResponse(contentType, u.Path)
	fileName := fmt.Sprintf("recipe_%d%s", time.Now().UnixNano(), ext)
	absPath := filepath.Join(a.uploadDir, fileName)
	if err := os.WriteFile(absPath, data, 0o644); err != nil {
		return "", err
	}

	return "/uploads/" + fileName, nil
}

func extensionFromImageResponse(contentType, pathValue string) string {
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = contentType[:idx]
	}
	switch strings.TrimSpace(contentType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}

	ext := strings.ToLower(filepath.Ext(pathValue))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif":
		if ext == ".jpeg" {
			return ".jpg"
		}
		return ext
	default:
		return ".jpg"
	}
}

func isAllowedImportImageHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "gousto.co.uk", "www.gousto.co.uk", "production-media.gousto.co.uk":
		return true
	case "bbcgoodfood.com", "www.bbcgoodfood.com", "images.immediate.co.uk":
		return true
	default:
		return false
	}
}

func makeRecipeID(name string) string {
	clean := strings.ToLower(strings.TrimSpace(name))
	clean = strings.ReplaceAll(clean, " ", "_")
	clean = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_':
			return r
		default:
			return -1
		}
	}, clean)
	if clean == "" {
		clean = "recipe"
	}
	return fmt.Sprintf("%s_%d", clean, time.Now().Unix())
}

func parseIngredients(raw string) []string {
	replaced := strings.ReplaceAll(raw, "\r\n", "\n")
	replaced = strings.ReplaceAll(replaced, ",", "\n")
	parts := strings.Split(replaced, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func parseIngredientFields(r *http.Request) []string {
	names := r.Form["ingredient_name[]"]
	measurements := r.Form["ingredient_measurement[]"]
	if len(names) == 0 && len(measurements) == 0 {
		return parseIngredients(r.FormValue("ingredients"))
	}

	maxLen := len(names)
	if len(measurements) > maxLen {
		maxLen = len(measurements)
	}
	out := make([]string, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		name := ""
		measurement := ""
		if i < len(names) {
			name = strings.TrimSpace(names[i])
		}
		if i < len(measurements) {
			measurement = strings.TrimSpace(measurements[i])
		}
		if name == "" {
			continue
		}
		item := name
		if measurement != "" {
			item += " " + measurement
		}
		out = append(out, item)
	}

	if len(out) == 0 {
		return parseIngredients(r.FormValue("ingredients"))
	}
	return out
}

func decodeIngredients(raw []byte) ([]string, error) {
	var legacyIngredients []string
	if err := json.Unmarshal(raw, &legacyIngredients); err == nil {
		return normalizeIngredientStrings(legacyIngredients), nil
	}

	var withSettings []struct {
		Text          string `json:"text"`
		PushToTodoist bool   `json:"push_to_todoist"`
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

func parseIngredientTextFields(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text := strings.TrimSpace(value)
		if text == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

func splitIngredientForPrefill(raw string) (string, string) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", ""
	}

	if strings.HasSuffix(text, ")") {
		open := strings.LastIndex(text, "(")
		if open > 0 {
			name := strings.TrimSpace(text[:open])
			measurement := strings.TrimSpace(text[open+1 : len(text)-1])
			if name != "" && measurement != "" {
				return name, measurement
			}
		}
	}

	parts := strings.Fields(text)
	if len(parts) >= 2 {
		first := strings.TrimSpace(parts[0])
		if isQuantityToken(first) {
			measurement := first
			nameStart := 1

			if len(parts) >= 3 && isUnitToken(parts[1]) {
				measurement = first + " " + parts[1]
				nameStart = 2
			}

			name := strings.TrimSpace(strings.Join(parts[nameStart:], " "))
			if name != "" {
				return name, measurement
			}
		}
	}

	return text, ""
}

var quantityTokenPattern = regexp.MustCompile(`^(?:\d+(?:[./]\d+)?|\d+[¼½¾⅓⅔⅛⅜⅝⅞]?|[¼½¾⅓⅔⅛⅜⅝⅞]|\d+(?:g|kg|mg|ml|l|oz|lb|lbs|cm|mm))$`)

func isQuantityToken(token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	token = strings.Trim(token, ",")
	return quantityTokenPattern.MatchString(token)
}

func isUnitToken(token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	token = strings.Trim(token, ",")
	_, ok := map[string]struct{}{
		"g": {}, "kg": {}, "mg": {}, "ml": {}, "l": {}, "oz": {}, "lb": {}, "lbs": {},
		"tsp": {}, "tbsp": {}, "cup": {}, "cups": {}, "pinch": {}, "pinches": {},
		"pack": {}, "packs": {}, "clove": {}, "cloves": {}, "sprig": {}, "sprigs": {},
	}[token]
	return ok
}

func sourceLabelForURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	switch host {
	case "gousto.co.uk", "www.gousto.co.uk":
		return "Gousto"
	case "bbcgoodfood.com", "www.bbcgoodfood.com":
		return "BBC Good Food"
	default:
		return ""
	}
}

func resolveImportedImageURL(sourceURL, imageURL string) string {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return ""
	}
	img, err := url.Parse(imageURL)
	if err != nil {
		return ""
	}
	if img.IsAbs() {
		return img.String()
	}
	base, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil {
		return ""
	}
	return base.ResolveReference(img).String()
}

func parseSelectedIngredientIndexes(raw []string) ([]int, error) {
	indexes := make([]int, 0, len(raw))
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		idx, err := strconv.Atoi(v)
		if err != nil {
			return nil, err
		}
		indexes = append(indexes, idx)
	}
	return indexes, nil
}

func (a *App) externalBaseURL(r *http.Request) string {
	if a.baseURL != "" {
		return a.baseURL
	}

	host := strings.TrimSpace(r.Host)
	if host == "" {
		host = "localhost:8080"
	}
	return "http://" + host
}

func resolveBaseURL(flagBaseURL, flagLocalIP, listenAddr string) string {
	if v := strings.TrimSpace(flagBaseURL); v != "" {
		return normalizeBaseURL(v, listenAddr)
	}
	if v := strings.TrimSpace(flagLocalIP); v != "" {
		return normalizeBaseURL(v, listenAddr)
	}
	if v := strings.TrimSpace(os.Getenv("BASE_URL")); v != "" {
		return normalizeBaseURL(v, listenAddr)
	}
	if v := strings.TrimSpace(os.Getenv("LOCAL_IP")); v != "" {
		return normalizeBaseURL(v, listenAddr)
	}
	return ""
}

func normalizeBaseURL(raw, listenAddr string) string {
	v := strings.TrimRight(strings.TrimSpace(raw), "/")
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
		return v
	}
	if strings.Contains(v, ":") {
		return "http://" + v
	}
	port := "8080"
	if strings.HasPrefix(listenAddr, ":") && len(listenAddr) > 1 {
		port = listenAddr[1:]
	}
	return fmt.Sprintf("http://%s:%s", v, port)
}

func resolveTodoistProjectID(flagProjectID string) string {
	if v := strings.TrimSpace(flagProjectID); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("TODOIST_PROJECT_ID")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("TODOIST_PROJECT")); v != "" {
		return v
	}
	return ""
}

func resolveTodoistAPIBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("TODOIST_API_BASE_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://api.todoist.com/api/v1"
}

func (a *App) redirectError(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?error="+urlQueryEscape(msg), http.StatusSeeOther)
}

func (a *App) redirectNotice(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?notice="+urlQueryEscape(msg), http.StatusSeeOther)
}

func urlQueryEscape(s string) string {
	return url.QueryEscape(s)
}
