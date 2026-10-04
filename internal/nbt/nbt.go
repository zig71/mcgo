// Package nbt reads Minecraft's named NBT format, the shape found inside
// Anvil region files. The server needs it to turn a saved chunk into the
// network chunk packet.
package nbt

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// Type is an NBT tag id.
type Type byte

const (
	End Type = iota
	Byte
	Short
	Int
	Long
	Float
	Double
	ByteArray
	String
	List
	Compound
	IntArray
	LongArray
)

// Tag is one NBT tag. Only the field matching Type is meaningful.
type Tag struct {
	Type  Type
	Str   string  // String, and the name of compound fields
	Num   int64   // Byte, Short, Int, Long
	Flt   float64 // Float, Double
	Bytes []byte  // ByteArray
	Ints  []int32 // IntArray
	Longs []int64 // LongArray
	List  []Tag   // List (each element carries its own Type)
	Comp  map[string]Tag
}

// Int returns the tag's value as an int, for any numeric tag.
func (t Tag) Int() int64 {
	switch t.Type {
	case Float, Double:
		return int64(t.Flt)
	default:
		return t.Num
	}
}

// Get looks up a compound field.
func (t Tag) Get(name string) (Tag, bool) {
	v, ok := t.Comp[name]
	return v, ok
}

// ErrTruncated reports input that ended mid-tag.
var ErrTruncated = errors.New("nbt: truncated")

// Read reads a named root tag: the tag type, its name, then the payload.
func Read(r io.Reader) (string, Tag, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", Tag{}, fmt.Errorf("nbt: root type: %w", err)
	}
	t := Type(b[0])
	if t == End {
		return "", Tag{Type: End}, nil
	}
	name, err := readString(r)
	if err != nil {
		return "", Tag{}, err
	}
	tag, err := readPayload(r, t)
	if err != nil {
		return "", Tag{}, err
	}
	return name, tag, nil
}

// ReadCompound reads an unnamed compound payload, as the network format uses.
func ReadCompound(r io.Reader) (Tag, error) {
	return readCompound(r)
}

func readString(r io.Reader) (string, error) {
	var n [2]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return "", ErrTruncated
	}
	buf := make([]byte, binary.BigEndian.Uint16(n[:]))
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", ErrTruncated
	}
	return string(buf), nil
}

func readPayload(r io.Reader, t Type) (Tag, error) {
	switch t {
	case Byte:
		var b [1]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return Tag{}, ErrTruncated
		}
		return Tag{Type: t, Num: int64(int8(b[0]))}, nil
	case Short:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return Tag{}, ErrTruncated
		}
		return Tag{Type: t, Num: int64(int16(binary.BigEndian.Uint16(b[:])))}, nil
	case Int:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return Tag{}, ErrTruncated
		}
		return Tag{Type: t, Num: int64(int32(binary.BigEndian.Uint32(b[:])))}, nil
	case Long:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return Tag{}, ErrTruncated
		}
		return Tag{Type: t, Num: int64(binary.BigEndian.Uint64(b[:]))}, nil
	case Float:
		var b [4]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return Tag{}, ErrTruncated
		}
		return Tag{Type: t, Flt: float64(math.Float32frombits(binary.BigEndian.Uint32(b[:])))}, nil
	case Double:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return Tag{}, ErrTruncated
		}
		return Tag{Type: t, Flt: math.Float64frombits(binary.BigEndian.Uint64(b[:]))}, nil
	case ByteArray:
		n, err := readLen(r)
		if err != nil {
			return Tag{}, err
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return Tag{}, ErrTruncated
		}
		return Tag{Type: t, Bytes: buf}, nil
	case String:
		s, err := readString(r)
		if err != nil {
			return Tag{}, err
		}
		return Tag{Type: t, Str: s}, nil
	case List:
		var b [1]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return Tag{}, ErrTruncated
		}
		elem := Type(b[0])
		n, err := readLen(r)
		if err != nil {
			return Tag{}, err
		}
		out := Tag{Type: t, List: make([]Tag, 0, n)}
		for range n {
			v, err := readPayload(r, elem)
			if err != nil {
				return Tag{}, err
			}
			out.List = append(out.List, v)
		}
		return out, nil
	case Compound:
		return readCompound(r)
	case IntArray:
		n, err := readLen(r)
		if err != nil {
			return Tag{}, err
		}
		out := Tag{Type: t, Ints: make([]int32, n)}
		for i := range n {
			var b [4]byte
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return Tag{}, ErrTruncated
			}
			out.Ints[i] = int32(binary.BigEndian.Uint32(b[:]))
		}
		return out, nil
	case LongArray:
		n, err := readLen(r)
		if err != nil {
			return Tag{}, err
		}
		out := Tag{Type: t, Longs: make([]int64, n)}
		for i := range n {
			var b [8]byte
			if _, err := io.ReadFull(r, b[:]); err != nil {
				return Tag{}, ErrTruncated
			}
			out.Longs[i] = int64(binary.BigEndian.Uint64(b[:]))
		}
		return out, nil
	default:
		return Tag{}, fmt.Errorf("nbt: unknown tag type %d", t)
	}
}

func readCompound(r io.Reader) (Tag, error) {
	out := Tag{Type: Compound, Comp: make(map[string]Tag)}
	for {
		var b [1]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return Tag{}, ErrTruncated
		}
		t := Type(b[0])
		if t == End {
			return out, nil
		}
		name, err := readString(r)
		if err != nil {
			return Tag{}, err
		}
		v, err := readPayload(r, t)
		if err != nil {
			return Tag{}, err
		}
		out.Comp[name] = v
	}
}

func readLen(r io.Reader) (int, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, ErrTruncated
	}
	n := int32(binary.BigEndian.Uint32(b[:]))
	if n < 0 {
		return 0, fmt.Errorf("nbt: negative length %d", n)
	}
	return int(n), nil
}
