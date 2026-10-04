package server

import (
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// actionPacket builds a player-action packet: status, packed position, face,
// sequence.
func actionPacket(status, x, y, z int32, face byte, seq int32) *mcproto.Reader {
	var w mcproto.Writer
	w.WriteVarInt(status)
	w.WriteLong(packPosition(int(x), int(y), int(z)))
	w.WriteByte(face)
	w.WriteVarInt(seq)
	return mcproto.NewReader(w.Bytes())
}

// Punching a bell rings it. Vanilla's BellBlock answers a hit with a block event
// carrying the face that was struck, and the client swings the bell from that.
// This server broke the bell instead, so it could not be rung at all — and a
// block that cannot be rung is one of the animations nothing could trigger.
func TestPunchingABellRingsIt(t *testing.T) {
	srv := fluidServer()
	s, got, _ := pipedSession(t, srv)
	srv.register(s)
	tab := v777.Tables
	bell, ok := srv.blockState(tab, "minecraft:bell", nil)
	if !ok {
		t.Fatal("no bell block state")
	}
	s.world.SetBlock(4, 65, 4, bell)

	// Status 3 is "stopped digging", which is the one that breaks the block;
	// face 1 is the top of the block.
	s.handlePlayerAction(actionPacket(3, 4, 65, 4, 1, 7))

	body := firstPacketWith(t, got, tab.PlayClientboundBlockEvent)
	r := mcproto.NewReader(body)
	if _, err := r.ReadLong(); err != nil {
		t.Fatalf("position: %v", err)
	}
	b0, err := r.ReadByte()
	if err != nil {
		t.Fatalf("b0: %v", err)
	}
	b1, err := r.ReadByte()
	if err != nil {
		t.Fatalf("b1: %v", err)
	}
	if b0 != 1 || b1 != 1 {
		t.Errorf("the bell event is (%d,%d), want (1,1): 1 marks a bell, then the face struck", b0, b1)
	}
	if s.world.Block(4, 65, 4) != bell {
		t.Error("the bell was broken instead of rung")
	}
}
