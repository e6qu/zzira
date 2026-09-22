// Package qr encodes text as a QR symbol, so a page can show a code an
// authenticator app scans instead of asking someone to type a shared secret.
//
// It writes byte mode at error correction level M, which is what an
// authenticator's otpauth URI needs, in the smallest version that holds the
// text. ISO/IEC 18004 defines everything here: the block structure, the
// Reed-Solomon codes, the masks and how the modules are laid out.
package qr

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Matrix is a finished symbol: Size by Size modules, dark or light.
type Matrix struct {
	Size    int
	Version int
	// Mask is the data mask the encoder chose.
	Mask    int
	modules []bool
}

// At reports whether the module in column x of row y is dark. Anything
// outside the symbol is light.
func (m *Matrix) At(x, y int) bool {
	if x < 0 || y < 0 || x >= m.Size || y >= m.Size {
		return false
	}
	return m.modules[y*m.Size+x]
}

func (m *Matrix) set(x, y int, dark bool) {
	m.modules[y*m.Size+x] = dark
}

// version is one version's block structure at error correction level M: how
// many error correction codewords each block carries, and the two groups of
// blocks the data codewords are split between.
type version struct {
	ecPerBlock    int
	blocks1, len1 int
	blocks2, len2 int
	alignment     []int
}

// versions are 1 to 20, which hold up to 666 bytes: more than any otpauth URI
// or setup link a page shows, whatever the address of the person it is for.
var versions = map[int]version{
	1:  {10, 1, 16, 0, 0, nil},
	2:  {16, 1, 28, 0, 0, []int{6, 18}},
	3:  {26, 1, 44, 0, 0, []int{6, 22}},
	4:  {18, 2, 32, 0, 0, []int{6, 26}},
	5:  {24, 2, 43, 0, 0, []int{6, 30}},
	6:  {16, 4, 27, 0, 0, []int{6, 34}},
	7:  {18, 4, 31, 0, 0, []int{6, 22, 38}},
	8:  {22, 2, 38, 2, 39, []int{6, 24, 42}},
	9:  {22, 3, 36, 2, 37, []int{6, 26, 46}},
	10: {26, 4, 43, 1, 44, []int{6, 28, 50}},
	11: {30, 1, 50, 4, 51, []int{6, 30, 54}},
	12: {22, 6, 36, 2, 37, []int{6, 32, 58}},
	13: {22, 8, 37, 1, 38, []int{6, 34, 62}},
	14: {24, 4, 40, 5, 41, []int{6, 26, 46, 66}},
	15: {24, 5, 41, 5, 42, []int{6, 26, 48, 70}},
	16: {28, 7, 45, 3, 46, []int{6, 26, 50, 74}},
	17: {28, 10, 46, 1, 47, []int{6, 30, 54, 78}},
	18: {26, 9, 43, 4, 44, []int{6, 30, 56, 82}},
	19: {26, 3, 44, 11, 45, []int{6, 30, 58, 86}},
	20: {26, 3, 41, 13, 42, []int{6, 34, 62, 90}},
}

const maxVersion = 20

func (v version) dataCodewords() int {
	return v.blocks1*v.len1 + v.blocks2*v.len2
}

// countBits is the width of the character count that follows the mode, which
// widens at version 10.
func countBits(number int) int {
	if number < 10 {
		return 8
	}
	return 16
}

func (v version) capacity(number int) int {
	return (v.dataCodewords()*8 - 4 - countBits(number)) / 8
}

// Encode writes text as the smallest symbol that holds it, masked with the
// pattern the standard's penalty rules prefer.
func Encode(text string) (*Matrix, error) {
	return encode(text, -1)
}

