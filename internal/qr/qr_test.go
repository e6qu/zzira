package qr

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestASymbolMatchesAKnownGoodEncoder compares whole symbols, module for
// module, with ones written by segno, an encoder with no connection to this
// one. The two lengths fill a version 1 and a version 7 symbol exactly, so no
// padding convention is involved, and the version 7 symbol also carries the
// version information a smaller one does not.
func TestASymbolMatchesAKnownGoodEncoder(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range strings.Split(strings.TrimSpace(string(raw)), "--- ")[1:] {
		lines := strings.Split(strings.TrimSpace(block), "\n")
		var length, wantVersion, mask int
		if _, err := fmt.Sscanf(lines[0], "%d %d %d", &length, &wantVersion, &mask); err != nil {
			t.Fatal(err)
		}
		matrix, err := encode(strings.Repeat("x", length), mask)
		if err != nil {
			t.Fatal(err)
		}
		if matrix.Version != wantVersion {
			t.Fatalf("%d bytes made a version %d symbol, want %d", length, matrix.Version, wantVersion)
		}
		for y, line := range lines[1:] {
			for x, module := range line {
				if matrix.At(x, y) != (module == '#') {
					t.Fatalf("version %d mask %d: module %d,%d is %v\n%s\n%s", wantVersion, mask, x, y, matrix.At(x, y), render(matrix), strings.Join(lines[1:], "\n"))
				}
			}
		}
	}
}

// TestASymbolReadsBackAsWhatWasEncoded takes each symbol apart the way a
// reader does: it unmasks the data, undoes the interleaving, checks every
// block against its error correction, and reads the mode, the length and the
// bytes back out.
func TestASymbolReadsBackAsWhatWasEncoded(t *testing.T) {
	for _, text := range []string{
		"a",
		"hello",
		"otpauth://totp/ZZIRA:ana%40example.test?algorithm=SHA1&digits=6&issuer=ZZIRA&period=30&secret=JBSWY3DPEHPK3PXP",
		"https://zzira.test/profile/two-step?enrol=" + strings.Repeat("k", 60),
		strings.Repeat("Ünicöde ", 12),
		strings.Repeat("x", 213),
		strings.Repeat("x", 666),
	} {
		matrix, err := Encode(text)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		read, err := decode(matrix)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if read != text {
			t.Fatalf("read back %q, want %q", read, text)
		}
	}
	if _, err := Encode(""); err == nil {
		t.Fatal("an empty symbol was encoded")
	}
	if _, err := Encode(strings.Repeat("x", 667)); err == nil {
		t.Fatal("667 bytes fit in a version 20 symbol")
	}
}

// TestTheFunctionPatternsAreWhereAReaderLooks checks the parts of a symbol a
// reader finds before it reads anything: the three finders, the timing lines
// and the module that is always dark.
func TestTheFunctionPatternsAreWhereAReaderLooks(t *testing.T) {
	matrix, err := Encode("otpauth://totp/ZZIRA:ana%40example.test?secret=JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	for _, corner := range [][2]int{{0, 0}, {matrix.Size - 7, 0}, {0, matrix.Size - 7}} {
		for y := 0; y < 7; y++ {
			for x := 0; x < 7; x++ {
				edge := x == 0 || x == 6 || y == 0 || y == 6
				centre := x >= 2 && x <= 4 && y >= 2 && y <= 4
				if matrix.At(corner[0]+x, corner[1]+y) != (edge || centre) {
					t.Fatalf("the finder at %v is wrong at %d,%d", corner, x, y)
				}
			}
		}
	}
	for i := 8; i < matrix.Size-8; i++ {
		if matrix.At(i, 6) != (i%2 == 0) || matrix.At(6, i) != (i%2 == 0) {
			t.Fatalf("the timing pattern breaks at %d", i)
		}
	}
	if !matrix.At(8, matrix.Size-8) {
		t.Fatal("the module that is always dark is light")
	}
}

func render(m *Matrix) string {
	lines := make([]string, 0, m.Size)
	for y := 0; y < m.Size; y++ {
		line := make([]byte, m.Size)
		for x := 0; x < m.Size; x++ {
			line[x] = '.'
			if m.At(x, y) {
				line[x] = '#'
			}
		}
		lines = append(lines, string(line))
	}
	return strings.Join(lines, "\n")
}

