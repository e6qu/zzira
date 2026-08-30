package lexorank

import (
	"sort"
	"strings"
	"testing"
)

func mustMid(t *testing.T, a, b string) string {
	t.Helper()
	m, err := Mid(a, b)
	if err != nil {
		t.Fatalf("Mid(%q,%q): %v", a, b, err)
	}
	if a != "" && m <= a {
		t.Fatalf("Mid(%q,%q)=%q must be > a", a, b, m)
	}
	if b != "" && m >= b {
		t.Fatalf("Mid(%q,%q)=%q must be < b", a, b, m)
	}
	return m
}

func TestMidBasic(t *testing.T) {
	cases := [][2]string{
		{"u", "z"},
		{"a", "u"},
		{"b", "d"},
		{"m", "p"},
		{"", "z"},
		{"m", ""},
		{"ab", "ad"},
		{"az", "b"},
		{"y", "z"},
		{"aaz", "ab"},
	}
	for _, c := range cases {
		mustMid(t, c[0], c[1])
	}
}

func TestMidRebalanceSplits(t *testing.T) {
	// adjacent chars force multi-char descent
	m := mustMid(t, "m", "n")
	if !strings.HasPrefix(m, "m") {
		t.Fatalf("expected descent under m, got %q", m)
	}
	m = mustMid(t, "mz", "n")
	if !strings.HasPrefix(m, "m") {
		t.Fatalf("expected descent under m, got %q", m)
	}
}

func TestManyInsertionsStaySorted(t *testing.T) {
	ranks := []string{"u"}
	// repeatedly insert at the top and at the bottom
	for i := 0; i < 50; i++ {
		ranks = append([]string{mustMid(t, "", ranks[0])}, ranks...)
		ranks = append(ranks, mustMid(t, ranks[len(ranks)-1], ""))
	}
	if !sort.StringsAreSorted(ranks) {
		t.Fatal("ranks not sorted after 100 insertions")
	}
	if len(ranks[0]) > 60 || len(ranks[len(ranks)-1]) > 60 {
		t.Fatalf("ranks grew unreasonably: %d", len(ranks[0]))
	}
}

func TestMidErrors(t *testing.T) {
	if _, err := Mid("z", "a"); err == nil {
		t.Fatal("a>=b must error")
	}
	if _, err := Mid("u", "u"); err == nil {
		t.Fatal("a==b must error")
	}
}

func TestNormalize(t *testing.T) {
	if _, err := Mid("U", "Z"); err != nil {
		t.Fatalf("uppercase should normalize: %v", err)
	}
}
