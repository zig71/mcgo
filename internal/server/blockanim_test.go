package server

import (
	"testing"
	"time"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
)

// firstPacketWith drains the sent-packet channel until the given id arrives and
// returns its body.
func firstPacketWith(t *testing.T, got chan mcproto.Packet, id int32) []byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case p := <-got:
			if p.ID == id {
				return p.Data
			}
		default:
			time.Sleep(time.Millisecond)
		}
	}
	t.Fatalf("packet %d never arrived", id)
	return nil
}

// A chest tells the client to swing its lid with a block event: the position,
// b0 = 1, b1 = the number of players who have it open, and the block's registry
// id. The client animates from this packet; the creak is a separate sound.
func TestChestLidBlockEvent(t *testing.T) {
	srv := fluidServer()
	s, got, _ := pipedSession(t, srv)
	srv.register(s)
	tab := v777.Tables
	chest, ok := srv.blockState(tab, "minecraft:chest", nil)
	if !ok {
		t.Fatal("no chest block state")
	}
	s.world.SetBlock(4, 65, 4, chest)
	if !s.openContainer(4, 65, 4) {
		t.Fatal("the chest did not open")
	}
	r := mcproto.NewReader(firstPacketWith(t, got, tab.PlayClientboundBlockEvent))
	pos, err := r.ReadLong()
	if err != nil {
		t.Fatalf("position: %v", err)
	}
	if pos != packPosition(4, 65, 4) {
		t.Errorf("the block event is for %d, want the chest at (4,65,4)", pos)
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
		t.Errorf("the block event is (%d,%d), want (1,1): 1 marks a lid, then the open count", b0, b1)
	}
	id, err := r.ReadVarInt()
	if err != nil {
		t.Fatalf("block id: %v", err)
	}
	if id != tab.BlockIDs["minecraft:chest"] {
		t.Errorf("the block id is %d, want the chest's %d", id, tab.BlockIDs["minecraft:chest"])
	}
	if r.Remaining() != 0 {
		t.Errorf("%d trailing bytes in the block event", r.Remaining())
	}
}

// A barrel has no block event at all: its OPEN property is what animates the
// lid, and it goes back down when the last player closes it. The count is why
// two players at one chest do not see it shut when the first walks away.
func TestBarrelLidIsABlockState(t *testing.T) {
	srv := fluidServer()
	tab := v777.Tables
	w := srv.worldFor(tab)
	barrel, ok := srv.blockState(tab, "minecraft:barrel", nil)
	if !ok {
		t.Fatal("no barrel block state")
	}
	w.SetBlock(4, 65, 4, barrel)
	srv.containerOpened(tab, w, 4, 65, 4, true)
	if name, props, _ := srv.blockStateName(tab, w.Block(4, 65, 4)); name != "minecraft:barrel" || props["open"] != "true" {
		t.Errorf("the barrel is %q %v after opening, want open=true", name, props)
	}
	// A second opener, then one closer: still open.
	srv.containerOpened(tab, w, 4, 65, 4, true)
	srv.containerOpened(tab, w, 4, 65, 4, false)
	if _, props, _ := srv.blockStateName(tab, w.Block(4, 65, 4)); props["open"] != "true" {
		t.Errorf("the barrel shut with a player still in it: open=%q", props["open"])
	}
	srv.containerOpened(tab, w, 4, 65, 4, false)
	if _, props, _ := srv.blockStateName(tab, w.Block(4, 65, 4)); props["open"] != "false" {
		t.Errorf("the barrel is open=%q after the last player left, want false", props["open"])
	}
}
