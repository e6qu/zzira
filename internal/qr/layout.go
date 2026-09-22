package qr

// drawPatterns writes everything that is not data: the three finders and
// their separators, the alignment patterns, the timing lines, the dark module
// and the areas the format and version information are written into. Every
// module it writes is marked reserved, so the data skips them.
func (m *Matrix) drawPatterns(reserved []bool, v version) {
	reserve := func(x, y int, dark bool) {
		if x < 0 || y < 0 || x >= m.Size || y >= m.Size {
			return
		}
		m.set(x, y, dark)
		reserved[y*m.Size+x] = true
	}
	finder := func(left, top int) {
		for y := -1; y <= 7; y++ {
			for x := -1; x <= 7; x++ {
				edge := x == 0 || x == 6 || y == 0 || y == 6
				centre := x >= 2 && x <= 4 && y >= 2 && y <= 4
				inside := x >= 0 && x <= 6 && y >= 0 && y <= 6
				reserve(left+x, top+y, inside && (edge || centre))
			}
		}
	}
	finder(0, 0)
	finder(m.Size-7, 0)
	finder(0, m.Size-7)
	for i := 8; i < m.Size-8; i++ {
		dark := i%2 == 0
		reserve(i, 6, dark)
		reserve(6, i, dark)
	}
	for _, y := range v.alignment {
		for _, x := range v.alignment {
			// The three corners already carry a finder pattern.
			if (x == 6 && y == 6) || (x == 6 && y == v.alignment[len(v.alignment)-1]) || (y == 6 && x == v.alignment[len(v.alignment)-1]) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					edge := dx == -2 || dx == 2 || dy == -2 || dy == 2
					reserve(x+dx, y+dy, edge || (dx == 0 && dy == 0))
				}
			}
		}
	}
	// The module above the lower left finder's separator is always dark.
	reserve(8, m.Size-8, true)
	// The format information is written after the mask is chosen; its
	// modules are only reserved here.
	for i := 0; i <= 8; i++ {
		if i != 6 {
			reserve(i, 8, false)
			reserve(8, i, false)
		}
	}
	for i := 0; i < 8; i++ {
		reserve(m.Size-1-i, 8, false)
		if i < 7 {
			reserve(8, m.Size-1-i, false)
		}
	}
	if m.Version >= 7 {
		for i := 0; i < 18; i++ {
			reserve(i/3, m.Size-11+i%3, false)
			reserve(m.Size-11+i%3, i/3, false)
		}
	}
}

// drawCodewords lays the interleaved codewords out in the two-module wide
// columns that run up and down the symbol, from the bottom right.
func (m *Matrix) drawCodewords(reserved []bool, codewords []byte) {
	bit := 0
	upward := true
	for right := m.Size - 1; right > 0; right -= 2 {
		// Column 6 is the vertical timing line, and the columns to its left
		// shift over by one.
		if right == 6 {
			right = 5
		}
		for step := 0; step < m.Size; step++ {
			y := step
			if upward {
				y = m.Size - 1 - step
			}
			for _, x := range [2]int{right, right - 1} {
				if reserved[y*m.Size+x] {
					continue
				}
				dark := false
				if bit < len(codewords)*8 {
					dark = codewords[bit/8]&(1<<(7-bit%8)) != 0
				}
				m.set(x, y, dark)
				bit++
			}
		}
		upward = !upward
	}
}

// mask reports whether the module at x, y is inverted by one of the eight
// data masks.
func mask(pattern, x, y int) bool {
	switch pattern {
	case 0:
		return (y+x)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (y+x)%3 == 0
	case 4:
		return (y/2+x/3)%2 == 0
	case 5:
		return (y*x)%2+(y*x)%3 == 0
	case 6:
		return ((y*x)%2+(y*x)%3)%2 == 0
	default:
		return ((y+x)%2+(y*x)%3)%2 == 0
	}
}

// applyBestMask masks the data with each pattern in turn and keeps the one
// the standard's penalty rules score lowest.
func (m *Matrix) applyBestMask(reserved []bool, forced int) int {
	if forced >= 0 {
		m.toggle(reserved, forced)
		return forced
	}
	best, bestScore := 0, 0
	for pattern := 0; pattern < 8; pattern++ {
		// The standard scores the masked symbol before the format and
		// version information are written into it.
		m.toggle(reserved, pattern)
		score := m.penalty()
		m.toggle(reserved, pattern)
		if pattern == 0 || score < bestScore {
			best, bestScore = pattern, score
		}
	}
	m.toggle(reserved, best)
	return best
}

