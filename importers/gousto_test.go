package importers

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestParseGoustoHTML_WithSavedPageHTML(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("testdata", "gousto_open_steak_sandwich_page.html")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	_, err = parseGoustoHTML(string(raw))
	if !errors.Is(err, ErrParseFailed) {
		t.Fatalf("expected ErrParseFailed for shell HTML, got: %v", err)
	}
}

func TestParseGoustoHTML_WithRecipeJSONLD(t *testing.T) {
	t.Parallel()

	html := `<!doctype html>
<html>
<head>
<script type="application/ld+json">
{
  "@context": "https://schema.org",
  "@type": "Recipe",
  "name": "Open Steak Sandwich",
  "recipeYield": "2 servings",
  "recipeIngredient": [
    "300g beef steak",
    "2 ciabatta rolls",
    "1 red onion"
  ]
}
</script>
</head>
<body><h1>Open Steak Sandwich</h1></body>
</html>`

	parsed, err := parseGoustoHTML(html)
	if err != nil {
		t.Fatalf("parse html: %v", err)
	}

	if parsed.Name != "Open Steak Sandwich" {
		t.Fatalf("expected name to be parsed, got %q", parsed.Name)
	}
	if len(parsed.Ingredients) != 3 {
		t.Fatalf("expected 3 ingredients, got %d", len(parsed.Ingredients))
	}
	if len(parsed.Warnings) != 0 {
		t.Fatalf("expected no warnings for 2 servings, got %v", parsed.Warnings)
	}
}

func TestExtractRecipeParam(t *testing.T) {
	t.Parallel()

	u, err := url.Parse("https://www.gousto.co.uk/cookbook/beef-recipes/open-steak-sandwich-balsamic-onions-chips")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	param, err := extractRecipeParam(u)
	if err != nil {
		t.Fatalf("extract recipe param: %v", err)
	}
	if param != "open-steak-sandwich-balsamic-onions-chips" {
		t.Fatalf("unexpected recipe param: %q", param)
	}
}

func TestBuildImportedRecipeFromEntry_UsesTwoPeopleFilter(t *testing.T) {
	t.Parallel()

	entry := goustoRecipeEntry{
		Title: "Steak Sandwich",
		Ingredients: []goustoRecipeIngredient{
			{Label: "Steak 300g", GoustoUUID: "a"},
			{Label: "Ciabatta x2", GoustoUUID: "b"},
			{Label: "Onion", GoustoUUID: "c"},
		},
		PortionSizes: []goustoPortionSize{
			{Portions: 2, IngredientsSkus: []goustoPortionIngredient{{ID: "a"}, {ID: "c"}}},
		},
	}

	parsed, err := buildImportedRecipeFromEntry(entry, 2)
	if err != nil {
		t.Fatalf("build imported recipe: %v", err)
	}
	if len(parsed.Ingredients) != 2 {
		t.Fatalf("expected 2 filtered ingredients, got %d", len(parsed.Ingredients))
	}
	if parsed.Ingredients[0] != "Steak 300g" || parsed.Ingredients[1] != "Onion" {
		t.Fatalf("unexpected filtered ingredients: %#v", parsed.Ingredients)
	}
	if len(parsed.Warnings) != 0 {
		t.Fatalf("expected no warning, got %v", parsed.Warnings)
	}
}

func TestBuildImportedRecipeFromEntry_FallbackWarningWhenNoTwoPortionData(t *testing.T) {
	t.Parallel()

	entry := goustoRecipeEntry{
		Title: "Steak Sandwich",
		Ingredients: []goustoRecipeIngredient{
			{Label: "Steak 300g", GoustoUUID: "a"},
			{Label: "Ciabatta x2", GoustoUUID: "b"},
		},
		PortionSizes: []goustoPortionSize{},
	}

	parsed, err := buildImportedRecipeFromEntry(entry, 2)
	if err != nil {
		t.Fatalf("build imported recipe: %v", err)
	}
	if len(parsed.Ingredients) != 2 {
		t.Fatalf("expected fallback ingredients, got %d", len(parsed.Ingredients))
	}
	if len(parsed.Warnings) == 0 {
		t.Fatalf("expected warning for fallback amounts")
	}
}
