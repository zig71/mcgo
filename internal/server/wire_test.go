package server

import (
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// The tests here walk a packet's body field by field, in the order the
// decompiled packet declares them, and insist that the body ends exactly where
// the last field does. A missing field is the failure that matters: it is what
// took the connection down three times (level event, system chat, explode), and
// it cannot be caught by a test whose expectation was written from the same
// reading that produced the encoder.

// waitPacket waits for one packet by id.
func waitPacket(t *testing.T, got chan mcproto.Packet, id int32) mcproto.Packet {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case p := <-got:
			if p.ID == id {
				return p
			}
		case <-deadline:
			t.Fatalf("packet %d never arrived", id)
		}
	}
}

// Block Update: the position, then the block state.
func TestBlockUpdateWire(t *testing.T) {
	srv := fluidServer()
	s, got, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	state, ok := srv.blockState(v777.Tables, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	s.setBlockUpdate(3, 64, -5, state)

	p := waitPacket(t, got, s.t.PlayClientboundBlockUpdate)
	r := mcproto.NewReader(p.Data)
	pos, err := r.ReadLong()
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if pos != packPosition(3, 64, -5) {
		t.Errorf("position is %d, want the packed 3,64,-5", pos)
	}
	gotState, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if gotState != state {
		t.Errorf("state is %d, want %d", gotState, state)
	}
	if r.Remaining() != 0 {
		t.Errorf("%d trailing bytes after the block state", r.Remaining())
	}
}

// Container Set Content: the window, the state, the slots, then the item on the
// cursor, which is the field a body like this most easily loses.
func TestContainerSetContentWire(t *testing.T) {
	srv := fluidServer()
	s, got, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)

	if err := s.sendInventory(); err != nil {
		t.Fatalf("sending the inventory: %v", err)
	}

	p := waitPacket(t, got, s.t.PlayClientboundContainerSetContent)
	r := mcproto.NewReader(p.Data)
	window, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("window: %v", err)
	}
	if window != 0 {
		t.Errorf("window is %d, want the player's own 0", window)
	}
	if _, err := r.ReadVarInt(); err != nil { // state id
		t.Fatalf("state: %v", err)
	}
	count, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("slot count: %v", err)
	}
	if count != 46 {
		t.Errorf("the player inventory sent %d slots, want 46", count)
	}
	// Every slot is empty and so is the carried item, so the rest is one zero
	// byte each — and the carried item is the byte a body like this is most
	// likely to have lost.
	if r.Remaining() != int(count)+1 {
		t.Fatalf("%d bytes after the slot count, want %d (46 slots and the carried item)", r.Remaining(), count+1)
	}
	for i := 0; i < int(count)+1; i++ {
		if b, err := r.ReadByte(); err != nil || b != 0 {
			t.Fatalf("byte %d of the slots is %d (err %v), want an empty slot", i, b, err)
		}
	}
}

// Respawn: the whole spawn info block, then what is carried over.
func TestRespawnWire(t *testing.T) {
	srv := fluidServer()
	s, got, _ := pipedSession(t, srv)
	sessionRegisterForTest(srv, s)
	s.spawnX, s.spawnY, s.spawnZ = 0.5, 65, 0.5
	s.dead = true

	if err := s.respawn(); err != nil {
		t.Fatalf("respawning: %v", err)
	}

	p := waitPacket(t, got, s.t.PlayClientboundRespawn)
	r := mcproto.NewReader(p.Data)
	if _, err := r.ReadVarInt(); err != nil { // dimension type
		t.Fatalf("dimension type: %v", err)
	}
	if _, err := r.ReadString(); err != nil { // dimension name
		t.Fatalf("dimension name: %v", err)
	}
	if _, err := r.ReadLong(); err != nil { // hashed seed
		t.Fatalf("seed: %v", err)
	}
	if _, err := r.ReadVarInt(); err != nil { // game mode
		t.Fatalf("game mode: %v", err)
	}
	if _, err := r.ReadVarInt(); err != nil { // previous game mode
		t.Fatalf("previous game mode: %v", err)
	}
	for _, name := range []string{"is debug", "is flat", "has death location"} {
		if _, err := r.ReadByte(); err != nil { // a boolean is one byte
			t.Fatalf("%s: %v", name, err)
		}
	}
	if _, err := r.ReadVarInt(); err != nil { // portal cooldown
		t.Fatalf("portal cooldown: %v", err)
	}
	if _, err := r.ReadVarInt(); err != nil { // sea level
		t.Fatalf("sea level: %v", err)
	}
	if _, err := r.ReadByte(); err != nil { // what is carried over
		t.Fatalf("the carried over flags: %v", err)
	}
	if r.Remaining() != 0 {
		t.Errorf("%d trailing bytes after the respawn info", r.Remaining())
	}
}
