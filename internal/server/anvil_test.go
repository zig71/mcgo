package server

import (
	"bytes"
	"testing"

	"mcgo/internal/mcproto"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

func anvilWindow(left, addition string) ([]world.Item, *session) {
	tab := v777.Tables
	s := &session{t: tab, containerSize: 3, containerTitle: "container.repair"}
	items := make([]world.Item, 3)
	if left != "" {
		items[anvilInputSlot] = world.Item{ID: itemID(tab, left), Count: 1}
	}
	if addition != "" {
		items[anvilAddSlot] = world.Item{ID: itemID(tab, addition), Count: 1}
	}
	return items, s
}

func TestAnvilRenames(t *testing.T) {
	items, s := anvilWindow("minecraft:diamond_sword", "")
	s.anvilName = "Excalibur"

	result, cost := anvilResult(s.t, items[anvilInputSlot], items[anvilAddSlot], s.anvilName)

	if result.ID != items[anvilInputSlot].ID || itemName(s.t, result) != "Excalibur" {
		t.Fatalf("anvil produced %+v, want a renamed sword", result)
	}
	if cost != 1 {
		t.Errorf("rename cost = %d, want 1", cost)
	}
}

func TestAnvilClearsAName(t *testing.T) {
	items, _ := anvilWindow("minecraft:diamond_sword", "")
	setItemName(v777.Tables, &items[anvilInputSlot], "Excalibur")

	result, cost := anvilResult(v777.Tables, items[anvilInputSlot], items[anvilAddSlot], "")

	if result.Empty() {
		t.Fatal("clearing a name produced no result")
	}
	if got := itemName(v777.Tables, result); got != "" {
		t.Errorf("name was not cleared: %q", got)
	}
	if cost != 1 {
		t.Errorf("clearing cost = %d, want 1", cost)
	}
}

// Two undamaged items have nothing to combine, and vanilla agrees: it produces
// no result for a pair with no damage and no enchantments.
func TestAnvilWillNotCombine(t *testing.T) {
	items, _ := anvilWindow("minecraft:diamond_sword", "minecraft:diamond_sword")

	if result, cost := anvilResult(v777.Tables, items[anvilInputSlot], items[anvilAddSlot], ""); !result.Empty() || cost != 0 {
		t.Fatalf("two items produced %+v (cost %d)", result, cost)
	}
}

func TestAnvilRejectsBadNames(t *testing.T) {
	items, _ := anvilWindow("minecraft:diamond_sword", "")
	long := make([]byte, anvilMaxNameLength+1)
	for i := range long {
		long[i] = 'a'
	}

	if result, _ := anvilResult(v777.Tables, items[anvilInputSlot], items[anvilAddSlot], string(long)); !result.Empty() {
		t.Errorf("a %d-character name was accepted", len(long))
	}
	if got := filterAnvilName("bad\u00a7lname"); got != "badlname" {
		t.Errorf("section sign not stripped: %q", got)
	}
}

// Taking the result uses up the item on the anvil and clears the cost.
func TestAnvilTakeConsumesInput(t *testing.T) {
	items, s := anvilWindow("minecraft:diamond_sword", "")
	s.anvilName = "Excalibur"
	s.refreshAnvilResult(items)
	w := s.window(items)

	carried := world.Item{}
	if !s.anvilClick(items, w, 0, &carried) {
		t.Fatal("taking the anvil result did nothing")
	}
	if itemName(v777.Tables, carried) != "Excalibur" {
		t.Fatalf("cursor holds %+v, want the renamed sword", carried)
	}
	if !items[anvilInputSlot].Empty() {
		t.Errorf("the input survived the take: %+v", items[anvilInputSlot])
	}
	if result, _ := anvilResult(v777.Tables, items[anvilInputSlot], items[anvilAddSlot], s.anvilName); !result.Empty() {
		t.Errorf("the result survived the take: %+v", result)
	}
}

// A named slot must carry the custom_name component, with the name as a
// network-NBT text component; the client draws the name from it.
func TestNamedSlotOnTheWire(t *testing.T) {
	s := &session{t: v777.Tables}
	it := world.Item{ID: 5, Count: 1}
	setItemName(v777.Tables, &it, "hi")
	var w mcproto.Writer
	s.writeSlot(&w, it)

	component := append([]byte{0x0A, 0x08, 0x00, 0x04}, "text"...)
	component = append(component, 0x00, 0x02, 'h', 'i', 0x00)
	// The patch leads with both counts: one component set, none removed.
	want := []byte{0x01, 0x05, 0x01, 0x00, byte(v777.Tables.CustomNameComponent)}
	want = append(want, component...)

	if got := w.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("named slot\n got %x\nwant %x", got, want)
	}
}

// A plain slot stays three VarInts and a trailing zero for removals.
func TestPlainSlotOnTheWire(t *testing.T) {
	s := &session{t: v777.Tables}
	var w mcproto.Writer
	s.writeSlot(&w, world.Item{ID: 5, Count: 3})

	if got, want := w.Bytes(), []byte{0x03, 0x05, 0x00, 0x00}; !bytes.Equal(got, want) {
		t.Fatalf("plain slot = %x, want %x", got, want)
	}
}
