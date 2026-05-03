package namespace

import (
	"testing"
)

// store_test.go covers name validation edge cases against validateName
// (the same function used by Store-level registration). The stubInserter
// is declared in register_test.go and is used here for the same package.

func TestValidateName_EdgeCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		desc    string
		name    string
		wantErr bool
	}{
		// Boundary: exactly 3-char label → valid.
		{"min label 3 chars", "abc.jeffe", false},
		// Boundary: exactly 63-char label → valid.
		{"max label 63 chars",
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jeffe",
			false},
		// One under minimum.
		{"label 2 chars", "ab.jeffe", true},
		// One over maximum.
		{"label 64 chars",
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jeffe",
			true},
		// Digits only in label.
		{"digits only label", "123.jeffe", false},
		// Hyphen in the middle → valid.
		{"hyphen in middle", "a-b.jeffe", false},
		// Double hyphen → valid (only leading/trailing prohibited).
		{"double hyphen mid", "a--b.jeffe", false},
		// All hyphens label → both leading and trailing → invalid.
		{"all hyphens", "---.jeffe", true},
		// Period in label → invalid.
		{"period in label", "a.b.jeffe", true},
		// Empty string → invalid.
		{"empty", "", true},
		// Just the suffix → empty label → invalid.
		{"just suffix", ".jeffe", true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			err := validateName(tc.name)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateName(%q) err=%v wantErr=%v", tc.name, err, tc.wantErr)
			}
		})
	}
}
