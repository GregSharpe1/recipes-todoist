package importers

import (
	"context"
	"net/url"
)

type Importer interface {
	CanHandle(u *url.URL) bool
	Import(ctx context.Context, req ImportRequest) (ImportedRecipe, error)
}

type Registry struct {
	importers []Importer
}

func NewRegistry(importers ...Importer) *Registry {
	return &Registry{importers: importers}
}

func (r *Registry) Find(rawURL string) Importer {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	for _, importer := range r.importers {
		if importer.CanHandle(u) {
			return importer
		}
	}
	return nil
}

func (r *Registry) Import(ctx context.Context, req ImportRequest) (ImportedRecipe, error) {
	importer := r.Find(req.URL)
	if importer == nil {
		return ImportedRecipe{}, ErrUnsupportedSource
	}
	if req.TargetServings <= 0 {
		req.TargetServings = 2
	}
	return importer.Import(ctx, req)
}
