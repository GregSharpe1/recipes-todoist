package importers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var rePostContentScript = regexp.MustCompile(`(?is)<script[^>]*id=["']__POST_CONTENT__["'][^>]*>(.*?)</script>`)

type BBCGoodFoodImporter struct {
	httpClient *http.Client
}

func NewBBCGoodFoodImporter(client *http.Client) *BBCGoodFoodImporter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &BBCGoodFoodImporter{httpClient: client}
}

func (b *BBCGoodFoodImporter) CanHandle(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	return host == "bbcgoodfood.com" || host == "www.bbcgoodfood.com"
}

func (b *BBCGoodFoodImporter) Import(ctx context.Context, req ImportRequest) (ImportedRecipe, error) {
	u, err := url.Parse(req.URL)
	if err != nil || !b.CanHandle(u) {
		return ImportedRecipe{}, ErrUnsupportedSource
	}

	html, err := b.fetchHTML(ctx, u.String())
	if err != nil {
		return ImportedRecipe{}, err
	}

	parsed, err := parseBBCGoodFoodHTML(html, req.TargetServings)
	if err != nil {
		return ImportedRecipe{}, err
	}
	parsed.SourceURL = u.String()
	parsed.SourceName = "BBC Good Food"

	return parsed, nil
}

func (b *BBCGoodFoodImporter) fetchHTML(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: build request", ErrFetchFailed)
	}
	req.Header.Set("User-Agent", "todoist-recipes/1.0 (+recipe importer)")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrFetchFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%w: status %d", ErrFetchFailed, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("%w: read response", ErrFetchFailed)
	}

	return string(body), nil
}

type bbcPostContent struct {
	Title    string `json:"title"`
	Servings string `json:"servings"`
	Image    struct {
		URL string `json:"url"`
	} `json:"image"`
	Ingredients []struct {
		Ingredients []struct {
			QuantityText   string `json:"quantityText"`
			IngredientText string `json:"ingredientText"`
			Note           string `json:"note"`
		} `json:"ingredients"`
	} `json:"ingredients"`
}

func parseBBCGoodFoodHTML(html string, targetServings int) (ImportedRecipe, error) {
	if parsed, ok := parseBBCPostContentJSON(html, targetServings); ok {
		return parsed, nil
	}

	name, ingredients, yield, imageURL, ok := parseJSONLDRecipes(html)
	if !ok {
		return ImportedRecipe{}, fmt.Errorf("%w: no recipe data found", ErrParseFailed)
	}

	if name == "" {
		name = parseH1(html)
	}
	if name == "" {
		name = "Imported BBC Good Food Recipe"
	}

	ingredients = normalizeIngredients(ingredients)
	if len(ingredients) == 0 {
		return ImportedRecipe{}, fmt.Errorf("%w: no ingredients found", ErrParseFailed)
	}

	result := ImportedRecipe{Name: name, Ingredients: ingredients, ImageURL: imageURL}
	if targetServings == 2 && !strings.Contains(strings.ToLower(yield), "2") {
		result.Warnings = append(result.Warnings, "Could not confirm 2-person quantities; imported default ingredient amounts.")
	}

	return result, nil
}

func parseBBCPostContentJSON(html string, targetServings int) (ImportedRecipe, bool) {
	m := rePostContentScript.FindStringSubmatch(html)
	if len(m) < 2 {
		return ImportedRecipe{}, false
	}
	raw := strings.TrimSpace(htmlEntityDecode(m[1]))
	if raw == "" {
		return ImportedRecipe{}, false
	}

	var content bbcPostContent
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		return ImportedRecipe{}, false
	}

	ingredients := make([]string, 0)
	for _, group := range content.Ingredients {
		for _, ingredient := range group.Ingredients {
			text := strings.TrimSpace(ingredient.IngredientText)
			if text == "" {
				continue
			}
			line := strings.TrimSpace(ingredient.QuantityText + " " + text)
			if note := strings.TrimSpace(ingredient.Note); note != "" {
				line += " " + note
			}
			ingredients = append(ingredients, line)
		}
	}
	ingredients = normalizeIngredients(ingredients)
	if len(ingredients) == 0 {
		return ImportedRecipe{}, false
	}

	name := cleanText(content.Title)
	if name == "" {
		name = "Imported BBC Good Food Recipe"
	}

	result := ImportedRecipe{Name: name, Ingredients: ingredients, ImageURL: strings.TrimSpace(content.Image.URL)}
	if targetServings == 2 {
		servings := strings.ToLower(strings.TrimSpace(content.Servings))
		if !strings.Contains(servings, "2") {
			result.Warnings = append(result.Warnings, "Could not confirm 2-person quantities; imported default ingredient amounts.")
		}
	}

	return result, true
}
