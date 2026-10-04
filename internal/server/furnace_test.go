package server

import (
	"testing"

	"mcgo/internal/proto"
	"mcgo/internal/proto/v777"
	"mcgo/internal/world"
)

// itemID resolves an item name to its registry ID through the generated table,
// so the tests do not hardcode version-specific numbers. It panics on an
// unknown name: an unresolvable name would make a test compare 0 with 0 and
// pass while proving nothing.
func itemID(t proto.Tables, name string) int32 {
	id, ok := t.ItemIDs[name]
	if !ok || id == 0 {
		panic("unknown item " + name)
	}
	return id
}

// furnaceWindow builds the three-slot inventory a furnace menu has.
func furnaceWindow(t proto.Tables, input, fuel, output string) []world.Item {
	items := make([]world.Item, 3)
	if input != "" {
		items[0] = world.Item{ID: itemID(t, input), Count: 1}
	}
	if fuel != "" {
		items[1] = world.Item{ID: itemID(t, fuel), Count: 1}
	}
	if output != "" {
		items[2] = world.Item{ID: itemID(t, output), Count: 1}
	}
	return items
}

// run cooks for n ticks the way the server tick loop does.
func run(t proto.Tables, title string, items []world.Item, st world.FurnaceState, n int) world.FurnaceState {
	for range n {
		st, _ = advanceFurnace(t, title, items, st)
	}
	return st
}

func TestFurnaceSmelts(t *testing.T) {
	tab := v777.Tables
	items := furnaceWindow(tab, "minecraft:raw_iron", "minecraft:coal", "")

	st := run(tab, "container.furnace", items, world.FurnaceState{}, 200)

	if got := items[2]; got.ID != itemID(tab, "minecraft:iron_ingot") || got.Count != 1 {
		t.Fatalf("output = %+v, want one iron ingot", got)
	}
	if !items[0].Empty() {
		t.Errorf("input still holds %+v after smelting", items[0])
	}
	if !items[1].Empty() {
		t.Errorf("coal (%+v) was not consumed", items[1])
	}
	// Vanilla decrements the fuel counter at the top of the tick and only then
	// lights the machine, so the tick that lights it spends nothing: 199 of the
	// 200 ticks burn down the 1600-tick coal.
	if want := int32(1600 - 199); st.Lit != want {
		t.Errorf("fuel left = %d, want %d", st.Lit, want)
	}
}

func TestFurnaceKeepsFuelAfterOneItem(t *testing.T) {
	tab := v777.Tables
	items := furnaceWindow(tab, "minecraft:raw_iron", "minecraft:coal", "")
	items[0].Count = 3

	st := run(tab, "container.furnace", items, world.FurnaceState{}, 200)

	if items[0].Count != 2 {
		t.Errorf("input = %d, want 2 left", items[0].Count)
	}
	if st.Lit == 0 {
		t.Error("the furnace went out with fuel and input left")
	}
	if !items[1].Empty() {
		t.Errorf("the coal should still be spent: %+v", items[1])
	}
}

func TestBlastFurnaceIsTwiceAsFast(t *testing.T) {
	tab := v777.Tables
	items := furnaceWindow(tab, "minecraft:raw_iron", "minecraft:coal", "")

	st := run(tab, "container.blast_furnace", items, world.FurnaceState{}, 100)

	if got := items[2]; got.ID != itemID(tab, "minecraft:iron_ingot") {
		t.Fatalf("blast furnace produced %+v after 100 ticks", got)
	}
	// Its fuel also burns twice as fast: half of 1600, less the 99 ticks that
	// burn it down after the lighting tick.
	if want := int32(800 - 99); st.Lit != want {
		t.Errorf("fuel left = %d, want %d", st.Lit, want)
	}
}

func TestSmokerOnlySmokesFood(t *testing.T) {
	tab := v777.Tables
	items := furnaceWindow(tab, "minecraft:beef", "minecraft:coal", "")

	st := run(tab, "container.smoker", items, world.FurnaceState{}, 100)

	if got := items[2]; got.ID != itemID(tab, "minecraft:cooked_beef") {
		t.Fatalf("smoker produced %+v, want cooked beef", got)
	}
	_ = st
}

func TestSmokerIgnoresOre(t *testing.T) {
	tab := v777.Tables
	items := furnaceWindow(tab, "minecraft:raw_iron", "minecraft:coal", "")

	st := run(tab, "container.smoker", items, world.FurnaceState{}, 400)

	if !items[2].Empty() {
		t.Errorf("a smoker cooked iron: %+v", items[2])
	}
	if st.Lit != 0 {
		t.Errorf("a machine with no recipe lit up (lit=%d)", st.Lit)
	}
}

func TestFurnaceWillNotOverfillOutput(t *testing.T) {
	tab := v777.Tables
	items := furnaceWindow(tab, "minecraft:raw_iron", "minecraft:coal", "")
	items[0].Count = 64
	items[2] = world.Item{ID: itemID(tab, "minecraft:iron_ingot"), Count: 64}

	st := run(tab, "container.furnace", items, world.FurnaceState{}, 400)

	if items[2].Count != 64 {
		t.Errorf("output grew past a full stack: %+v", items[2])
	}
	if !items[0].Empty() && items[0].Count != 64 {
		t.Errorf("input was consumed into a full output: %+v", items[0])
	}
	_ = st
}

func TestLavaBucketLeavesBucket(t *testing.T) {
	tab := v777.Tables
	items := furnaceWindow(tab, "minecraft:raw_iron", "minecraft:lava_bucket", "")

	run(tab, "container.furnace", items, world.FurnaceState{}, 1)

	if got := items[1]; got.ID != itemID(tab, "minecraft:bucket") || got.Count != 1 {
		t.Fatalf("fuel slot = %+v, want an empty bucket", got)
	}
}
