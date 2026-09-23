// Package webauthn reads the two ceremonies a security key or passkey takes
// part in: registering one with an account, and answering a sign-in with it.
//
// What a browser hands back is CBOR and a signature over bytes the
// authenticator wrote, so this package reads only the CBOR a WebAuthn message
// uses and checks the signature itself, rather than trusting anything the
// page says.
package webauthn

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// cborValue is one decoded item: a map, a list, a number, some bytes or text.
type cborValue struct {
	kind    cborKind
	number  int64
	bytes   []byte
	text    string
	list    []cborValue
	entries []cborEntry
	boolean bool
}

type cborEntry struct{ key, value cborValue }

type cborKind int

const (
	cborUnsigned cborKind = iota
	cborNegative
	cborBytes
	cborText
	cborList
	cborMap
	cborSimple
)

// cborMaxItems caps how much one message may carry, so a hostile message
// cannot ask for an enormous allocation.
const cborMaxItems = 1 << 16

// decodeCBOR reads one item and answers what follows it.
func decodeCBOR(data []byte) (cborValue, []byte, error) {
	if len(data) == 0 {
		return cborValue{}, nil, errors.New("the message ends where a value was expected")
	}
	major := data[0] >> 5
	argument, rest, err := cborArgument(data)
	if err != nil {
		return cborValue{}, nil, err
	}
	switch major {
	case 0:
		// A CBOR unsigned number may be larger than a signed one holds, and
		// nothing a WebAuthn message carries is: an algorithm, a length, a
		// counter. One that large is refused rather than wrapped.
		if argument > math.MaxInt64 {
			return cborValue{}, nil, errors.New("a number in the message is too large")
		}
		return cborValue{kind: cborUnsigned, number: int64(argument)}, rest, nil
	case 1:
		if argument > math.MaxInt64 {
			return cborValue{}, nil, errors.New("a number in the message is too large")
		}
		return cborValue{kind: cborNegative, number: -1 - int64(argument)}, rest, nil
	case 2, 3:
		if argument > uint64(len(rest)) {
			return cborValue{}, nil, errors.New("the message says it is longer than it is")
		}
		value := rest[:argument]
		rest = rest[argument:]
		if major == 2 {
			return cborValue{kind: cborBytes, bytes: value}, rest, nil
		}
		return cborValue{kind: cborText, text: string(value)}, rest, nil
	case 4, 5:
		if argument > cborMaxItems {
			return cborValue{}, nil, errors.New("the message holds more than this reads")
		}
		if major == 4 {
			list := make([]cborValue, 0, argument)
			for index := uint64(0); index < argument; index++ {
				item, remainder, err := decodeCBOR(rest)
				if err != nil {
					return cborValue{}, nil, err
				}
				list, rest = append(list, item), remainder
			}
			return cborValue{kind: cborList, list: list}, rest, nil
		}
		entries := make([]cborEntry, 0, argument)
		for index := uint64(0); index < argument; index++ {
			key, remainder, err := decodeCBOR(rest)
			if err != nil {
				return cborValue{}, nil, err
			}
			value, remainder, err := decodeCBOR(remainder)
			if err != nil {
				return cborValue{}, nil, err
			}
			entries, rest = append(entries, cborEntry{key: key, value: value}), remainder
		}
		return cborValue{kind: cborMap, entries: entries}, rest, nil
	case 7:
		switch data[0] & 0x1f {
		case 20:
			return cborValue{kind: cborSimple, boolean: false}, rest, nil
		case 21:
			return cborValue{kind: cborSimple, boolean: true}, rest, nil
		case 22, 23:
			return cborValue{kind: cborSimple}, rest, nil
		}
		return cborValue{}, nil, fmt.Errorf("the message carries a value this does not read (0x%02x)", data[0])
	}
	return cborValue{}, nil, fmt.Errorf("the message carries a value this does not read (0x%02x)", data[0])
}

// cborArgument reads the length or number that follows an item's first byte.
func cborArgument(data []byte) (uint64, []byte, error) {
	switch low := data[0] & 0x1f; {
	case low < 24:
		return uint64(low), data[1:], nil
	case low == 24:
		if len(data) < 2 {
			return 0, nil, errors.New("the message ends inside a value")
		}
		return uint64(data[1]), data[2:], nil
	case low == 25:
		if len(data) < 3 {
			return 0, nil, errors.New("the message ends inside a value")
		}
		return uint64(binary.BigEndian.Uint16(data[1:3])), data[3:], nil
	case low == 26:
		if len(data) < 5 {
			return 0, nil, errors.New("the message ends inside a value")
		}
		return uint64(binary.BigEndian.Uint32(data[1:5])), data[5:], nil
	case low == 27:
		if len(data) < 9 {
			return 0, nil, errors.New("the message ends inside a value")
		}
		return binary.BigEndian.Uint64(data[1:9]), data[9:], nil
	}
	return 0, nil, errors.New("the message carries a length this does not read")
}

// value is what a map holds under a text key.
func (v cborValue) value(key string) (cborValue, bool) {
	for _, entry := range v.entries {
		if entry.key.kind == cborText && entry.key.text == key {
			return entry.value, true
		}
	}
	return cborValue{}, false
}

// label is what a COSE key holds under a numeric label.
func (v cborValue) label(key int64) (cborValue, bool) {
	for _, entry := range v.entries {
		switch entry.key.kind {
		case cborUnsigned, cborNegative:
			if entry.key.number == key {
				return entry.value, true
			}
		}
	}
	return cborValue{}, false
}
