// Package lexorank implements fractional ordering keys: a rank string that
// sorts between two existing ranks without touching siblings. Pure — shared
// by server and wasm client.
//
// The alphabet is two ASCII bands: 'A'-'Z' (the "below" band) then 'a'-'z'
// (the main band). Because 'A' < 'a' in byte order, ranks can always descend
// below the lowest main-band rank by switching bands. 'z' is the per-position
// maximum.
package lexorank

import "fmt"

const (
	alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	minChar  = 'A'
	maxChar  = 'z'
)

func idx(c byte) int {
	if c >= 'a' {
		return 26 + int(c-'a')
	}
	return int(c - 'A')
}

func charAt(i int) byte { return alphabet[i] }

// Mid returns a rank strictly between a and b (a < b, both in the rank
// alphabet). Empty a means "before all"; empty b means "after all".
func Mid(a, b string) (string, error) {
	switch {
	case a != "" && b != "" && a >= b:
		return "", fmt.Errorf("lexorank: a (%q) must sort before b (%q)", a, b)
	case a == "" && b == "":
		return "u", nil
	case a == "":
		return before(b)
	case b == "":
		return after(a)
	}
	return between(a, b)
}

// before returns the largest rank strictly below s (descending band if needed).
func before(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("lexorank: before(\"\") is unbounded")
	}
	c := s[0]
	if c == minChar {
		// below "A…": recurse into the remainder; ranks grow by prefixing 'A'
		rest := s[1:]
		if rest == "" {
			return "", fmt.Errorf("lexorank: cannot go below %q", s)
		}
		sub, err := before(rest)
		if err != nil {
			return "", err
		}
		return string(minChar) + sub, nil
	}
	i := idx(c)
	if i == 0 {
		return "", fmt.Errorf("lexorank: cannot go below %q", s)
	}
	return string(charAt(i-1)) + string(maxChar), nil
}

// after returns the smallest rank strictly above s.
func after(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("lexorank: after(\"\") is unbounded")
	}
	n := s
	for i := len(n) - 1; i >= 0; i-- {
		if n[i] != maxChar {
			return n[:i] + string(charAt(idx(n[i])+1)), nil
		}
	}
	// all max chars: extend with the alphabet midpoint
	return n + midCharStr(""), nil
}

func between(a, b string) (string, error) {
	var out []byte
	for i := 0; ; i++ {
		ia, ib := 0, idx(maxChar) // exhausted side opens toward the alphabet start
		pastB := i >= len(b)
		if i < len(a) {
			ia = idx(a[i])
		}
		if pastB {
			// the ceiling is b's last character itself
			ib = idx(b[len(b)-1])
		} else {
			ib = idx(b[i])
		}
		if ib < ia && !pastB {
			return "", fmt.Errorf("lexorank: no gap between %q and %q at position %d", a, b, i)
		}
		if ib-ia >= 2 {
			out = append(out, charAt((ia+ib)/2))
			return string(out), nil
		}
		// equal, adjacent, or below an open ceiling: take the char and descend
		out = append(out, charAt(ia))
		if i > len(a)+len(b)+8 {
			return "", fmt.Errorf("lexorank: descent did not converge between %q and %q", a, b)
		}
	}
}

// midCharStr returns the middle character of the alphabet (used when extending).
func midCharStr(_ string) string {
	return string(charAt(len(alphabet) / 2))
}

// Initial returns the rank for the first card in a column.
func Initial() string { return "u" }
