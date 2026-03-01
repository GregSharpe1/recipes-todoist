package importers

import "errors"

var (
	ErrUnsupportedSource = errors.New("unsupported recipe source")
	ErrFetchFailed       = errors.New("failed to fetch recipe page")
	ErrParseFailed       = errors.New("failed to parse recipe data")
)

type ImportRequest struct {
	URL            string
	TargetServings int
}

type ImportedRecipe struct {
	Name        string
	Ingredients []string
	ImageURL    string
	SourceURL   string
	SourceName  string
	Warnings    []string
}
