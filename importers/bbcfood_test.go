package importers

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBBCFoodImporterCanHandle(t *testing.T) {
	t.Parallel()

	importer := NewBBCFoodImporter(nil)
	tests := []struct {
		name    string
		rawURL  string
		handled bool
	}{
		{name: "www https", rawURL: "https://www.bbc.co.uk/food/recipes/prawn_linguine_02687", handled: true},
		{name: "bare host", rawURL: "http://bbc.co.uk/food/recipes/prawn_linguine_02687", handled: true},
		{name: "lookalike host", rawURL: "https://www.bbc.co.uk.evil.test/food/recipes/prawn_linguine_02687", handled: false},
		{name: "bbc good food", rawURL: "https://www.bbcgoodfood.com/recipes/prawn-linguine", handled: false},
		{name: "unrelated bbc path", rawURL: "https://www.bbc.co.uk/news", handled: false},
		{name: "recipe collection", rawURL: "https://www.bbc.co.uk/food/recipes/", handled: false},
		{name: "nested recipe path", rawURL: "https://www.bbc.co.uk/food/recipes/prawn_linguine_02687/extra", handled: false},
		{name: "nonstandard port", rawURL: "https://www.bbc.co.uk:8443/food/recipes/prawn_linguine_02687", handled: false},
		{name: "unsupported scheme", rawURL: "ftp://www.bbc.co.uk/food/recipes/prawn_linguine_02687", handled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.rawURL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			if got := importer.CanHandle(u); got != tt.handled {
				t.Fatalf("CanHandle(%q) = %v, want %v", tt.rawURL, got, tt.handled)
			}
		})
	}
}

func TestParseBBCFoodHTMLPreservesPublishedQuantities(t *testing.T) {
	t.Parallel()

	raw := readBBCFoodFixture(t)
	parsed, err := parseBBCFoodHTML(raw, 2)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	wantIngredients := []string{
		"320g /11½oz linguine",
		"200g/7oz frozen peas",
		"1 tbsp extra virgin olive oil",
		"1 large garlic clove, finely chopped",
		"large pinch chilli flakes",
		"300–400g/10½–14oz cooked small prawns",
		"250g/9oz mascarpone or 300ml/10fl oz double cream",
		"100g/3½oz rocket",
		"1 unwaxed lemon, juice and finely grated zest of ½ lemon",
		"salt and freshly ground black pepper",
	}
	if parsed.Name != "Prawn linguine" {
		t.Fatalf("Name = %q, want Prawn linguine", parsed.Name)
	}
	if !reflect.DeepEqual(parsed.Ingredients, wantIngredients) {
		t.Fatalf("Ingredients = %#v, want %#v", parsed.Ingredients, wantIngredients)
	}
	if parsed.ImageURL != "https://ichef.bbci.co.uk/food/ic/food_16x9_1600/recipes/prawn_linguine_02687_16x9.jpg" {
		t.Fatalf("unexpected ImageURL: %q", parsed.ImageURL)
	}
	wantWarning := "Imported published quantities for Serves 4; amounts were not rescaled."
	if !reflect.DeepEqual(parsed.Warnings, []string{wantWarning}) {
		t.Fatalf("Warnings = %#v, want %q", parsed.Warnings, wantWarning)
	}
}

func TestParseBBCFoodHTMLMatchingServingHasNoWarning(t *testing.T) {
	t.Parallel()

	parsed, err := parseBBCFoodHTML(readBBCFoodFixture(t), 4)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if len(parsed.Warnings) != 0 {
		t.Fatalf("Warnings = %#v, want none", parsed.Warnings)
	}
}

func TestParseBBCFoodHTMLRejectsMissingRecipeData(t *testing.T) {
	t.Parallel()

	_, err := parseBBCFoodHTML(`<html><body><h1>No structured recipe</h1></body></html>`, 2)
	if !errors.Is(err, ErrParseFailed) {
		t.Fatalf("error = %v, want ErrParseFailed", err)
	}
}

func readBBCFoodFixture(t *testing.T) string {
	t.Helper()

	fixturePath := filepath.Join("testdata", "bbcfood_prawn_linguine_page.html")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(raw)
}
