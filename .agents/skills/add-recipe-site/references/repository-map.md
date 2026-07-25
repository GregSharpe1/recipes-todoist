# Recipe importer repository map

## Importer contract

- `importers/types.go`: request/result types and sentinel errors.
- `importers/registry.go`: ordered domain-specific importer selection; defaults a
  missing target serving count to two.
- `importers/gousto.go`: API-first importer plus shared HTML/Schema.org helpers,
  including `parseJSONLDRecipes`, `normalizeIngredients`, and image extraction.
- `importers/bbcgoodfood.go`: page-data-first importer with JSON-LD fallback.
- `importers/bbcfood.go`: domain/path-allowlisted JSON-LD importer for BBC Food.

Every importer must implement:

```go
type Importer interface {
	CanHandle(u *url.URL) bool
	Import(ctx context.Context, req ImportRequest) (ImportedRecipe, error)
}
```

`ImportedRecipe` requires a usable name and non-empty ingredient list. Populate the
image when published, then set `SourceURL`, `SourceName`, and relevant warnings.

## Integration points

- `internal/app/run.go`: construct and register the importer.
- `internal/app/handlers.go`: keep the unsupported-source message accurate.
- `internal/app/parsing.go`: add the saved-recipe source badge label.
- `internal/app/media.go`: allowlist the source's exact image CDN hostnames.
- `templates/index.html`: keep import heading, placeholder, source URL hint, and
  action wording accurate.
- `README.md`: keep features, usage, and routes accurate.

## Existing application policy

- Import requests currently prefer two-serving variants.
- Preserve the source's published ingredient strings when it does not provide an
  exact two-serving variant.
- Show a warning instead of attempting to scale free-form quantities.
- Resolve relative image URLs in the application after importing.
- Keep source fetching bounded by a timeout and a 4 MiB response limit.

## Test conventions

- Put importer tests beside the implementation.
- Put minimal deterministic HTML under `importers/testdata/`.
- Use table-driven URL cases when several hostname/path boundaries matter.
- Assert sentinel errors with `errors.Is`.
- Run the full Go test suite and build after registration changes.
