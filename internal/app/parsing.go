package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

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

func parseIngredientFieldsFromPrefix(r *http.Request, prefix string) []string {
	names := r.Form[prefix+"ingredient_name[]"]
	measurements := r.Form[prefix+"ingredient_measurement[]"]
	if len(names) == 0 && len(measurements) == 0 {
		return nil
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
