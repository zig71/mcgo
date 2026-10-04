package nbt

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"sort"
)

// ErrUnsupported reports a tag this package cannot write.
var ErrUnsupported = errors.New("nbt: unsupported tag")

// Write emits a named root tag: the type, the name, then the payload. That is
// the form Anvil files use.
func Write(w io.Writer, name string, t Tag) error {
	if _, err := w.Write([]byte{byte(t.Type)}); err != nil {
		return err
	}
	if err := writeString(w, name); err != nil {
		return err
	}
	return WritePayload(w, t)
}

// WriteUnnamed emits a type byte and payload with no name: the network form
// used by block entities and registry entries.
func WriteUnnamed(w io.Writer, t Tag) error {
	if _, err := w.Write([]byte{byte(t.Type)}); err != nil {
		return err
	}
	return WritePayload(w, t)
}

// WritePayload emits a tag's payload, without its type byte.
func WritePayload(w io.Writer, t Tag) error {
	switch t.Type {
	case End:
		return nil
	case Byte:
		return write(w, []byte{byte(int8(t.Num))})
	case Short:
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(int16(t.Num)))
		return write(w, b[:])
	case Int:
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(int32(t.Num)))
		return write(w, b[:])
	case Long:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(t.Num))
		return write(w, b[:])
	case Float:
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], math.Float32bits(float32(t.Flt)))
		return write(w, b[:])
	case Double:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], math.Float64bits(t.Flt))
		return write(w, b[:])
	case ByteArray:
		if err := writeLen(w, len(t.Bytes)); err != nil {
			return err
		}
		return write(w, t.Bytes)
	case String:
		return writeString(w, t.Str)
	case List:
		elem := byte(End)
		if len(t.List) > 0 {
			elem = byte(t.List[0].Type)
		}
		if _, err := w.Write([]byte{elem}); err != nil {
			return err
		}
		if err := writeLen(w, len(t.List)); err != nil {
			return err
		}
		for _, e := range t.List {
			if err := WritePayload(w, e); err != nil {
				return err
			}
		}
		return nil
	case Compound:
		return writeCompoundBody(w, t.Comp)
	case IntArray:
		if err := writeLen(w, len(t.Ints)); err != nil {
			return err
		}
		for _, v := range t.Ints {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(v))
			if err := write(w, b[:]); err != nil {
				return err
			}
		}
		return nil
	case LongArray:
		if err := writeLen(w, len(t.Longs)); err != nil {
			return err
		}
		for _, v := range t.Longs {
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], uint64(v))
			if err := write(w, b[:]); err != nil {
				return err
			}
		}
		return nil
	default:
		return ErrUnsupported
	}
}

// writeCompoundBody emits fields in sorted order, so output is deterministic,
// then the TAG_End terminator. Go map order is random; NBT fields are ordered.
func writeCompoundBody(w io.Writer, comp map[string]Tag) error {
	names := make([]string, 0, len(comp))
	for name := range comp {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t := comp[name]
		if _, err := w.Write([]byte{byte(t.Type)}); err != nil {
			return err
		}
		if err := writeString(w, name); err != nil {
			return err
		}
		if err := WritePayload(w, t); err != nil {
			return err
		}
	}
	_, err := w.Write([]byte{0})
	return err
}

// Without returns a copy of a compound with the named fields removed, which is
// how a saved block entity becomes a wire one (x, y and z come from the packet).
func Without(comp map[string]Tag, drop ...string) map[string]Tag {
	out := make(map[string]Tag, len(comp))
	for k, v := range comp {
		out[k] = v
	}
	for _, k := range drop {
		delete(out, k)
	}
	return out
}

func writeString(w io.Writer, s string) error {
	if len(s) > 0xFFFF {
		return ErrUnsupported
	}
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], uint16(len(s)))
	if err := write(w, b[:]); err != nil {
		return err
	}
	return write(w, []byte(s))
}

func writeLen(w io.Writer, n int) error {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(int32(n)))
	return write(w, b[:])
}

func write(w io.Writer, b []byte) error {
	_, err := w.Write(b)
	return err
}
