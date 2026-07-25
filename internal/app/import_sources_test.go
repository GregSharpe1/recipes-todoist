package app

import "testing"

func TestParseMethodFieldsNormalizesWithoutRemovingDuplicates(t *testing.T) {
	got := parseMethodFields([]string{"  Mix ingredients\n thoroughly  ", "", "Mix ingredients\n thoroughly", "  Bake  "})
	want := []string{"Mix ingredients\n thoroughly", "Mix ingredients\n thoroughly", "Bake"}
	if len(got) != len(want) {
		t.Fatalf("parseMethodFields() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseMethodFields()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSourceLabelForBBCFoodURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		url   string
		label string
	}{
		{name: "www", url: "https://www.bbc.co.uk/food/recipes/prawn_linguine_02687", label: "BBC Food"},
		{name: "bare host", url: "https://bbc.co.uk/food/recipes/prawn_linguine_02687", label: "BBC Food"},
		{name: "lookalike", url: "https://www.bbc.co.uk.evil.test/food/recipes/prawn_linguine_02687", label: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sourceLabelForURL(tt.url); got != tt.label {
				t.Fatalf("sourceLabelForURL(%q) = %q, want %q", tt.url, got, tt.label)
			}
		})
	}
}

func TestBBCFoodImageHostAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host    string
		allowed bool
	}{
		{host: "ichef.bbci.co.uk", allowed: true},
		{host: "ichef.bbc.co.uk", allowed: true},
		{host: "ichef.bbci.co.uk.evil.test", allowed: false},
		{host: "example.com", allowed: false},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := isAllowedImportImageHost(tt.host); got != tt.allowed {
				t.Fatalf("isAllowedImportImageHost(%q) = %v, want %v", tt.host, got, tt.allowed)
			}
		})
	}
}
