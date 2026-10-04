package server

import (
	"testing"

	"mcgo/internal/world"
)

// windowFor builds a window: container slots first, then the player's.
// sameItem compares two slots including their components, which == cannot do
// once an item carries a component slice.
func sameItem(a, b world.Item) bool {
	return a.ID == b.ID && a.Count == b.Count && a.Same(b)
}

// testContainerSlots is the container half of the window these tests build.
const testContainerSlots = 27

// windowFor builds a window: container slots first, then the player's.
func windowFor(container, player []world.Item) []world.Item {
	w := make([]world.Item, 0, testContainerSlots+36)
	containerPad := make([]world.Item, testContainerSlots)
	copy(containerPad, container)
	playerPad := make([]world.Item, 36)
	copy(playerPad, player)
	return append(append(w, containerPad...), playerPad...)
}

// TestApplyClickQuickMove checks shift-click moves a stack between the halves,
// topping up a partial stack before taking an empty slot — the behaviour a
// player notices immediately if it is wrong.
func TestApplyClickQuickMove(t *testing.T) {
	w := windowFor(
		[]world.Item{{ID: 5, Count: 64}},
		[]world.Item{{ID: 5, Count: 32}},
	)
	carried := applyClick(w, world.Item{}, 0, 1, 0, testContainerSlots)
	if !carried.Empty() {
		t.Errorf("shift-click left %+v on the cursor", carried)
	}
	if w[0].Count != 0 {
		t.Errorf("container slot still holds %d", w[0].Count)
	}
	// The partial stack is topped up to 64, the rest lands in a free slot.
	if w[testContainerSlots].Count != 64 {
		t.Errorf("partial stack is %d, want 64", w[testContainerSlots].Count)
	}
	if w[testContainerSlots+1].Count != 32 {
		t.Errorf("overflow is %d, want 32", w[testContainerSlots+1].Count)
	}

	// The other direction: a player slot sends its stack into the container.
	w2 := windowFor([]world.Item{}, []world.Item{{ID: 9, Count: 10}})
	applyClick(w2, world.Item{}, testContainerSlots, 1, 0, testContainerSlots)
	if w2[0].Count != 10 || !w2[testContainerSlots].Empty() {
		t.Errorf("shift-click into the container did not move the stack: %+v", w2[:2])
	}
}

// TestApplyClickSwap checks the hotkey swap, and that the offhand button — a
// slot this server does not model — changes nothing.
func TestApplyClickSwap(t *testing.T) {
	w := windowFor(
		[]world.Item{{ID: 3, Count: 7}},
		[]world.Item{{ID: 8, Count: 12}},
	)
	applyClick(w, world.Item{}, 0, 2, 0, testContainerSlots) // hotbar key 1
	if w[0].ID != 8 || w[testContainerSlots].ID != 3 {
		t.Errorf("swap did not exchange the stacks: %+v / %+v", w[0], w[testContainerSlots])
	}

	w2 := windowFor([]world.Item{{ID: 3, Count: 7}}, []world.Item{{ID: 8, Count: 12}})
	applyClick(w2, world.Item{}, 0, 2, 40, testContainerSlots) // offhand
	if w2[0].ID != 3 || w2[testContainerSlots].ID != 8 {
		t.Error("the offhand button changed the window")
	}
}

// TestApplyClickClone checks creative middle-click copies a full stack without
// consuming the slot.
func TestApplyClickClone(t *testing.T) {
	w := windowFor([]world.Item{{ID: 4, Count: 3}}, nil)
	carried := applyClick(w, world.Item{}, 0, 3, 0, testContainerSlots)
	if carried.ID != 4 || carried.Count != 64 {
		t.Errorf("clone produced %+v, want 64 of id 4", carried)
	}
	if w[0].Count != 3 {
		t.Errorf("clone consumed the slot: %+v", w[0])
	}
}

// TestApplyClickPickupAll checks double-click gathers matching stacks onto the
// cursor and stops at a full stack.
func TestApplyClickPickupAll(t *testing.T) {
	w := windowFor(
		[]world.Item{{ID: 2, Count: 20}, {ID: 7, Count: 64}},
		[]world.Item{{ID: 2, Count: 30}},
	)
	carried := applyClick(w, world.Item{ID: 2, Count: 10}, 1, 6, 0, testContainerSlots)
	if carried.ID != 2 || carried.Count != 60 {
		t.Errorf("gathered %+v, want 60 of id 2", carried)
	}
	if w[0].Count != 0 {
		t.Errorf("a gathered slot still holds %d", w[0].Count)
	}
	if w[testContainerSlots].Count != 0 {
		t.Errorf("the player's stack was not gathered: %d", w[testContainerSlots].Count)
	}
	if w[1].Count != 64 {
		t.Error("pickup-all took from a different item")
	}
}

// TestApplyClickUnimplementedModes checks the modes this server does not
// implement leave the window exactly as it was: a wrong guess would destroy or
// duplicate items, which is far worse than the client needing to reopen.
func TestApplyClickUnimplementedModes(t *testing.T) {
	for _, mode := range []int32{4, 5, 99} {
		w := windowFor([]world.Item{{ID: 1, Count: 5}}, []world.Item{{ID: 2, Count: 5}})
		before := append([]world.Item(nil), w...)
		carried := applyClick(w, world.Item{ID: 3, Count: 1}, 0, mode, 0, testContainerSlots)
		for i := range w {
			if !sameItem(w[i], before[i]) {
				t.Errorf("mode %d changed slot %d", mode, i)
			}
		}
		if carried.ID != 3 || carried.Count != 1 {
			t.Errorf("mode %d changed the cursor to %+v", mode, carried)
		}
	}

	// A click outside the window keeps the stack rather than destroying it.
	w := windowFor(nil, nil)
	carried := applyClick(w, world.Item{ID: 6, Count: 9}, -999, 0, 0, testContainerSlots)
	if carried.ID != 6 || carried.Count != 9 {
		t.Errorf("a click outside the window dropped the cursor: %+v", carried)
	}
}
