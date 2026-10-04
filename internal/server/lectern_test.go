package server

import (
	"bytes"
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/nbt"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// bookComponent is a written book's content component as the wire carries it: a
// compound with a pages list, framed as a type byte and a payload.
func bookComponent(t *testing.T, pages int) []byte {
	t.Helper()
	list := nbt.Tag{Type: nbt.List}
	for range pages {
		list.List = append(list.List, nbt.Tag{Type: nbt.String, Str: "text"})
	}
	comp := nbt.Tag{Type: nbt.Compound, Comp: map[string]nbt.Tag{
		"pages": list,
		"title": {Type: nbt.String, Str: "A Book"},
	}}
	var buf bytes.Buffer
	if err := nbt.WriteUnnamed(&buf, comp); err != nil {
		t.Fatalf("building the component: %v", err)
	}
	return buf.Bytes()
}

// lecternButton is the packet a lectern screen's buttons send.
func lecternButton(button byte) *mcproto.Reader {
	var w mcproto.Writer
	w.WriteVarInt(containerWindowID)
	w.WriteByte(button)
	return mcproto.NewReader(w.Bytes())
}

// TestLecternTakesShowsAndReturnsABook walks a lectern through its whole use: a
// book in hand goes onto it (one off the stack, and the block starts showing
// it), using it again opens the book at the page property, the page buttons
// turn and clamp at the last page, and the take button puts the book back in
// the player's inventory and clears the lectern.
func TestLecternTakesShowsAndReturnsABook(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	tab := v777.Tables
	if s.t.Protocol != tab.Protocol {
		t.Fatalf("session tables are protocol %d", s.t.Protocol)
	}
	lectern, ok := srv.blockState(tab, "minecraft:lectern", nil)
	if !ok {
		t.Fatal("no lectern block state")
	}
	s.world.SetBlock(0, 65, 0, lectern)

	bookID, ok := tab.ItemIDs["minecraft:written_book"]
	if !ok {
		t.Fatal("no written book item")
	}
	s.inv[27] = world.Item{ID: bookID, Count: 2, Components: []world.Component{
		{Type: tab.BannerPatternsComponent + 1000, Value: []byte{0x00}}, // not the pages
		{Type: tab.BannerPatternsComponent + 1001, Value: bookComponent(t, 3)},
	}}
	s.heldSlot = 0
	// Survival, because creative keeps the book: vanilla's consumeAndReturn
	// does not take it from a player with infinite materials.
	s.gameMode = gameModeSurvival

	if !s.useLectern(0, 65, 0) {
		t.Fatal("the lectern did not take the book from the hand")
	}
	held := s.srv.containers.Get(0, 65, 0, 1)
	if held[0].ID != bookID || held[0].Count != 1 {
		t.Fatalf("the lectern holds %+v, want one written book", held[0])
	}
	if s.inv[27].Count != 1 {
		t.Errorf("the hand holds %d books, want 1 left of 2", s.inv[27].Count)
	}
	if name, props, ok := srv.blockStateName(tab, s.world.Block(0, 65, 0)); !ok || props["has_book"] != "true" {
		t.Errorf("lectern state is %q %v, want has_book=true", name, props)
	}

	// Using it again opens the book: the page property starts at zero and the
	// page count comes from the book's component.
	if !s.useLectern(0, 65, 0) {
		t.Fatal("the lectern with a book did not open")
	}
	if !s.containerOpen || s.containerTitle != lecternTitle {
		t.Fatalf("open window is %q (open=%v), want the lectern", s.containerTitle, s.containerOpen)
	}
	if s.lecternPages != 3 {
		t.Errorf("page count is %d, want 3", s.lecternPages)
	}

	// The next-page button turns once, and then stops at the last page.
	s.handleContainerButton(lecternButton(2))
	if s.lecternPage != 1 {
		t.Errorf("page after one turn is %d, want 1", s.lecternPage)
	}
	for range 5 {
		s.handleContainerButton(lecternButton(2))
	}
	if s.lecternPage != 2 {
		t.Errorf("page ran to %d, want it clamped at 2", s.lecternPage)
	}
	s.handleContainerButton(lecternButton(1))
	if s.lecternPage != 1 {
		t.Errorf("page after turning back is %d, want 1", s.lecternPage)
	}

	// Taking the book clears the lectern and fills the emptied slot.
	s.handleContainerButton(lecternButton(3))
	if got := s.srv.containers.Get(0, 65, 0, 1)[0]; !got.Empty() {
		t.Errorf("the lectern still holds %+v after the book was taken", got)
	}
	if s.inv[27].ID != bookID || s.inv[27].Count != 2 {
		t.Errorf("the hand holds %+v, want the two books back", s.inv[27])
	}
	if name, props, ok := srv.blockStateName(tab, s.world.Block(0, 65, 0)); !ok || props["has_book"] != "false" {
		t.Errorf("lectern state is %q %v, want has_book=false", name, props)
	}
}

// A lectern with nothing in it and nothing book-shaped in hand is not this
// server's business: the use has to fall through to placing a block.
func TestLecternPassesOnOtherUses(t *testing.T) {
	srv := fluidServer()
	s, _, _ := pipedSession(t, srv)
	tab := v777.Tables
	lectern, _ := srv.blockState(tab, "minecraft:lectern", nil)
	s.world.SetBlock(0, 65, 0, lectern)
	s.inv[27] = world.Item{ID: tab.ItemIDs["minecraft:stone"], Count: 1}
	s.heldSlot = 0
	if s.useLectern(0, 65, 0) {
		t.Error("an empty lectern took a stone")
	}
	if !s.srv.containers.Get(0, 65, 0, 1)[0].Empty() {
		t.Error("the stone ended up on the lectern")
	}
}
