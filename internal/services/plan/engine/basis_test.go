package engine

import "testing"

// The "A " article is an ordinary sentence opener and folds after a "Based on …, "
// lead; acronyms and proper names are left alone.
func TestLowerFirst(t *testing.T) {
	for in, want := range map[string]string{
		"A seconds-per-service cutover": "a seconds-per-service cutover",
		"We recommend":                  "we recommend",
		"PNI is":                        "PNI is",
		"":                              "",
	} {
		if got := lowerFirst(in); got != want {
			t.Errorf("lowerFirst(%q) = %q, want %q", in, got, want)
		}
	}
}
