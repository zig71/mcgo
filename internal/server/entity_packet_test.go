package server

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// A real client rejected our Add Entity packet with "found 5 bytes extra": the
// velocity field was three zero shorts written after the rotations, where the
// protocol wants the low-precision Vec3 (a single zero byte) written before
// them. These tests pin the exact byte layout so a field cannot silently move
// or change size again.

func TestAddEntityBodyLayout(t *testing.T) {
	var uuid [16]byte
	for i := range uuid {
		uuid[i] = 0xAB
	}
	// Player entity type id 159: a two-byte VarInt.
	got := addEntityBody(2, uuid, 159, 1.5, 64, -3.5, 90, 0)

	want := []byte{0x02}                  // entity id
	want = append(want, uuid[:]...)       // uuid
	want = append(want, 0x9F, 0x01)       // entity type, VarInt
	want = append(want, f64(1.5)...)      // x
	want = append(want, f64(64)...)       // y
	want = append(want, f64(-3.5)...)     // z
	want = append(want, 0x00)             // movement: LpVec3 zero
	want = append(want, 0x00, 0x40, 0x40) // pitch, yaw, head yaw
	want = append(want, 0x00)             // object data

	if !bytes.Equal(got, want) {
		t.Fatalf("addEntityBody\n got %x\nwant %x", got, want)
	}
	if len(got) != 48 {
		t.Fatalf("Add Entity is %d bytes, want 48", len(got))
	}
}

func TestMoveEntityBodyLayout(t *testing.T) {
	got := moveEntityBody(2, 16, 0, -16, 90, 45)

	want := []byte{
		0x02,       // entity id
		0x01,       // properties: on ground, zero steps
		0x00, 0x10, // dx
		0x00, 0x00, // dy
		0xFF, 0xF0, // dz
		0x40, // yaw
		0x20, // pitch
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("moveEntityBody\n got %x\nwant %x", got, want)
	}
}

func f64(v float64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
	return b[:]
}

// TestDisguisedChatBodyLayout pins the chat-type reference: a holder writes
// registry index + 1, so minecraft:chat (index 0) travels as 1. Writing 0
// makes the client expect an inline chat type and it rejects the packet.
func TestDisguisedChatBodyLayout(t *testing.T) {
	component := func(s string) []byte {
		b := []byte{0x0A, 0x08, 0x00, 0x04}
		b = append(b, "text"...)
		b = append(b, byte(len(s)>>8), byte(len(s)))
		b = append(b, s...)
		return append(b, 0x00)
	}
	want := append(component("hi"), 0x01)
	want = append(want, component("Al")...)
	want = append(want, 0x00)

	if got := disguisedChatBody("hi", "Al"); !bytes.Equal(got, want) {
		t.Fatalf("disguisedChatBody\n got %x\nwant %x", got, want)
	}
}
