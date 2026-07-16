package money

import "testing"

func TestParseAndFormat(t *testing.T) {
	tests := []struct {
		input     string
		cents     int64
		formatted string
	}{
		{"0.01", 1, "0.01"},
		{"0.1", 10, "0.10"},
		{"12", 1200, "12.00"},
		{"999999.99", 99999999, "999999.99"},
	}
	for _, test := range tests {
		cents, err := Parse(test.input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", test.input, err)
		}
		if cents != test.cents || Format(cents) != test.formatted {
			t.Fatalf("Parse(%q) = %d / %s", test.input, cents, Format(cents))
		}
	}
}

func TestParseRejectsUnsafeAmounts(t *testing.T) {
	for _, input := range []string{"", "0", "-1", "1.001", "abc", ".10", "1000000000"} {
		if _, err := Parse(input); err == nil {
			t.Fatalf("Parse(%q) should fail", input)
		}
	}
}