// decode reads a symbol the way a reader does, and is how the tests check
// that what was written can be read.
func decode(m *Matrix) (string, error) {
	v := versions[m.Version]
	reserved := make([]bool, m.Size*m.Size)
	// The function patterns are wherever a fresh symbol of this version puts
	// them, so they can be marked without reading them.
	blank := &Matrix{Size: m.Size, Version: m.Version, modules: make([]bool, m.Size*m.Size)}
	blank.drawPatterns(reserved, v)

	bits := make([]bool, 0, m.Size*m.Size)
	upward := true
	for right := m.Size - 1; right > 0; right -= 2 {
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
				bits = append(bits, m.At(x, y) != mask(m.Mask, x, y))
			}
		}
		upward = !upward
	}
	codewords := make([]byte, len(bits)/8)
	for i, bit := range bits[:len(codewords)*8] {
		if bit {
			codewords[i/8] |= 1 << (7 - i%8)
		}
	}

	// Undo the interleaving, then check each block against its error
	// correction: every syndrome of an undamaged block is zero.
	lengths := make([]int, 0, v.blocks1+v.blocks2)
	for i := 0; i < v.blocks1; i++ {
		lengths = append(lengths, v.len1)
	}
	for i := 0; i < v.blocks2; i++ {
		lengths = append(lengths, v.len2)
	}
	blocks := make([][]byte, len(lengths))
	at := 0
	for i := 0; i < v.len2 || i < v.len1; i++ {
		for b, length := range lengths {
			if i < length {
				blocks[b] = append(blocks[b], codewords[at])
				at++
			}
		}
	}
	for i := 0; i < v.ecPerBlock; i++ {
		for b := range blocks {
			blocks[b] = append(blocks[b], codewords[at])
			at++
		}
	}
	for b, block := range blocks {
		for power := 0; power < v.ecPerBlock; power++ {
			syndrome := byte(0)
			for _, coefficient := range block {
				syndrome = gfMultiply(syndrome, gfExp[power]) ^ coefficient
			}
			if syndrome != 0 {
				return "", fmt.Errorf("block %d does not match its error correction", b)
			}
		}
	}

	data := make([]byte, 0, v.dataCodewords())
	for b, block := range blocks {
		data = append(data, block[:lengths[b]]...)
	}
	reader := &bitReader{bytes: data}
	if mode := reader.read(4); mode != 0b0100 {
		return "", fmt.Errorf("mode %04b is not byte mode", mode)
	}
	length := reader.read(countBits(m.Version))
	if length > len(data) {
		return "", fmt.Errorf("a length of %d is longer than the symbol", length)
	}
	out := make([]byte, length)
	for i := range out {
		out[i] = byte(reader.read(8))
	}
	return string(out), nil
}

type bitReader struct {
	bytes []byte
	at    int
}

func (b *bitReader) read(width int) int {
	value := 0
	for i := 0; i < width; i++ {
		value <<= 1
		if b.at/8 < len(b.bytes) && b.bytes[b.at/8]&(1<<(7-b.at%8)) != 0 {
			value |= 1
		}
		b.at++
	}
	return value
}

// TestTheChosenMaskIsTheLowestScoringOne checks the encoder keeps the mask
// the standard's penalty rules prefer, rather than the first that works.
func TestTheChosenMaskIsTheLowestScoringOne(t *testing.T) {
	const text = "otpauth://totp/ZZIRA:ana%40example.test?secret=JBSWY3DPEHPK3PXP&issuer=ZZIRA"
	chosen, err := Encode(text)
	if err != nil {
		t.Fatal(err)
	}
	best, bestScore := -1, 0
	for pattern := 0; pattern < 8; pattern++ {
		candidate, err := encode(text, pattern)
		if err != nil {
			t.Fatal(err)
		}
		// The format information is written after a mask is chosen, so it is
		// not part of what the rules score.
		score := scoreWithoutFormat(candidate, pattern)
		if best < 0 || score < bestScore {
			best, bestScore = pattern, score
		}
	}
	if chosen.Mask != best {
		t.Fatalf("the encoder chose mask %d, and mask %d scores lowest", chosen.Mask, best)
	}
}

// scoreWithoutFormat scores a finished symbol as the encoder saw it, with the
// format and version information left light.
func scoreWithoutFormat(m *Matrix, pattern int) int {
	blank := &Matrix{Size: m.Size, Version: m.Version, Mask: pattern, modules: make([]bool, m.Size*m.Size)}
	copy(blank.modules, m.modules)
	reserved := make([]bool, m.Size*m.Size)
	fresh := &Matrix{Size: m.Size, Version: m.Version, modules: make([]bool, m.Size*m.Size)}
	fresh.drawPatterns(reserved, versions[m.Version])
	for i := range blank.modules {
		if reserved[i] && !fresh.modules[i] {
			blank.modules[i] = false
		}
	}
	return blank.penalty()
}

// TestThePathDrawsTheSymbol reads the SVG path back into modules and compares
// it with the symbol, so what a page draws is what was encoded.
func TestThePathDrawsTheSymbol(t *testing.T) {
	matrix, err := Encode("otpauth://totp/ZZIRA:ana%40example.test?secret=JBSWY3DPEHPK3PXP&issuer=ZZIRA&period=30")
	if err != nil {
		t.Fatal(err)
	}
	if matrix.Extent() != matrix.Size+8 {
		t.Fatalf("the quiet zone is %d modules wide", (matrix.Extent()-matrix.Size)/2)
	}
	drawn := make([]bool, matrix.Size*matrix.Size)
	for _, shape := range strings.Split(matrix.SVGPath(), "z") {
		if shape == "" {
			continue
		}
		var x, y, run, back int
		if _, err := fmt.Sscanf(shape, "M%d %dh%dv1h-%d", &x, &y, &run, &back); err != nil {
			t.Fatalf("shape %q: %v", shape, err)
		}
		if run != back {
			t.Fatalf("shape %q does not close", shape)
		}
		for i := 0; i < run; i++ {
			drawn[(y-4)*matrix.Size+(x-4+i)] = true
		}
	}
	for y := 0; y < matrix.Size; y++ {
		for x := 0; x < matrix.Size; x++ {
			if drawn[y*matrix.Size+x] != matrix.At(x, y) {
				t.Fatalf("the path draws %d,%d as %v", x, y, drawn[y*matrix.Size+x])
			}
		}
	}
}
