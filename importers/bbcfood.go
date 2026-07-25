package importers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var reBBCFoodServingCount = regexp.MustCompile(`\d+`)

type BBCFoodImporter struct {
	httpClient *http.Client
}

func NewBBCFoodImporter(client *http.Client) *BBCFoodImporter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &BBCFoodImporter{httpClient: client}
}

func (b *BBCFoodImporter) CanHandle(u *url.URL) bool {
	if u == nil {
		return false
	}

	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme != "http" && scheme != "https" {
		return false
	}

	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host != "bbc.co.uk" && host != "www.bbc.co.uk" {
		return false
	}
	if port := u.Port(); port != "" && !((scheme == "http" && port == "80") || (scheme == "https" && port == "443")) {
		return false
	}

	parts := strings.Split(strings.Trim(strings.TrimSpace(u.EscapedPath()), "/"), "/")
	return len(parts) == 3 && parts[0] == "food" && parts[1] == "recipes" && parts[2] != ""
}

func (b *BBCFoodImporter) Import(ctx context.Context, req ImportRequest) (ImportedRecipe, error) {
	u, err := url.Parse(req.URL)
	if err != nil || !b.CanHandle(u) {
		return ImportedRecipe{}, ErrUnsupportedSource
	}

	html, err := b.fetchHTML(ctx, u.String())
	if err != nil {
		return ImportedRecipe{}, err
	}

	parsed, err := parseBBCFoodHTML(html, req.TargetServings)
	if err != nil {
		return ImportedRecipe{}, err
	}
	parsed.SourceURL = u.String()
	parsed.SourceName = "BBC Food"
	return parsed, nil
}

func (b *BBCFoodImporter) fetchHTML(ctx context.Context, rawURL string) (string, error) {
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

func parseBBCFoodHTML(html string, targetServings int) (ImportedRecipe, error) {
	name, ingredients, yield, imageURL, ok := parseJSONLDRecipes(html)
	if !ok {
		return ImportedRecipe{}, fmt.Errorf("%w: no recipe data found", ErrParseFailed)
	}

	if name == "" {
		name = parseH1(html)
	}
	if name == "" {
		name = "Imported BBC Food Recipe"
	}

	ingredients = normalizeIngredients(ingredients)
	if len(ingredients) == 0 {
		return ImportedRecipe{}, fmt.Errorf("%w: no ingredients found", ErrParseFailed)
	}

	result := ImportedRecipe{
		Name:        name,
		Ingredients: ingredients,
		ImageURL:    imageURL,
	}
	if targetServings > 0 && !bbcFoodYieldIncludes(yield, targetServings) {
		publishedYield := cleanText(yield)
		if publishedYield == "" {
			result.Warnings = append(result.Warnings, "Imported published ingredient quantities; serving size could not be confirmed and amounts were not rescaled.")
		} else {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Imported published quantities for %s; amounts were not rescaled.", publishedYield))
		}
	}
	return result, nil
}

func bbcFoodYieldIncludes(yield string, targetServings int) bool {
	if targetServings <= 0 {
		return false
	}
	for _, raw := range reBBCFoodServingCount.FindAllString(yield, -1) {
		servings, err := strconv.Atoi(raw)
		if err == nil && servings == targetServings {
			return true
		}
	}
	return false
}