func (m *Matrix) toggle(reserved []bool, pattern int) {
	for y := 0; y < m.Size; y++ {
		for x := 0; x < m.Size; x++ {
			if reserved[y*m.Size+x] || !mask(pattern, x, y) {
				continue
			}
			m.set(x, y, !m.At(x, y))
		}
	}
}

// penalty scores a masked symbol by the four rules in the standard: long runs
// of one shade, solid blocks, anything that looks like a finder pattern, and
// an unbalanced share of dark modules.
func (m *Matrix) penalty() int {
	score := 0
	line := make([]bool, m.Size)
	for _, byRow := range [2]bool{true, false} {
		for a := 0; a < m.Size; a++ {
			for b := 0; b < m.Size; b++ {
				if byRow {
					line[b] = m.At(b, a)
				} else {
					line[b] = m.At(a, b)
				}
			}
			score += runPenalty(line)
			score += finderPenalty(line)
		}
	}
	for y := 0; y < m.Size-1; y++ {
		for x := 0; x < m.Size-1; x++ {
			if m.At(x, y) == m.At(x+1, y) && m.At(x, y) == m.At(x, y+1) && m.At(x, y) == m.At(x+1, y+1) {
				score += 3
			}
		}
	}
	dark := 0
	for _, module := range m.modules {
		if module {
			dark++
		}
	}
	// How far the share of dark modules is from half, in whole steps of five
	// percent, without rounding the share first.
	total := len(m.modules)
	deviation := dark*100 - total*50
	if deviation < 0 {
		deviation = -deviation
	}
	score += deviation / (5 * total) * 10
	return score
}

func runPenalty(line []bool) int {
	score, run := 0, 1
	for i := 1; i < len(line); i++ {
		if line[i] == line[i-1] {
			run++
			continue
		}
		if run >= 5 {
			score += 3 + run - 5
		}
		run = 1
	}
	if run >= 5 {
		score += 3 + run - 5
	}
	return score
}

// finderPenalty counts the 1:1:3:1:1 pattern that a reader mistakes for a
// finder: dark, light, three dark, light, dark, with four light modules
// before or after it. The edge of the symbol counts as that light area, which
// is what the standard means by "preceded or followed by".
func finderPenalty(line []bool) int {
	core := [7]bool{true, false, true, true, true, false, true}
	light := func(from, to int) bool {
		if from < 0 {
			from = 0
		}
		if to > len(line) {
			to = len(line)
		}
		for i := from; i < to; i++ {
			if line[i] {
				return false
			}
		}
		return true
	}
	score := 0
	for i := 0; i+7 <= len(line); {
		matched := true
		for j := 0; j < 7; j++ {
			if line[i+j] != core[j] {
				matched = false
				break
			}
		}
		if !matched {
			i++
			continue
		}
		if light(i-4, i) || light(i+7, i+11) {
			score += 40
			i += 7
			continue
		}
		// The three dark modules in the middle can start the next pattern.
		i += 4
	}
	return score
}

// drawFormat writes the error correction level and mask, twice, with the BCH
// check bits and the mask the standard applies to them.
func (m *Matrix) drawFormat(pattern int) {
	// Level M is 00.
	bits := pattern
	remainder := bits << 10
	for i := 4; i >= 0; i-- {
		if remainder&(1<<(i+10)) != 0 {
			remainder ^= 0x537 << i
		}
	}
	value := ((bits << 10) | remainder) ^ 0x5412
	for i := 0; i < 15; i++ {
		dark := value&(1<<i) != 0
		switch {
		case i < 6:
			m.set(8, i, dark)
		case i == 6:
			m.set(8, 7, dark)
		case i == 7:
			m.set(8, 8, dark)
		default:
			m.set(8, m.Size-15+i, dark)
		}
		switch {
		case i < 8:
			m.set(m.Size-1-i, 8, dark)
		case i == 8:
			m.set(7, 8, dark)
		default:
			m.set(14-i, 8, dark)
		}
	}
	m.set(8, m.Size-8, true)
}

// drawVersion writes the version information a symbol of version 7 or above
// carries in its two corners.
func (m *Matrix) drawVersion(number int) {
	remainder := number << 12
	for i := 5; i >= 0; i-- {
		if remainder&(1<<(i+12)) != 0 {
			remainder ^= 0x1f25 << i
		}
	}
	value := (number << 12) | remainder
	for i := 0; i < 18; i++ {
		dark := value&(1<<i) != 0
		m.set(i/3, m.Size-11+i%3, dark)
		m.set(m.Size-11+i%3, i/3, dark)
	}
}
