package metadata

import "testing"

func TestNormalizeOriginalLanguage(t *testing.T) {
	t.Parallel()
	if got := NormalizeOriginalLanguage("  EN "); got != "en" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeOriginalLanguage(""); got != "" {
		t.Fatalf("empty got %q", got)
	}
}
