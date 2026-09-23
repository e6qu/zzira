package qr

// The error correction codewords are a Reed-Solomon remainder over GF(256)
// with the primitive polynomial x^8 + x^4 + x^3 + x^2 + 1, which is the field
// QR symbols are defined in.

var (
	gfExp [512]byte
	gfLog [256]byte
)

func init() {
	value := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(value)
		gfLog[value] = byte(i)
		value <<= 1
		if value&0x100 != 0 {
			value ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMultiply(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// generator is the polynomial whose roots are the first count powers of two,
// which is what the remainder is taken against.
func generator(count int) []byte {
	poly := []byte{1}
	for i := 0; i < count; i++ {
		next := make([]byte, len(poly)+1)
		for j, coefficient := range poly {
			next[j] ^= coefficient
			next[j+1] ^= gfMultiply(coefficient, gfExp[i])
		}
		poly = next
	}
	return poly
}

func reedSolomon(data []byte, count int) []byte {
	poly := generator(count)
	remainder := make([]byte, len(data)+count)
	copy(remainder, data)
	for i := 0; i < len(data); i++ {
		lead := remainder[i]
		if lead == 0 {
			continue
		}
		for j, coefficient := range poly {
			remainder[i+j] ^= gfMultiply(coefficient, lead)
		}
	}
	return remainder[len(data):]
}
