package server

import (
	"bytes"
	"testing"

	"mcgo/internal/proto"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// A player's held item is sent as Set Equipment: the entity, then slot bytes
// where the high bit means "another entry follows". A single entry must have
// that bit clear, or the client reads past the packet.
func TestSetEquipmentBody(t *testing.T) {
	s := &session{t: v777.Tables}
	got := s.setEquipmentBody(2, slotMainHand, world.Item{ID: 5, Count: 1})

	want := []byte{
		0x02,       // entity id
		0x00,       // main hand, no continuation bit
		0x01, 0x05, // item count, item id
		0x00, 0x00, // no components added, none removed
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("setEquipmentBody\n got %x\nwant %x", got, want)
	}
}

// A named item keeps its custom name in the hand, so the component rides along.
func TestSetEquipmentCarriesTheName(t *testing.T) {
	s := &session{t: v777.Tables}
	it := world.Item{ID: 5, Count: 1}
	setItemName(v777.Tables, &it, "hi")
	got := s.setEquipmentBody(1, slotMainHand, it)

	if got[2] != 0x01 || got[3] != 0x05 {
		t.Fatalf("item not where expected: %x", got)
	}
	if !bytes.Contains(got, []byte("text")) {
		t.Errorf("the custom name is missing: %x", got)
	}
}

// 26.3 spells a swing as Swing Animation: entity, hand, then the animation's
// type and duration.
func TestSwingBody(t *testing.T) {
	want := []byte{
		0x02, // entity id
		0x00, // main hand
		0x01, // whack
		0x06, // six ticks
	}
	if got := swingBody(2); !bytes.Equal(got, want) {
		t.Fatalf("swingBody\n got %x\nwant %x", got, want)
	}
}

// Older versions have no Swing Animation packet and use Animate with action 0.
func TestAnimateBody(t *testing.T) {
	want := []byte{0x02, 0x00}
	if got := animateBody(2); !bytes.Equal(got, want) {
		t.Fatalf("animateBody\n got %x\nwant %x", got, want)
	}
}

// The version decides which form a swing takes: 26.3 has the new packet, 26.2
// and 1.21.8 fall back to Animate, and a version with neither stays silent.
func TestSwingFormFollowsTheVersion(t *testing.T) {
	id, body, ok := swingPacket(v777.Tables, 2)
	if !ok || id != v777.Tables.PlayClientboundSwingAnimation {
		t.Fatalf("26.3 chose packet %d (ok=%v), want the swing animation packet", id, ok)
	}
	if !bytes.Equal(body, swingBody(2)) {
		t.Errorf("26.3 sent %x", body)
	}

	older := proto.Tables{PlayClientboundAnimate: 2}
	id, body, ok = swingPacket(older, 2)
	if !ok || id != 2 || !bytes.Equal(body, animateBody(2)) {
		t.Errorf("an older version chose packet %d body %x", id, body)
	}

	if _, _, ok := swingPacket(proto.Tables{}, 2); ok {
		t.Error("a version with no swing packet still claimed to send one")
	}
}
