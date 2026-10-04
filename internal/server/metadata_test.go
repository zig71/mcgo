package server

import (
	"bytes"
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// The metadata body is a run of entries ended by 255. An entry is a byte
// index, the value's serializer, then the value; getting the terminator or the
// serializer wrong leaves the client reading the next packet as metadata.
func TestMetadataBodyLayout(t *testing.T) {
	got := metadataBody(2, flagSneaking, 0x7f, false)

	want := []byte{
		0x02, // entity id
		0x00, // index 0: the shared flags
		0x00, // serializer 0: BYTE
		0x02, // sneaking
		0xff, // end of the list
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("metadataBody\n got %x\nwant %x", got, want)
	}
}

// Skin layers ride in the same packet once the client has told us about them.
func TestMetadataBodyWithSkinParts(t *testing.T) {
	got := metadataBody(2, 0, 0x7f, true)

	want := []byte{
		0x02,             // entity id
		0x00, 0x00, 0x00, // shared flags, empty
		byte(metaSkinParts), 0x00, 0x7f, // skin layers
		0xff,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("metadataBody\n got %x\nwant %x", got, want)
	}
	if metaSkinParts != 16 {
		t.Errorf("skin parts index = %d, want 16 (8 Entity + 7 LivingEntity + 1)", metaSkinParts)
	}
}

func TestMetadataFlags(t *testing.T) {
	s := &session{t: v777.Tables, srv: &Server{}}

	if got := s.flags(); got != 0 {
		t.Errorf("a standing still player has flags %d, want 0", got)
	}
	s.setPose(true, false)
	if got := s.flags(); got != flagSneaking {
		t.Errorf("sneaking flags = %d, want %d", got, flagSneaking)
	}
	s.setPose(true, true)
	if got := s.flags(); got != flagSneaking|flagSprinting {
		t.Errorf("sneaking and sprinting flags = %d", got)
	}
	s.setPose(false, false)
	if got := s.flags(); got != 0 {
		t.Errorf("flags after standing up = %d", got)
	}
}

// The input bitfield is the only place sneaking travels: it is not a movement
// packet and not a player command.
func TestPlayerInputSetsPose(t *testing.T) {
	s := &session{t: v777.Tables, srv: &Server{}}

	s.handlePlayerInput(mcproto.NewReader([]byte{inputFlagShift}))
	if !s.sneaking || s.sprinting {
		t.Errorf("shift input gave sneaking=%v sprinting=%v", s.sneaking, s.sprinting)
	}
	s.handlePlayerInput(mcproto.NewReader([]byte{inputFlagSprint}))
	if s.sneaking || !s.sprinting {
		t.Errorf("sprint input gave sneaking=%v sprinting=%v", s.sneaking, s.sprinting)
	}
	s.handlePlayerInput(mcproto.NewReader([]byte{0}))
	if s.sneaking || s.sprinting {
		t.Error("releasing both keys left a flag set")
	}
	// A truncated packet must not panic.
	s.handlePlayerInput(mcproto.NewReader(nil))
}

// Sprinting also arrives as a player command, whose actions are enumerated in
// the game's own order.
func TestPlayerCommandSetsSprinting(t *testing.T) {
	s := &session{t: v777.Tables, srv: &Server{}}

	var w mcproto.Writer
	w.WriteVarInt(1) // entity id
	w.WriteVarInt(actionStartSprinting)
	s.handlePlayerCommand(mcproto.NewReader(w.Bytes()))
	if !s.sprinting {
		t.Error("START_SPRINTING did not set the sprinting flag")
	}

	w.Reset()
	w.WriteVarInt(1)
	w.WriteVarInt(actionStopSprinting)
	s.handlePlayerCommand(mcproto.NewReader(w.Bytes()))
	if s.sprinting {
		t.Error("STOP_SPRINTING did not clear the sprinting flag")
	}

	// The other actions are not about appearance and must change nothing.
	for _, action := range []int32{actionStopSleeping, actionOpenInventory, actionStartFallFlying} {
		s.setPose(true, true)
		w.Reset()
		w.WriteVarInt(1)
		w.WriteVarInt(action)
		s.handlePlayerCommand(mcproto.NewReader(w.Bytes()))
		if !s.sneaking || !s.sprinting {
			t.Errorf("action %d changed the pose", action)
		}
	}
	// A truncated packet must not panic.
	s.handlePlayerCommand(mcproto.NewReader(nil))
}
