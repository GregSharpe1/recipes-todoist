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
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
	qrcode "github.com/skip2/go-qrcode"
)

type Recipe struct {
	ID          string
	Name        string
	ImagePath   string
	Ingredients []string
}

type recipeCard struct {
	ID          string
	Name        string
	ImagePath   string
	Ingredients []string
	QRPath      string
	PushPath    string
}

type indexData struct {
	Recipes []recipeCard
	Error   string
	Notice  string
}

type App struct {
	uploadDir  string
	tmpl       *template.Template
	httpClient *http.Client
	baseURL    string
	projectID  string
	apiBaseURL string
	db         *sql.DB
}

func (a *App) indexHandler(w http.ResponseWriter, r *http.Request) {
	recipes, err := a.listRecipes(r.Context())
	if err != nil {
		http.Error(w, "failed to load recipes", http.StatusInternalServerError)
		return
	}

	data := indexData{
		Error:  r.URL.Query().Get("error"),
		Notice: r.URL.Query().Get("notice"),
	}
	for _, recipe := range recipes {
		data.Recipes = append(data.Recipes, recipeCard{
			ID:          recipe.ID,
			Name:        recipe.Name,
			ImagePath:   recipe.ImagePath,
			Ingredients: recipe.Ingredients,
			QRPath:      "/qr/" + recipe.ID,
			PushPath:    "/api/push/" + recipe.ID,
		})
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
	ingredients := parseIngredients(r.FormValue("ingredients"))
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

	if err := a.PushToTodoist(r.Context(), recipeID); err != nil {
		a.redirectError(w, r, err.Error())
		return
	}
	a.redirectNotice(w, r, "ingredients sent to Todoist")
}

func (a *App) scanHandler(w http.ResponseWriter, r *http.Request) {
	recipeID := r.PathValue("id")
	if recipeID == "" {
		http.NotFound(w, r)
		return
	}

	if err := a.PushToTodoist(r.Context(), recipeID); err != nil {
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

func (a *App) PushToTodoist(ctx context.Context, recipeID string) error {
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

	var wg sync.WaitGroup
	errCh := make(chan error, len(recipe.Ingredients))

	for _, ingredient := range recipe.Ingredients {
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
	ingredients_json JSONB NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`
	_, err := db.ExecContext(ctx, q)
	return err
}

func (a *App) listRecipes(ctx context.Context) ([]Recipe, error) {
	const q = `
SELECT id, name, image_path, ingredients_json
FROM recipes
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
		if err := rows.Scan(&recipe.ID, &recipe.Name, &recipe.ImagePath, &rawIngredients); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(rawIngredients, &recipe.Ingredients); err != nil {
			return nil, err
		}
		recipes = append(recipes, recipe)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return recipes, nil
}

func (a *App) recipeExists(ctx context.Context, id string) (bool, error) {
	const q = `SELECT 1 FROM recipes WHERE id = $1 LIMIT 1`
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
SELECT id, name, image_path, ingredients_json
FROM recipes
WHERE id = $1`

	var recipe Recipe
	var rawIngredients []byte
	err := a.db.QueryRowContext(ctx, q, id).Scan(&recipe.ID, &recipe.Name, &recipe.ImagePath, &rawIngredients)
	if errors.Is(err, sql.ErrNoRows) {
		return Recipe{}, errors.New("recipe not found")
	}
	if err != nil {
		return Recipe{}, err
	}
	if err := json.Unmarshal(rawIngredients, &recipe.Ingredients); err != nil {
		return Recipe{}, err
	}

	return recipe, nil
}

func (a *App) insertRecipe(ctx context.Context, recipe Recipe) (bool, error) {
	rawIngredients, err := json.Marshal(recipe.Ingredients)
	if err != nil {
		return false, err
	}

	const q = `
INSERT INTO recipes (id, name, image_path, ingredients_json)
VALUES ($1, $2, $3, $4::jsonb)
ON CONFLICT (id) DO NOTHING`

	result, err := a.db.ExecContext(ctx, q, recipe.ID, recipe.Name, recipe.ImagePath, rawIngredients)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rows == 1, nil
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
