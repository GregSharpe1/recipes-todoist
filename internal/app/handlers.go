package app

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	"todoist-recipes/importers"
)

func (a *App) indexHandler(w http.ResponseWriter, r *http.Request) {
	data, err := a.buildIndexData(r.Context())
	if err != nil {
		http.Error(w, "failed to load recipes", http.StatusInternalServerError)
		return
	}

	data.ActiveTab = currentTab(r)
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
	regularLists, err := a.listRegularLists(ctx)
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
	for _, list := range regularLists {
		data.RegularLists = append(data.RegularLists, regularListCard{
			ID:            list.ID,
			Name:          list.Name,
			Items:         list.Items,
			PushPath:      "/api/push/regular/" + list.ID,
			DeletePath:    "/api/regular-lists/" + list.ID + "/delete",
			SaveItemsPath: "/api/regular-lists/" + list.ID + "/items/save",
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
	data.ActiveTab = currentTab(r)
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

func (a *App) createRegularListHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.redirectError(w, r, "invalid regular list form")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		a.redirectError(w, r, "regular list name is required")
		return
	}
	items := parseIngredientFieldsFromPrefix(r, "regular_")
	if len(items) == 0 {
		a.redirectError(w, r, "at least one item is required")
		return
	}

	if err := a.createRegularList(r.Context(), name, items); err != nil {
		a.redirectError(w, r, "failed to save regular list")
		return
	}
	a.redirectNotice(w, r, "regular list added")
}

func (a *App) deleteRegularListHandler(w http.ResponseWriter, r *http.Request) {
	listID := r.PathValue("id")
	if listID == "" {
		http.NotFound(w, r)
		return
	}

	deleted, err := a.softDeleteRegularListByID(r.Context(), listID)
	if err != nil {
		a.redirectError(w, r, "failed to delete regular list")
		return
	}
	if !deleted {
		a.redirectError(w, r, "regular list not found")
		return
	}
	a.redirectNotice(w, r, "regular list archived")
}

func (a *App) saveRegularItemsHandler(w http.ResponseWriter, r *http.Request) {
	listID := r.PathValue("id")
	if listID == "" {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.redirectError(w, r, "invalid item form")
		return
	}

	items := parseIngredientTextFields(r.Form["item_text[]"])
	if len(items) == 0 {
		a.redirectError(w, r, "at least one item is required")
		return
	}
	if err := a.updateRegularListItems(r.Context(), listID, items); err != nil {
		a.redirectError(w, r, "failed to save regular list items")
		return
	}
	a.redirectNotice(w, r, "regular list items saved")
}

func (a *App) pushRegularListHandler(w http.ResponseWriter, r *http.Request) {
	listID := r.PathValue("id")
	if listID == "" {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.redirectError(w, r, "invalid push request")
		return
	}

	selectionMode := strings.TrimSpace(r.FormValue("selection_mode")) == "1"
	selectedIndexes, err := parseSelectedIngredientIndexes(r.Form["item_idx[]"])
	if err != nil {
		a.redirectError(w, r, "invalid item selection")
		return
	}

	if err := a.PushRegularListToTodoist(r.Context(), listID, selectedIndexes, selectionMode); err != nil {
		a.redirectError(w, r, err.Error())
		return
	}
	a.redirectNotice(w, r, "items sent to Todoist")
}

func currentTab(r *http.Request) string {
	tab := strings.TrimSpace(r.URL.Query().Get("tab"))
	if tab == "regular" {
		return "regular"
	}
	return "recipes"
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
