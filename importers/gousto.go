package importers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

var (
	reJSONLDScript = regexp.MustCompile(`(?is)<script[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	reH1           = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	reTag          = regexp.MustCompile(`(?is)<[^>]+>`)
	reSpace        = regexp.MustCompile(`\s+`)
)

type GoustoImporter struct {
	httpClient *http.Client
}

func NewGoustoImporter(client *http.Client) *GoustoImporter {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &GoustoImporter{httpClient: client}
}

func (g *GoustoImporter) CanHandle(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	return host == "gousto.co.uk" || host == "www.gousto.co.uk"
}

func (g *GoustoImporter) Import(ctx context.Context, req ImportRequest) (ImportedRecipe, error) {
	u, err := url.Parse(req.URL)
	if err != nil || !g.CanHandle(u) {
		return ImportedRecipe{}, ErrUnsupportedSource
	}
	recipeParam, err := extractRecipeParam(u)
	if err == nil {
		imported, apiErr := g.fetchRecipeFromAPI(ctx, recipeParam, req.TargetServings)
		if apiErr == nil {
			imported.SourceURL = u.String()
			imported.SourceName = "Gousto"
			return imported, nil
		}
	}

	html, err := g.fetchHTML(ctx, u.String())
	if err != nil {
		return ImportedRecipe{}, err
	}

	parsed, err := parseGoustoHTML(html)
	if err != nil {
		return ImportedRecipe{}, err
	}
	parsed.SourceURL = u.String()
	parsed.SourceName = "Gousto"

	if req.TargetServings != 2 {
		parsed.Warnings = nil
	}

	return parsed, nil
}

type goustoAPIResponse struct {
	Data struct {
		Entry goustoRecipeEntry `json:"entry"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type goustoRecipeEntry struct {
	Title        string                   `json:"title"`
	Ingredients  []goustoRecipeIngredient `json:"ingredients"`
	PortionSizes []goustoPortionSize      `json:"portion_sizes"`
	Image        string                   `json:"image"`
	Media        struct {
		Image  string `json:"image"`
		Images []struct {
			Image string `json:"image"`
			URL   string `json:"url"`
		} `json:"images"`
	} `json:"media"`
	SEO struct {
		OpenGraphImage string `json:"open_graph_image"`
	} `json:"seo"`
}

type goustoRecipeIngredient struct {
	Label      string `json:"label"`
	Name       string `json:"name"`
	GoustoUUID string `json:"gousto_uuid"`
}

type goustoPortionSize struct {
	Portions        int                       `json:"portions"`
	IngredientsSkus []goustoPortionIngredient `json:"ingredients_skus"`
}

type goustoPortionIngredient struct {
	ID string `json:"id"`
}

func extractRecipeParam(u *url.URL) (string, error) {
	if u == nil {
		return "", fmt.Errorf("%w: empty url", ErrUnsupportedSource)
	}

	cleanPath := strings.TrimSpace(u.EscapedPath())
	cleanPath = strings.TrimPrefix(cleanPath, "/")
	cleanPath = strings.TrimPrefix(cleanPath, "cookbook/")
	cleanPath = strings.Trim(cleanPath, "/")
	if cleanPath == "" {
		return "", fmt.Errorf("%w: invalid recipe path", ErrUnsupportedSource)
	}

	parts := strings.Split(cleanPath, "/")
	recipeParam := strings.TrimSpace(parts[len(parts)-1])
	if recipeParam == "" {
		return "", fmt.Errorf("%w: invalid recipe path", ErrUnsupportedSource)
	}

	decoded, err := url.PathUnescape(recipeParam)
	if err != nil {
		return "", fmt.Errorf("%w: invalid recipe path", ErrUnsupportedSource)
	}
	return decoded, nil
}

func (g *GoustoImporter) fetchRecipeFromAPI(ctx context.Context, recipeParam string, targetServings int) (ImportedRecipe, error) {
	recipeParam = strings.TrimSpace(recipeParam)
	if recipeParam == "" {
		return ImportedRecipe{}, fmt.Errorf("%w: empty recipe identifier", ErrParseFailed)
	}

	endpoint := "https://production-api.gousto.co.uk/cmsreadbroker/v1/" + path.Join("recipe", recipeParam)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ImportedRecipe{}, fmt.Errorf("%w: build api request", ErrFetchFailed)
	}
	req.Header.Set("x-gousto-request-source", "content-webclient")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("User-Agent", "todoist-recipes/1.0 (+recipe importer)")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return ImportedRecipe{}, fmt.Errorf("%w: %v", ErrFetchFailed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ImportedRecipe{}, fmt.Errorf("%w: status %d", ErrFetchFailed, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return ImportedRecipe{}, fmt.Errorf("%w: read response", ErrFetchFailed)
	}

	var payload goustoAPIResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return ImportedRecipe{}, fmt.Errorf("%w: invalid api payload", ErrParseFailed)
	}
	if len(payload.Errors) > 0 {
		msg := strings.TrimSpace(payload.Errors[0].Message)
		if msg == "" {
			msg = "unknown api error"
		}
		return ImportedRecipe{}, fmt.Errorf("%w: %s", ErrParseFailed, msg)
	}

	imported, err := buildImportedRecipeFromEntry(payload.Data.Entry, targetServings)
	if err != nil {
		return ImportedRecipe{}, err
	}
	return imported, nil
}

func buildImportedRecipeFromEntry(entry goustoRecipeEntry, targetServings int) (ImportedRecipe, error) {
	all := normalizeIngredients(collectIngredientLabels(entry.Ingredients))
	if len(all) == 0 {
		return ImportedRecipe{}, fmt.Errorf("%w: no ingredients found", ErrParseFailed)
	}

	result := ImportedRecipe{
		Name:        cleanText(entry.Title),
		Ingredients: all,
		ImageURL:    firstNonEmptyURL(entry.Image, entry.Media.Image, entry.SEO.OpenGraphImage),
	}
	if result.ImageURL == "" && len(entry.Media.Images) > 0 {
		for _, img := range entry.Media.Images {
			result.ImageURL = firstNonEmptyURL(img.Image, img.URL)
			if result.ImageURL != "" {
				break
			}
		}
	}
	if result.Name == "" {
		result.Name = "Imported Gousto Recipe"
	}

	if targetServings != 2 {
		return result, nil
	}

	forTwo, ok := filterForServings(entry.Ingredients, entry.PortionSizes, 2)
	if !ok {
		result.Warnings = append(result.Warnings, "Could not confirm 2-person quantities; imported default ingredient amounts.")
		return result, nil
	}
	result.Ingredients = forTwo
	return result, nil
}

func collectIngredientLabels(ingredients []goustoRecipeIngredient) []string {
	out := make([]string, 0, len(ingredients))
	for _, ingredient := range ingredients {
		label := cleanText(ingredient.Label)
		if label == "" {
			label = cleanText(ingredient.Name)
		}
		if label == "" {
			continue
		}
		out = append(out, label)
	}
	return out
}

func filterForServings(ingredients []goustoRecipeIngredient, portionSizes []goustoPortionSize, servings int) ([]string, bool) {
	ids := make(map[string]struct{})
	for _, portion := range portionSizes {
		if portion.Portions != servings {
			continue
		}
		for _, sku := range portion.IngredientsSkus {
			id := strings.TrimSpace(sku.ID)
			if id == "" {
				continue
			}
			ids[id] = struct{}{}
		}
		break
	}
	if len(ids) == 0 {
		return nil, false
	}

	filtered := make([]string, 0, len(ids))
	for _, ingredient := range ingredients {
		uuid := strings.TrimSpace(ingredient.GoustoUUID)
		if uuid == "" {
			continue
		}
		if _, ok := ids[uuid]; !ok {
			continue
		}
		label := cleanText(ingredient.Label)
		if label == "" {
			label = cleanText(ingredient.Name)
		}
		if label == "" {
			continue
		}
		filtered = append(filtered, label)
	}

	filtered = normalizeIngredients(filtered)
	if len(filtered) == 0 {
		return nil, false
	}
	return filtered, true
}

func (g *GoustoImporter) fetchHTML(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("%w: build request", ErrFetchFailed)
	}
	req.Header.Set("User-Agent", "todoist-recipes/1.0 (+recipe importer)")

	resp, err := g.httpClient.Do(req)
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

func parseGoustoHTML(html string) (ImportedRecipe, error) {
	name, ingredients, yield, imageURL, ok := parseJSONLDRecipes(html)
	if !ok {
		return ImportedRecipe{}, fmt.Errorf("%w: no recipe data found", ErrParseFailed)
	}

	if name == "" {
		name = parseH1(html)
	}
	if name == "" {
		name = "Imported Gousto Recipe"
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
	if !strings.Contains(yield, "2") {
		result.Warnings = append(result.Warnings, "Could not confirm 2-person quantities; imported default ingredient amounts.")
	}

	return result, nil
}

func parseJSONLDRecipes(html string) (string, []string, string, string, bool) {
	scripts := reJSONLDScript.FindAllStringSubmatch(html, -1)
	for _, m := range scripts {
		if len(m) < 2 {
			continue
		}
		raw := strings.TrimSpace(htmlEntityDecode(m[1]))
		if raw == "" {
			continue
		}

		var data any
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			continue
		}

		recipes := extractRecipeNodes(data)
		for _, recipe := range recipes {
			ingredients := extractStringSlice(recipe["recipeIngredient"])
			if len(ingredients) == 0 {
				continue
			}
			name := cleanText(extractFirstString(recipe["name"]))
			yield := cleanText(extractFirstString(recipe["recipeYield"]))
			imageURL := cleanText(extractFirstString(recipe["image"]))
			if imageURL == "" {
				imageURL = cleanText(extractImageURL(recipe["image"]))
			}
			return name, ingredients, yield, imageURL, true
		}
	}

	return "", nil, "", "", false
}

func extractImageURL(v any) string {
	switch t := v.(type) {
	case map[string]any:
		for _, key := range []string{"url", "@id", "contentUrl"} {
			if s, ok := t[key].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	case []any:
		for _, item := range t {
			if s := extractImageURL(item); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstNonEmptyURL(values ...string) string {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func extractRecipeNodes(v any) []map[string]any {
	out := make([]map[string]any, 0)
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			out = append(out, extractRecipeNodes(item)...)
		}
	case map[string]any:
		if looksLikeRecipe(t) {
			out = append(out, t)
		}
		if graph, ok := t["@graph"]; ok {
			out = append(out, extractRecipeNodes(graph)...)
		}
	}
	return out
}

func looksLikeRecipe(m map[string]any) bool {
	v, ok := m["@type"]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case string:
		return strings.EqualFold(t, "Recipe")
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok && strings.EqualFold(s, "Recipe") {
				return true
			}
		}
	}
	return false
}

func extractStringSlice(v any) []string {
	out := make([]string, 0)
	switch t := v.(type) {
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	case string:
		out = append(out, t)
	}
	return out
}

func extractFirstString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		for _, item := range t {
			if s, ok := item.(string); ok {
				return s
			}
		}
	}
	return ""
}

func parseH1(html string) string {
	m := reH1.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return cleanText(m[1])
}

func cleanText(s string) string {
	s = reTag.ReplaceAllString(s, " ")
	s = htmlEntityDecode(s)
	s = reSpace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func normalizeIngredients(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		text := cleanText(item)
		text = strings.TrimPrefix(text, "•")
		text = strings.TrimSpace(text)
		if text == "" || len(text) > 160 {
			continue
		}
		lower := strings.ToLower(text)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		out = append(out, text)
	}
	return out
}

func htmlEntityDecode(s string) string {
	replacer := strings.NewReplacer(
		"&amp;", "&",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
		"&lt;", "<",
		"&gt;", ">",
		"&nbsp;", " ",
	)
	return replacer.Replace(s)
}
