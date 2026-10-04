package mcproto

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"unicode/utf8"
)

var (
	errInvalidString  = errors.New("mcproto: invalid UTF-8 string")
	errPacketTooLarge = errors.New("mcproto: packet length out of range")
)

// Reader reads Minecraft protocol primitives from an in-memory packet payload.
// ReadByte makes it satisfy io.ByteReader, so ReadVarInt works on it directly.
type Reader struct {
	buf []byte
	pos int
}

func NewReader(b []byte) *Reader { return &Reader{buf: b} }

func (r *Reader) ReadByte() (byte, error) {
	if r.pos >= len(r.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

// Remaining reports how many unread bytes are left.
func (r *Reader) Remaining() int { return len(r.buf) - r.pos }

func (r *Reader) ReadVarInt() (int32, error) { return ReadVarInt(r) }

// ReadVarLong reads a Minecraft VarLong. The writer had one and the reader did
// not, which is why a packet carrying a VarLong could be written and not
// parsed.
func (r *Reader) ReadVarLong() (int64, error) { return readVarLong(r) }

// readVarLong is the LEB128 loop for 64 bits: at most ten bytes, the last of
// which carries a single bit.
func readVarLong(r io.ByteReader) (int64, error) {
	var value uint64
	for i := range maxVarLongLen {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return int64(value), nil
		}
	}
	return 0, ErrVarLongTooBig
}

// ReadString reads a VarInt-length-prefixed UTF-8 string.
// ReadBytes reads exactly n raw bytes.
func (r *Reader) ReadBytes(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.buf) {
		return nil, io.ErrUnexpectedEOF
	}
	out := make([]byte, n)
	copy(out, r.buf[r.pos:r.pos+n])
	r.pos += n
	return out, nil
}

func (r *Reader) ReadString() (string, error) {
	n, err := r.ReadVarInt()
	if err != nil {
		return "", err
	}
	if n < 0 || int(n) > r.Remaining() {
		return "", io.ErrUnexpectedEOF
	}
	s := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	if !utf8.Valid(s) {
		return "", errInvalidString
	}
	return string(s), nil
}

func (r *Reader) ReadUShort() (uint16, error) {
	if r.Remaining() < 2 {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.BigEndian.Uint16(r.buf[r.pos:])
	r.pos += 2
	return v, nil
}

func (r *Reader) ReadInt() (int32, error) {
	if r.Remaining() < 4 {
		return 0, io.ErrUnexpectedEOF
	}
	v := int32(binary.BigEndian.Uint32(r.buf[r.pos:]))
	r.pos += 4
	return v, nil
}

func (r *Reader) ReadDouble() (float64, error) {
	if r.Remaining() < 8 {
		return 0, io.ErrUnexpectedEOF
	}
	v := math.Float64frombits(binary.BigEndian.Uint64(r.buf[r.pos:]))
	r.pos += 8
	return v, nil
}

func (r *Reader) ReadFloat() (float32, error) {
	if r.Remaining() < 4 {
		return 0, io.ErrUnexpectedEOF
	}
	v := math.Float32frombits(binary.BigEndian.Uint32(r.buf[r.pos:]))
	r.pos += 4
	return v, nil
}

func (r *Reader) ReadLong() (int64, error) {
	if r.Remaining() < 8 {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.BigEndian.Uint64(r.buf[r.pos:])
	r.pos += 8
	return int64(v), nil
}

// Writer builds a packet payload. Embedding bytes.Buffer gives it Write and
// WriteByte, so WriteVarInt works on it directly.
type Writer struct{ bytes.Buffer }

func (w *Writer) WriteVarInt(v int32) error { return WriteVarInt(w, v) }

func (w *Writer) WriteString(s string) error {
	if err := w.WriteVarInt(int32(len(s))); err != nil {
		return err
	}
	_, err := w.Buffer.WriteString(s)
	return err
}

func (w *Writer) WriteShort(v int16) error {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], uint16(v))
	_, err := w.Buffer.Write(b[:])
	return err
}

func (w *Writer) WriteUShort(v uint16) error {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	_, err := w.Buffer.Write(b[:])
	return err
}

func (w *Writer) WriteLong(v int64) error {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	_, err := w.Buffer.Write(b[:])
	return err
}

// WriteVarLong writes v as a Minecraft VarLong: LEB128, at most 10 bytes.
func (w *Writer) WriteVarLong(v int64) error {
	u := uint64(v)
	for {
		if u&^0x7F == 0 {
			return w.WriteByte(byte(u))
		}
		if err := w.WriteByte(byte(u&0x7F | 0x80)); err != nil {
			return err
		}
		u >>= 7
	}
}

func (w *Writer) WriteInt(v int32) error {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	_, err := w.Buffer.Write(b[:])
	return err
}

func (w *Writer) WriteFloat(v float32) error {
	return w.WriteInt(int32(math.Float32bits(v)))
}

func (w *Writer) WriteDouble(v float64) error {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
	_, err := w.Buffer.Write(b[:])
	return err
}

func (w *Writer) WriteBool(v bool) error {
	if v {
		return w.WriteByte(1)
	}
	return w.WriteByte(0)
}

func (w *Writer) WriteBytes(b []byte) error {
	_, err := w.Buffer.Write(b)
	return err
}
