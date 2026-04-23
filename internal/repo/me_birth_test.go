package repo

import "testing"

func TestParseBirthYear(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"1985", 1985, true},
		{"  1990  ", 1990, true},
		{"15.03.1985", 1985, true},
		{"", 0, false},
		{"12", 0, false},
		{"3000", 0, false},
	}
	for _, c := range cases {
		y, ok := parseBirthYear(c.in)
		if ok != c.ok || (c.ok && y != c.want) {
			t.Fatalf("parseBirthYear(%q) = (%d, %v), want (%d, %v)", c.in, y, ok, c.want, c.ok)
		}
	}
}
