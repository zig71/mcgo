package mcproto

import (
	"errors"
	"io"
)

const maxVarIntLen = 5

// ErrVarIntTooBig is returned when a VarInt exceeds the 5-byte protocol limit.
var ErrVarIntTooBig = errors.New("mcproto: VarInt is too big")

// maxVarLongLen is the protocol's limit for a VarLong, and ErrVarLongTooBig is
// what exceeding it returns.
const maxVarLongLen = 10

var ErrVarLongTooBig = errors.New("mcproto: VarLong is too big")

// ReadVarInt reads a Minecraft VarInt from r.
func ReadVarInt(r io.ByteReader) (int32, error) {
	var result uint32
	for i := range maxVarIntLen {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		result |= uint32(b&0x7F) << (7 * i)
		if b&0x80 == 0 {
			return int32(result), nil
		}
	}
	return 0, ErrVarIntTooBig
}

// WriteVarInt writes v as a Minecraft VarInt to w.
func WriteVarInt(w io.ByteWriter, v int32) error {
	u := uint32(v)
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
