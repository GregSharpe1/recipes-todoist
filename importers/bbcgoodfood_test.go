package importers

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestBBCGoodFoodImporter_CanHandle(t *testing.T) {
	t.Parallel()

	importer := NewBBCGoodFoodImporter(nil)
	u, _ := url.Parse("https://www.bbcgoodfood.com/recipes/chicken-kale-mushroom-pot-pie")
	if !importer.CanHandle(u) {
		t.Fatalf("expected bbcgoodfood URL to be supported")
	}

	u2, _ := url.Parse("https://www.gousto.co.uk/cookbook/anything")
	if importer.CanHandle(u2) {
		t.Fatalf("did not expect gousto URL to be supported by bbc importer")
	}
}

func TestParseBBCGoodFoodHTML_WithSavedPageHTML(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("testdata", "bbcgoodfood_chicken_pot_pie_page.html")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	parsed, err := parseBBCGoodFoodHTML(string(raw), 2)
	if err != nil {
		t.Fatalf("parse html: %v", err)
	}

	if parsed.Name != "Chicken, kale & mushroom pot pie" {
		t.Fatalf("unexpected recipe name: %q", parsed.Name)
	}
	if len(parsed.Ingredients) != 3 {
		t.Fatalf("expected 3 ingredients, got %d", len(parsed.Ingredients))
	}
	if len(parsed.Warnings) == 0 {
		t.Fatalf("expected warning because serves 4 in fixture")
	}
	if parsed.ImageURL == "" {
		t.Fatalf("expected image URL to be parsed from fixture")
	}
}
