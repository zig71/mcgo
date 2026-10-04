package server

import (
	"testing"

	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// Picking a block puts its item in the hand: into a slot that already has one,
// or into the hand when there is none.
func TestPickItemIntoHand(t *testing.T) {
	s := placer(0, 0)
	stone := itemOf(t, "minecraft:stone")

	if !s.pickItem(stone) {
		t.Fatal("picking an item changed nothing")
	}
	if got := s.inv[27]; got.ID != stone || got.Count != 1 {
		t.Fatalf("hand holds %+v, want one stone", got)
	}

	// Picking it again changes nothing: the hand already has it.
	if s.pickItem(stone) {
		t.Error("picking an item already in the hand reported a change")
	}
}

// An item already on the hotbar is selected rather than duplicated.
func TestPickItemSelectsAHotbarSlot(t *testing.T) {
	s := placer(0, 0)
	stone := itemOf(t, "minecraft:stone")
	s.heldSlot = 0
	s.inv[28] = world.Item{ID: stone, Count: 3}

	if !s.pickItem(stone) {
		t.Fatal("picking did not select the hotbar slot holding the item")
	}
	if s.heldSlot != 1 {
		t.Errorf("selected slot = %d, want 1", s.heldSlot)
	}
	if s.inv[28].Count != 3 {
		t.Errorf("the existing stack changed: %+v", s.inv[28])
	}
}

// An item held elsewhere in the inventory is swapped into the hand.
func TestPickItemSwapsFromTheInventory(t *testing.T) {
	s := placer(0, 0)
	stone := itemOf(t, "minecraft:stone")
	s.heldSlot = 0
	s.inv[0] = world.Item{ID: stone, Count: 7}
	s.inv[27] = world.Item{ID: itemOf(t, "minecraft:dirt"), Count: 1}

	if !s.pickItem(stone) {
		t.Fatal("picking did not swap the item into the hand")
	}
	if got := s.inv[27]; got.ID != stone || got.Count != 7 {
		t.Errorf("hand holds %+v, want the stone stack", got)
	}
	if got := s.inv[0]; got.ID != itemOf(t, "minecraft:dirt") {
		t.Errorf("the dirt did not move to the old slot: %+v", got)
	}
}

// The position form turns a block into the item that places it.
func TestPickItemFromAPosition(t *testing.T) {
	s := placer(0, 0)
	stone := itemOf(t, "minecraft:stone")
	state, ok := s.srv.blockState(v777.Tables, "minecraft:stone", nil)
	if !ok {
		t.Fatal("stone has no default state")
	}
	s.world.SetBlock(0, 65, 0, state)

	if !s.pickItemPos(0, 65, 0) {
		t.Fatal("picking a stone block did nothing")
	}
	if got := s.inv[27]; got.ID != stone {
		t.Errorf("hand holds item %d, want stone (%d)", got.ID, stone)
	}

	// Air is not pickable.
	if s.pickItemPos(0, 70, 0) {
		t.Error("picking air changed something")
	}
}
