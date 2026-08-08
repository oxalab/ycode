package interactive

// add_provider_test.go locks in slugify (the model-id/provider-key derivation for
// the "Add custom provider" flow). The full form + models.json write is exercised
// end-to-end on a real terminal; the SaveCustomProvider persistence is covered in
// the core package test.

import "testing"

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"My Provider":        "my-provider",
		"  Acme Corp  ":      "acme-corp",
		"GPT-4o mini":        "gpt-4o-mini",
		"OpenAI___Proxy":     "openai-proxy",
		"---weird---":        "weird",
		"   ":                "custom",
		"":                   "custom",
		"Café Résumé":        "caf-rsum", // non-ascii chars dropped (not separator-joined)
		"Multi   spaces":     "multi-spaces",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
