package app

import (
	"html/template"
	"net/http"

	"todoist-recipes/importers"
	"todoist-recipes/internal/db"
)

type Recipe = db.Recipe
type RegularList = db.RegularList

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

type regularListCard struct {
	ID            string
	Name          string
	Items         []string
	PushPath      string
	DeletePath    string
	SaveItemsPath string
}

type indexData struct {
	Recipes            []recipeCard
	RegularLists       []regularListCard
	ActiveTab          string
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
	repo             *db.Repository
	importerRegistry *importers.Registry
}