// encode writes one symbol. A mask of -1 lets the penalty rules choose;
// naming one is how the tests compare a symbol with a known good encoder.
func encode(text string, forcedMask int) (*Matrix, error) {
	if text == "" {
		return nil, fmt.Errorf("there is nothing to encode")
	}
	if !utf8.ValidString(text) {
		return nil, fmt.Errorf("the text is not valid UTF-8")
	}
	data := []byte(text)
	number := 0
	for candidate := 1; candidate <= maxVersion; candidate++ {
		if len(data) <= versions[candidate].capacity(candidate) {
			number = candidate
			break
		}
	}
	if number == 0 {
		return nil, fmt.Errorf("%d bytes is more than a version %d symbol holds", len(data), maxVersion)
	}
	chosen := versions[number]
	codewords := encodeCodewords(data, chosen, number)
	matrix := &Matrix{Size: 17 + 4*number, Version: number, modules: nil}
	matrix.modules = make([]bool, matrix.Size*matrix.Size)
	reserved := make([]bool, matrix.Size*matrix.Size)
	matrix.drawPatterns(reserved, chosen)
	matrix.drawCodewords(reserved, codewords)
	matrix.Mask = matrix.applyBestMask(reserved, forcedMask)
	matrix.drawFormat(matrix.Mask)
	if number >= 7 {
		matrix.drawVersion(number)
	}
	return matrix, nil
}

// encodeCodewords writes the byte mode segment, pads it to the version's data
// capacity, and interleaves the blocks with their error correction.
func encodeCodewords(data []byte, v version, number int) []byte {
	bits := &bitWriter{}
	bits.write(0b0100, 4)
	bits.write(len(data), countBits(number))
	for _, value := range data {
		bits.write(int(value), 8)
	}
	total := v.dataCodewords()
	// The terminator is up to four zero bits, and then the last codeword is
	// filled out and the rest padded with the two bytes the standard names.
	for i := 0; i < 4 && bits.length < total*8; i++ {
		bits.write(0, 1)
	}
	for bits.length%8 != 0 {
		bits.write(0, 1)
	}
	pad := []int{0b11101100, 0b00010001}
	for i := 0; bits.length < total*8; i++ {
		bits.write(pad[i%2], 8)
	}

	blocks := make([][]byte, 0, v.blocks1+v.blocks2)
	at := 0
	for i := 0; i < v.blocks1; i++ {
		blocks = append(blocks, bits.bytes[at:at+v.len1])
		at += v.len1
	}
	for i := 0; i < v.blocks2; i++ {
		blocks = append(blocks, bits.bytes[at:at+v.len2])
		at += v.len2
	}
	corrections := make([][]byte, len(blocks))
	for i, block := range blocks {
		corrections[i] = reedSolomon(block, v.ecPerBlock)
	}
	longest := v.len1
	if v.len2 > longest {
		longest = v.len2
	}
	out := make([]byte, 0, total+v.ecPerBlock*len(blocks))
	for i := 0; i < longest; i++ {
		for _, block := range blocks {
			if i < len(block) {
				out = append(out, block[i])
			}
		}
	}
	for i := 0; i < v.ecPerBlock; i++ {
		for _, correction := range corrections {
			out = append(out, correction[i])
		}
	}
	return out
}

type bitWriter struct {
	bytes  []byte
	length int
}

func (b *bitWriter) write(value, width int) {
	for i := width - 1; i >= 0; i-- {
		if b.length%8 == 0 {
			b.bytes = append(b.bytes, 0)
		}
		if value&(1<<i) != 0 {
			b.bytes[b.length/8] |= 1 << (7 - b.length%8)
		}
		b.length++
	}
}

// quietZone is the light margin a reader needs around a symbol, in modules.
const quietZone = 4

// Extent is how wide the symbol is with its quiet zone, in modules, which is
// what an SVG that draws SVGPath uses as its viewBox.
func (m *Matrix) Extent() int {
	return m.Size + 2*quietZone
}

// SVGPath draws the dark modules as one SVG path in module units, offset by
// the quiet zone. The path carries no markup of its own, so a template writes
// it into a path element's d attribute.
func (m *Matrix) SVGPath() string {
	var path strings.Builder
	for y := 0; y < m.Size; y++ {
		for x := 0; x < m.Size; x++ {
			if !m.At(x, y) {
				continue
			}
			// Runs of dark modules are drawn as one rectangle, which keeps
			// the path short enough to sit in a page.
			run := 1
			for x+run < m.Size && m.At(x+run, y) {
				run++
			}
			fmt.Fprintf(&path, "M%d %dh%dv1h-%dz", x+quietZone, y+quietZone, run, run)
			x += run - 1
		}
	}
	return path.String()
}
